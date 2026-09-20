package ipp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/OpenPrinting/goipp"
	"github.com/sschueller/brother-ql570-go/pkg/ql"
)

// Printer is the print backend the IPP server submits QL jobs to. It is a
// small interface so tests can substitute a fake.
type Printer interface {
	PrintJobs(ctx context.Context, jobs []ql.Job) (*ql.PrintResult, error)
	Status(ctx context.Context) (*ql.Status, error)
}

// maxIPPBody caps the size of one IPP request (message + document).
// A 62x100 mm RGB page at 300 dpi is ~2.3 MB; 64 MiB leaves plenty of
// headroom for multi-page PDFs while bounding memory use.
const maxIPPBody = 64 << 20

// printerState is the dynamic printer state reported through
// Get-Printer-Attributes.
type printerState struct {
	state     int // IPP printer-state enum
	reason    string
	message   string
	accepting bool
	status    *ql.Status
}

// Server implements the IPP print service for the QL-570. It is an
// http.Handler to mount on the daemon's mux; mDNS advertises its path
// (rp=ipp/print). It is deliberately unauthenticated: like a consumer
// printer it trusts the local network. Access control for the rest of the
// daemon's API is the daemon's business.
type Server struct {
	printer Printer
	name    string
	uuid    string
	store   *JobStore

	// Logf, when set, receives one line per IPP request for diagnostics.
	Logf func(format string, args ...any)

	// Hires prints IPP jobs at 600 dpi in the length direction (the
	// QL-570's high-resolution mode; 300 dpi across the width is the
	// hardware maximum). Set before serving.
	Hires bool

	// printMu serializes document processing so two jobs never touch
	// the USB path at the same time.
	printMu sync.Mutex
}

// NewServer creates an IPP server with the given printer backend, the
// printer name advertised to clients and the printer UUID (a stable
// urn:uuid:... string so clients recognize the printer across restarts).
func NewServer(printer Printer, name, uuid string) *Server {
	return &Server{
		printer: printer,
		name:    name,
		uuid:    uuid,
		store:   NewJobStore(),
	}
}

// Handler returns the http.Handler serving IPP at /ipp/print.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.ServeHTTP)
}

// ServeHTTP handles one IPP request: an IPP message body, followed by the
// document data for Print-Job and Send-Document.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	remote := r.RemoteAddr
	if r.Method != http.MethodPost {
		s.logf("ipp: %s %s %s: method not allowed", remote, r.Method, r.URL.Path)
		http.Error(w, "IPP requires POST", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxIPPBody)

	msg := &goipp.Message{}
	if err := msg.Decode(r.Body); err != nil {
		s.logf("ipp: %s %s op=?(decode) ua=%q: %v", remote, r.URL.Path, r.UserAgent(), err)
		http.Error(w, "invalid IPP message: "+err.Error(), http.StatusBadRequest)
		return
	}
	uri := printerURI(msg, r)

	op := goipp.Op(msg.Code)
	var resp *goipp.Message
	switch op {
	case goipp.OpGetPrinterAttributes:
		resp = s.getPrinterAttributes(msg)
	case goipp.OpGetJobs:
		resp = s.getJobs(msg, uri)
	case goipp.OpGetJobAttributes:
		resp = s.getJobAttributes(msg, uri)
	case goipp.OpValidateJob:
		resp = s.validateJob(msg)
	case goipp.OpCreateJob:
		resp = s.createJob(msg, uri)
	case goipp.OpSendDocument:
		doc, err := readDocument(r.Body)
		if err != nil {
			resp = errorResponse(msg, goipp.StatusErrorRequestEntity, "document too large")
			break
		}
		resp = s.sendDocument(msg, doc)
	case goipp.OpPrintJob:
		doc, err := readDocument(r.Body)
		if err != nil {
			resp = errorResponse(msg, goipp.StatusErrorRequestEntity, "document too large")
			break
		}
		resp = s.printJob(msg, doc, uri)
	case goipp.OpCancelJob:
		resp = s.cancelJob(msg)
	case goipp.OpIdentifyPrinter:
		resp = goipp.NewResponse(msg.Version, goipp.StatusOk, msg.RequestID)
		resp.Operation = operationAttrs(msg, "")
	default:
		resp = errorResponse(msg, goipp.StatusErrorOperationNotSupported, "operation not supported")
	}

	// Encode the response into a buffer first so every response carries
	// an explicit Content-Length. Some IPP clients (notably Android's
	// built-in print service, which embeds an old CUPS-derived HTTP
	// stack) do not understand chunked responses: without a
	// Content-Length they read until the connection closes, which never
	// happens on a keep-alive connection. Their capability fetch then
	// hangs until timeout and retries, and because Android fetches
	// capabilities for all printers serially, one such hang stalls the
	// entire print dialog.
	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		s.logf("ipp: %s op=%s: encoding response: %v", remote, opName(op), err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", goipp.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	cw := &countingResponseWriter{ResponseWriter: w}
	if _, err := cw.Write(buf.Bytes()); err != nil {
		s.logf("ipp: %s op=%s: writing response: %v", remote, opName(op), err)
	}
	dur := time.Since(start)
	if dur > time.Second {
		s.logf("ipp: %s op=%s ua=%q SLOW %s (%d bytes)", remote, opName(op), r.UserAgent(), dur, cw.n)
	} else {
		s.logf("ipp: %s op=%s ua=%q %s (%d bytes)", remote, opName(op), r.UserAgent(), dur, cw.n)
	}
}

// countingResponseWriter counts the bytes written to the client.
type countingResponseWriter struct {
	http.ResponseWriter
	n int64
}

func (c *countingResponseWriter) Write(p []byte) (int, error) {
	n, err := c.ResponseWriter.Write(p)
	c.n += int64(n)
	return n, err
}

// opName returns the IPP operation name for logging.
func opName(op goipp.Op) string {
	switch op {
	case goipp.OpGetPrinterAttributes:
		return "Get-Printer-Attributes"
	case goipp.OpGetJobs:
		return "Get-Jobs"
	case goipp.OpGetJobAttributes:
		return "Get-Job-Attributes"
	case goipp.OpValidateJob:
		return "Validate-Job"
	case goipp.OpCreateJob:
		return "Create-Job"
	case goipp.OpSendDocument:
		return "Send-Document"
	case goipp.OpPrintJob:
		return "Print-Job"
	case goipp.OpCancelJob:
		return "Cancel-Job"
	case goipp.OpIdentifyPrinter:
		return "Identify-Printer"
	}
	return fmt.Sprintf("Op-0x%04x", uint16(op))
}

// logf emits a diagnostic line when a logger is configured.
func (s *Server) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// readDocument reads the document data following the IPP message.
func readDocument(body io.Reader) ([]byte, error) {
	return io.ReadAll(body)
}

// errorResponse builds a response with the given status and message.
func errorResponse(req *goipp.Message, status goipp.Status, msg string) *goipp.Message {
	resp := goipp.NewResponse(req.Version, status, req.RequestID)
	resp.Operation = operationAttrs(req, msg)
	return resp
}

// currentPrinterState queries the printer for the dynamic attributes. A
// failed status read means the printer is unreachable. While another job
// is printing (printMu held), the printer reports "processing".
func (s *Server) currentPrinterState() *printerState {
	if !s.printMu.TryLock() {
		return &printerState{state: 4, reason: "printing", message: "printing", accepting: true}
	}
	defer s.printMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	st, err := s.printer.Status(ctx)
	if err != nil {
		return &printerState{state: 5, reason: "printer-unreachable", message: err.Error(), accepting: false}
	}
	return &printerState{state: 3, reason: "none", accepting: true, status: st}
}

// printerAvailable reports whether jobs may be submitted right now: the
// printer must be connected (it may be busy printing, jobs are queued).
func (s *Server) printerAvailable() error {
	st := s.currentPrinterState()
	if st.state == 5 {
		return errors.New("printer not connected")
	}
	return nil
}

// getPrinterAttributes answers Get-Printer-Attributes.
func (s *Server) getPrinterAttributes(msg *goipp.Message) *goipp.Message {
	st := s.currentPrinterState()
	attrs := filterAttributes(printerAttributes(s, msg, st), msg)
	resp := goipp.NewResponse(msg.Version, goipp.StatusOk, msg.RequestID)
	resp.Operation = operationAttrs(msg, "")
	resp.Printer = attrs
	return resp
}

// getJobs answers Get-Jobs with one Job group per stored job.
func (s *Server) getJobs(msg *goipp.Message, uri string) *goipp.Message {
	resp := goipp.NewResponse(msg.Version, goipp.StatusOk, msg.RequestID)
	resp.Operation = operationAttrs(msg, "")
	groups := goipp.Groups{{Tag: goipp.TagOperationGroup, Attrs: resp.Operation}}
	for _, job := range s.store.List() {
		attrs := filterAttributes(jobAttributes(job, uri), msg)
		groups = append(groups, goipp.Group{Tag: goipp.TagJobGroup, Attrs: attrs})
	}
	resp.Groups = groups
	return resp
}

// getJobAttributes answers Get-Job-Attributes.
func (s *Server) getJobAttributes(msg *goipp.Message, uri string) *goipp.Message {
	job, resp := s.lookupJob(msg)
	if job == nil {
		return resp
	}
	out := goipp.NewResponse(msg.Version, goipp.StatusOk, msg.RequestID)
	out.Operation = operationAttrs(msg, "")
	out.Job = filterAttributes(jobAttributes(job, uri), msg)
	return out
}

// validateJob answers Validate-Job without printing anything.
func (s *Server) validateJob(msg *goipp.Message) *goipp.Message {
	if unsupported := unsupportedJobAttrs(msg); len(unsupported) > 0 {
		resp := errorResponse(msg, goipp.StatusErrorAttributesOrValues, "unsupported job attributes")
		resp.Unsupported = unsupported
		return resp
	}
	resp := goipp.NewResponse(msg.Version, goipp.StatusOk, msg.RequestID)
	resp.Operation = operationAttrs(msg, "")
	return resp
}

// createJob answers Create-Job: the job is registered and waits for its
// document via Send-Document.
func (s *Server) createJob(msg *goipp.Message, uri string) *goipp.Message {
	if unsupported := unsupportedJobAttrs(msg); len(unsupported) > 0 {
		resp := errorResponse(msg, goipp.StatusErrorAttributesOrValues, "unsupported job attributes")
		resp.Unsupported = unsupported
		return resp
	}
	if err := s.printerAvailable(); err != nil {
		return errorResponse(msg, goipp.StatusErrorServiceUnavailable, err.Error())
	}
	job := s.store.Add(
		attrString(msg.Operation, "job-name"),
		attrString(msg.Operation, "requesting-user-name"),
		uri,
		msg.Job.Clone(),
	)
	resp := goipp.NewResponse(msg.Version, goipp.StatusOk, msg.RequestID)
	resp.Operation = operationAttrs(msg, "")
	resp.Job = jobSubmissionAttributes(job)
	return resp
}

// sendDocument answers Send-Document: the document is attached to the job
// and, when it is the last document, processing starts.
func (s *Server) sendDocument(msg *goipp.Message, doc []byte) *goipp.Message {
	job, resp := s.lookupJob(msg)
	if job == nil {
		return resp
	}
	if job.DocSet {
		return errorResponse(msg, goipp.StatusErrorNotPossible, "only one document per job is supported")
	}
	state, _, _, _ := job.snapshot()
	if state == StateCanceled {
		return errorResponse(msg, goipp.StatusErrorNotPossible, "job was canceled")
	}
	format := attrString(msg.Document, "document-format")
	if format == "" {
		format = sniffFormat(doc)
	}
	if unsupported := unsupportedFormat(format); unsupported != "" {
		resp := errorResponse(msg, goipp.StatusErrorDocumentFormatNotSupported, unsupported)
		resp.Unsupported = goipp.Attributes{goipp.MakeAttribute("document-format", goipp.TagMimeType, goipp.String(format))}
		return resp
	}
	job.Doc = doc
	job.DocSet = true
	job.DocFormat = format

	last := true
	if a := findAttr(msg.Document, "last-document"); a != nil {
		if b, ok := attrValueBool(a); ok {
			last = b
		}
	}
	if last {
		s.startProcessing(job)
	}

	resp2 := goipp.NewResponse(msg.Version, goipp.StatusOk, msg.RequestID)
	resp2.Operation = operationAttrs(msg, "")
	resp2.Job = jobSubmissionAttributes(job)
	return resp2
}

// printJob answers Print-Job: job creation and document submission in one
// operation.
func (s *Server) printJob(msg *goipp.Message, doc []byte, uri string) *goipp.Message {
	if unsupported := unsupportedJobAttrs(msg); len(unsupported) > 0 {
		resp := errorResponse(msg, goipp.StatusErrorAttributesOrValues, "unsupported job attributes")
		resp.Unsupported = unsupported
		return resp
	}
	format := attrString(msg.Operation, "document-format")
	if format == "" {
		format = sniffFormat(doc)
	}
	if errText := unsupportedFormat(format); errText != "" {
		resp := errorResponse(msg, goipp.StatusErrorDocumentFormatNotSupported, errText)
		resp.Unsupported = goipp.Attributes{goipp.MakeAttribute("document-format", goipp.TagMimeType, goipp.String(format))}
		return resp
	}
	if err := s.printerAvailable(); err != nil {
		return errorResponse(msg, goipp.StatusErrorServiceUnavailable, err.Error())
	}

	job := s.store.Add(
		attrString(msg.Operation, "job-name"),
		attrString(msg.Operation, "requesting-user-name"),
		uri,
		msg.Job.Clone(),
	)
	job.Doc = doc
	job.DocSet = true
	job.DocFormat = format
	s.startProcessing(job)

	resp := goipp.NewResponse(msg.Version, goipp.StatusOk, msg.RequestID)
	resp.Operation = operationAttrs(msg, "")
	resp.Job = jobSubmissionAttributes(job)
	return resp
}

// startProcessing launches the print processing for a job in the
// background; the IPP response does not wait for the physical print.
func (s *Server) startProcessing(job *StoredJob) {
	job.started.Store(true)
	go func() {
		s.printMu.Lock()
		defer s.printMu.Unlock()

		if job.Canceled() {
			job.setState(StateCanceled, "job canceled before printing")
			return
		}
		job.setState(StateProcessing, "")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		stCtx, stCancel := context.WithTimeout(ctx, 5*time.Second)
		st, stErr := s.printer.Status(stCtx)
		stCancel()
		if stErr != nil {
			// Media resolution falls back to defaults; PrintJobs
			// will report the connection problem with a clear error.
			st = nil
		}
		qlJobs, err := IPPJobToQL(job.Attrs, job.Doc, job.DocFormat, st, s.Hires)
		if err != nil {
			job.setState(StateAborted, err.Error())
			return
		}
		defer qlJobs.Cleanup()
		if job.Canceled() {
			job.setState(StateCanceled, "job canceled before printing")
			return
		}
		if _, err = s.printer.PrintJobs(ctx, qlJobs.Jobs); err != nil {
			job.setState(StateAborted, err.Error())
			return
		}
		if job.Canceled() {
			job.setState(StateCanceled, "job canceled by user")
			return
		}
		job.setState(StateCompleted, "")
	}()
}

// cancelJob answers Cancel-Job.
func (s *Server) cancelJob(msg *goipp.Message) *goipp.Message {
	job, resp := s.lookupJob(msg)
	if job == nil {
		return resp
	}
	if !job.Cancel() {
		return errorResponse(msg, goipp.StatusErrorNotPossible, "job already finished")
	}
	if !job.started.Load() {
		job.setState(StateCanceled, "job canceled by user")
	}
	out := goipp.NewResponse(msg.Version, goipp.StatusOk, msg.RequestID)
	out.Operation = operationAttrs(msg, "")
	return out
}

// lookupJob resolves the job-id operation attribute and returns the job,
// or nil with an error response when the job is unknown or the attribute
// is missing.
func (s *Server) lookupJob(msg *goipp.Message) (*StoredJob, *goipp.Message) {
	a := findAttr(msg.Operation, "job-id")
	if a == nil {
		return nil, errorResponse(msg, goipp.StatusErrorBadRequest, "missing job-id")
	}
	id, ok := attrValueInt(a)
	if !ok {
		return nil, errorResponse(msg, goipp.StatusErrorBadRequest, "job-id is not an integer")
	}
	job, found := s.store.Get(int32(id))
	if !found {
		return nil, errorResponse(msg, goipp.StatusErrorNotFound, "job not found")
	}
	return job, nil
}

// jobSubmissionAttributes is the Job group of a submission response.
func jobSubmissionAttributes(job *StoredJob) goipp.Attributes {
	state, _, _, _ := job.snapshot()
	return goipp.Attributes{
		goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(job.ID)),
		goipp.MakeAttribute("job-uri", goipp.TagURI, goipp.String(job.URI)),
		goipp.MakeAttribute("job-state", goipp.TagEnum, goipp.Integer(jobStateEnum(state))),
		goipp.MakeAttribute("job-state-reasons", goipp.TagKeyword, goipp.String("none")),
	}
}

// printerURI returns the printer-uri the client used, or reconstructs it
// from the request.
func printerURI(msg *goipp.Message, r *http.Request) string {
	if a := findAttr(msg.Operation, "printer-uri"); a != nil {
		if uri, ok := attrValueString(a); ok && uri != "" {
			return uri
		}
	}
	scheme := "ipp"
	if r.TLS != nil {
		scheme = "ipps"
	}
	return scheme + "://" + r.Host + "/ipp/print"
}

// attrString returns the string value of the named attribute in attrs.
func attrString(attrs goipp.Attributes, name string) string {
	if a := findAttr(attrs, name); a != nil {
		if v, ok := attrValueString(a); ok {
			return v
		}
	}
	return ""
}

// unsupportedFormat reports why a document format is not supported, or ""
// when it is.
func unsupportedFormat(format string) string {
	switch format {
	case FormatPWGRaster, FormatPDF:
		return ""
	}
	return "document format not supported (supported: image/pwg-raster, application/pdf)"
}

// unsupportedJobAttrs collects the job attributes this printer cannot
// honor (e.g. duplex printing or an unknown media size) for the
// Unsupported attributes group.
func unsupportedJobAttrs(msg *goipp.Message) goipp.Attributes {
	var unsupported goipp.Attributes
	if a := findAttr(msg.Job, "sides"); a != nil {
		if v, ok := attrValueString(a); ok && v != "one-sided" {
			unsupported = append(unsupported, *a)
		}
	}
	if a := findAttr(msg.Job, "copies"); a != nil {
		if n, ok := attrValueInt(a); ok && (n < 1 || n > 255) {
			unsupported = append(unsupported, *a)
		}
	}
	if a := findAttr(msg.Job, "media-col"); a != nil {
		if _, err := mediaFromCollection(a); err != nil {
			unsupported = append(unsupported, *a)
		}
	} else if a := findAttr(msg.Job, "media"); a != nil {
		if v, ok := attrValueString(a); ok {
			if _, err := LookupIPPMediaByName(v); err != nil {
				unsupported = append(unsupported, *a)
			}
		}
	}
	if format := attrString(msg.Operation, "document-format"); format != "" {
		if unsupportedFormat(format) != "" {
			unsupported = append(unsupported, goipp.MakeAttribute("document-format", goipp.TagMimeType, goipp.String(format)))
		}
	}
	return unsupported
}
