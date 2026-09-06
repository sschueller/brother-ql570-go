package ql

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Printer is the high-level printing API. It owns a Backend and serializes
// access so a single Printer can be shared by concurrent callers (e.g. the
// HTTP daemon).
type Printer struct {
	mu      sync.Mutex
	backend Backend
}

// NewPrinter wraps an existing Backend.
func NewPrinter(backend Backend) *Printer { return &Printer{backend: backend} }

// Open opens the printer on the given usblp device (e.g. /dev/usb/lp0).
// If device is empty, a connected QL-570 is auto-discovered.
func Open(device string) (*Printer, error) {
	b, err := OpenUSB(device)
	if err != nil {
		return nil, err
	}
	return NewPrinter(b), nil
}

// Backend returns the underlying backend.
func (p *Printer) Backend() Backend { return p.backend }

// Close closes the printer.
func (p *Printer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.backend.Close()
}

// Status sends a status information request and returns the decoded
// 32-byte status response.
func (p *Printer) Status(ctx context.Context) (*Status, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.statusLocked(ctx, 3*time.Second)
}

// Info returns the same status information as Status. Note: the official
// QL-570 raster command set has no command to query the firmware version
// or serial number; only the model identification bytes (part of every
// status response) and the current media state are exposed.
func (p *Printer) Info(ctx context.Context) (*Status, error) {
	return p.Status(ctx)
}

// PrintResult summarizes a finished print job.
type PrintResult struct {
	// Printed is true if the printer reported "printing completed".
	Printed bool `json:"printed"`
	// Ready is true if the printer reported the "waiting to receive"
	// phase change, i.e. it accepts new jobs.
	Ready bool `json:"ready"`
	// Statuses contains every status response received after the job.
	Statuses []*Status `json:"statuses,omitempty"`
	// Status is the last received status.
	Status *Status `json:"status,omitempty"`
}

// HasErrors reports whether any received status carried error information.
func (r *PrintResult) HasErrors() bool {
	for _, s := range r.Statuses {
		if s.HasErrors() {
			return true
		}
	}
	return false
}

// Print renders the job and prints it. See PrintJobs for details.
func (p *Printer) Print(ctx context.Context, job *Job) (*PrintResult, error) {
	return p.PrintJobs(ctx, []Job{*job})
}

// PrintJobs renders each job and prints them as pages of a single print
// job. Jobs with Copies > 1 contribute that many consecutive pages. All
// jobs must use the same media (e.g. different label lengths on the same
// continuous roll is fine; mixing 29mm and 62mm tape is not).
//
// Following the official printing procedure, the printer status is checked
// first: if the loaded media does not match the jobs, or the printer
// already reports errors, no print data is sent at all (avoids wasting
// tape and wedging the printer). The call then blocks until the printer
// reports printing completed (or an error status), until ctx is done, or
// until the 10 second status timeout expires.
func (p *Printer) PrintJobs(ctx context.Context, jobs []Job) (*PrintResult, error) {
	if len(jobs) == 0 {
		return nil, fmt.Errorf("no jobs to print")
	}

	media := make([]Media, len(jobs))
	for i := range jobs {
		j := jobs[i]
		j.DefaultJobValues()
		m, err := j.Validate()
		if err != nil {
			return nil, fmt.Errorf("job %d: %w", i+1, err)
		}
		jobs[i] = j
		media[i] = m
	}
	for i := 1; i < len(jobs); i++ {
		if media[i].ID != media[0].ID {
			return nil, fmt.Errorf("all jobs in a batch must use the same media: job 1 uses %q, job %d uses %q",
				media[0].ID, i+1, media[i].ID)
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Pre-flight status check.
	st, err := p.statusLocked(ctx, 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("printer status check failed: %w", err)
	}
	if st.HasErrors() {
		p.resetUSB()
		return nil, fmt.Errorf("printer reports errors: %v (fix the media and clear the printer error before printing)", st.Errors)
	}
	if err := checkMediaMatch(st, media[0]); err != nil {
		return nil, err
	}

	var pages []JobPage
	for i := range jobs {
		rows, err := RenderJob(&jobs[i], media[i])
		if err != nil {
			return nil, fmt.Errorf("job %d: %w", i+1, err)
		}
		opts := jobOptions(&jobs[i], media[i])
		for c := 0; c < jobs[i].Copies; c++ {
			pages = append(pages, JobPage{Opts: opts, Rows: rows})
		}
	}
	stream := BuildJobsStream(QL570, pages)

	if _, err := writeAll(p.backend, stream); err != nil {
		p.resetUSB()
		return nil, fmt.Errorf("writing job to printer: %w", err)
	}

	statuses, err := p.collectStatuses(ctx, 10*time.Second)
	res := &PrintResult{Statuses: statuses}
	if len(statuses) > 0 {
		res.Status = statuses[len(statuses)-1]
	}
	for _, s := range statuses {
		if s.StatusType == StatusTypePrintingCompleted {
			res.Printed = true
		}
		if s.StatusType == StatusTypePhaseChange && s.PhaseType == PhaseTypeWaitingToReceive {
			res.Ready = true
		}
	}
	if res.HasErrors() {
		p.resetUSB()
		return res, fmt.Errorf("printer reported errors: %v", errorStrings(statuses))
	}
	if err != nil {
		return res, err
	}
	if !res.Printed || !res.Ready {
		return res, fmt.Errorf("print outcome unconfirmed (printed=%v ready=%v)", res.Printed, res.Ready)
	}
	return res, nil
}

// checkMediaMatch verifies that the media reported by the printer status
// matches the job's media, so mismatched jobs are rejected before any
// print data reaches the printer.
func checkMediaMatch(st *Status, media Media) error {
	if st.MediaType == MediaTypeNone {
		return nil // no media info in the response; let the printer decide
	}
	var wantType byte
	switch media.FormFactor {
	case Endless:
		wantType = MediaTypeContinuous
	case DieCut, RoundDieCut:
		wantType = MediaTypeDieCut
	}
	if st.MediaType != wantType {
		return fmt.Errorf("printer has %s loaded but the job uses %s media %q; load the matching media or change --media",
			MediaTypeName(st.MediaType), media.FormFactor, media.ID)
	}
	if st.MediaWidthMM != 0 && st.MediaWidthMM != media.TapeWidthMM {
		return fmt.Errorf("printer has %d mm media loaded but the job uses %s (%d mm); load the matching media or change --media",
			st.MediaWidthMM, media.ID, media.TapeWidthMM)
	}
	if wantType == MediaTypeDieCut && st.MediaLengthMM != 0 && st.MediaLengthMM != media.TapeLengthMM {
		return fmt.Errorf("printer has %d mm x %d mm media loaded but the job uses %s (%d x %d mm)",
			st.MediaWidthMM, st.MediaLengthMM, media.ID, media.TapeWidthMM, media.TapeLengthMM)
	}
	return nil
}

// statusLocked performs a status request without taking the printer mutex.
func (p *Printer) statusLocked(ctx context.Context, timeout time.Duration) (*Status, error) {
	if _, err := writeAll(p.backend, cmdStatusRequest()); err != nil {
		return nil, fmt.Errorf("sending status request: %w", err)
	}
	return p.readStatus(ctx, timeout)
}

// resetUSB closes and reopens the USB device handle. Releasing the usblp
// handle clears endpoint stalls and resets the USB state, so the next job
// starts from a clean slate even after printer-side errors.
func (p *Printer) resetUSB() {
	if ub, ok := p.backend.(*USBBackend); ok {
		if err := ub.Reopen(); err != nil {
			// Keep going; the next operation reports the broken state.
			_ = err
		}
	}
}

// BuildJobBytes renders a job into the complete raw command stream without
// touching a printer (used by --dry-run and by library users who want to
// send the bytes themselves). It also returns the resolved media.
func BuildJobBytes(job *Job) ([]byte, Media, error) {
	j := *job
	j.DefaultJobValues()
	media, err := j.Validate()
	if err != nil {
		return nil, Media{}, err
	}
	rows, err := RenderJob(&j, media)
	if err != nil {
		return nil, Media{}, err
	}
	return BuildJobStream(QL570, jobOptions(&j, media), rows), media, nil
}

// BuildJobsBytes renders a batch of jobs (one label per job, respecting
// each job's copies) into a single raw command stream without touching a
// printer. The jobs must use the same media.
func BuildJobsBytes(jobs []Job) ([]byte, []Media, error) {
	if len(jobs) == 0 {
		return nil, nil, fmt.Errorf("no jobs to render")
	}
	media := make([]Media, len(jobs))
	var pages []JobPage
	for i := range jobs {
		j := jobs[i]
		j.DefaultJobValues()
		m, err := j.Validate()
		if err != nil {
			return nil, nil, fmt.Errorf("job %d: %w", i+1, err)
		}
		if i > 0 && m.ID != media[0].ID {
			return nil, nil, fmt.Errorf("all jobs in a batch must use the same media: job 1 uses %q, job %d uses %q",
				media[0].ID, i+1, m.ID)
		}
		media[i] = m
		rows, err := RenderJob(&j, m)
		if err != nil {
			return nil, nil, fmt.Errorf("job %d: %w", i+1, err)
		}
		opts := jobOptions(&j, m)
		for c := 0; c < j.Copies; c++ {
			pages = append(pages, JobPage{Opts: opts, Rows: rows})
		}
	}
	return BuildJobsStream(QL570, pages), media, nil
}

// jobOptions translates a validated job into the protocol-level options.
func jobOptions(j *Job, media Media) PrintOptions {
	opts := PrintOptions{
		WidthMM:  media.TapeWidthMM,
		Pages:    j.Copies,
		AutoCut:  j.AutoCut(),
		CutEvery: j.CutEvery,
		CutAtEnd: j.AutoCut(),
		Hires600: j.Hires,
		FeedDots: media.FeedMarginDots,
		Quality:  j.QualityPriority(),
	}
	if media.FormFactor == Endless {
		opts.MediaType = MediaTypeContinuous
		opts.LengthMM = 0
	} else {
		opts.MediaType = MediaTypeDieCut
		opts.LengthMM = media.TapeLengthMM
	}
	if j.FeedDots > 0 {
		opts.FeedDots = j.FeedDots
	}
	return opts
}

// readStatus reads and parses a single 32-byte status response.
func (p *Printer) readStatus(ctx context.Context, timeout time.Duration) (*Status, error) {
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	buf := make([]byte, 0, statusLen)
	for time.Now().Before(deadline) && len(buf) < statusLen {
		if ub, ok := p.backend.(*USBBackend); ok {
			if err := ub.SetReadDeadline(deadline); err != nil {
				return nil, fmt.Errorf("setting read deadline: %w", err)
			}
		}
		chunk := make([]byte, statusLen-len(buf))
		n, err := p.backend.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		if err != nil {
			if isTimeoutErr(err) {
				break
			}
			return nil, fmt.Errorf("reading status: %w", err)
		}
	}
	if len(buf) == 0 {
		return nil, errors.New("no response from printer (timeout)")
	}
	st, err := ParseStatus(buf)
	if err != nil {
		return nil, err
	}
	return st, nil
}

// collectStatuses drains status responses after a print job until the
// printer reports printing completed and is waiting to receive, or until
// the overall timeout expires.
func (p *Printer) collectStatuses(ctx context.Context, timeout time.Duration) ([]*Status, error) {
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	var statuses []*Status
	buf := make([]byte, 0, statusLen)
	printed, ready := false, false

	for time.Now().Before(deadline) && !(printed && ready) {
		rd := 100 * time.Millisecond
		if remaining := time.Until(deadline); remaining < rd {
			rd = remaining
		}
		if ub, ok := p.backend.(*USBBackend); ok {
			if err := ub.SetReadDeadline(time.Now().Add(rd)); err != nil {
				return statuses, fmt.Errorf("setting read deadline: %w", err)
			}
		}
		chunk := make([]byte, 32-len(buf))
		n, err := p.backend.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		if err != nil {
			if isTimeoutErr(err) {
				continue
			}
			return statuses, fmt.Errorf("reading printer status: %w", err)
		}
		for len(buf) >= statusLen {
			st, perr := ParseStatus(buf[:statusLen])
			if perr != nil {
				// Skip frames that do not start with the status header
				// (can happen if data arrived out of sync); shift by one
				// byte and retry.
				buf = buf[1:]
				continue
			}
			buf = buf[statusLen:]
			statuses = append(statuses, st)
			if st.StatusType == StatusTypePrintingCompleted {
				printed = true
			}
			if st.StatusType == StatusTypePhaseChange && st.PhaseType == PhaseTypeWaitingToReceive {
				ready = true
			}
		}
	}
	if len(statuses) == 0 {
		return statuses, errors.New("no status received from printer (timeout)")
	}
	return statuses, nil
}

func errorStrings(statuses []*Status) []string {
	var out []string
	for _, s := range statuses {
		out = append(out, s.Errors...)
	}
	return out
}

func isTimeoutErr(err error) bool {
	return errors.Is(err, os.ErrDeadlineExceeded)
}

func writeAll(w io.Writer, data []byte) (int, error) {
	total := 0
	for total < len(data) {
		n, err := w.Write(data[total:])
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}
