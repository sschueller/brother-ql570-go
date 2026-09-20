package ipp

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/OpenPrinting/goipp"
	"github.com/sschueller/brother-ql570-go/pkg/pdf"
	"github.com/sschueller/brother-ql570-go/pkg/ql"
)

// Document formats accepted by the IPP server.
const (
	FormatPWGRaster = "image/pwg-raster"
	FormatPDF       = "application/pdf"
)

// Logf, when set, receives diagnostics about decoded documents (page
// dimensions, orientation handling).
var Logf func(format string, args ...any)

func logf(format string, args ...any) {
	if Logf != nil {
		Logf(format, args...)
	}
}

// QLJobs is the result of mapping one IPP document to QL print jobs: one
// label per page.
type QLJobs struct {
	// Jobs is one ql.Job per page of the document. All jobs use the same
	// media.
	Jobs []ql.Job
	// Media is the resolved media option (shared by all jobs).
	Media MediaOption
	// TempFiles are the temporary PNG files created for the page images.
	// Call Cleanup after printing.
	TempFiles []string
}

// Cleanup removes the temporary page image files.
func (q *QLJobs) Cleanup() {
	for _, p := range q.TempFiles {
		_ = os.Remove(p)
	}
	q.TempFiles = nil
}

// IPPJobToQL converts an IPP job (job template attributes plus the
// document) into QL print jobs. The document must be PWG Raster or PDF;
// each page becomes one label on the selected media. Standard media sizes
// are resolved against the printer status st (may be nil: 62 mm
// continuous tape is assumed). hires enables the QL-570's 600 dpi mode
// along the label length. Temporary PNG files for the page images are
// written to os.TempDir() and must be removed with Cleanup once the
// print finished.
func IPPJobToQL(attrs goipp.Attributes, doc []byte, format string, st *ql.Status, hires bool) (*QLJobs, error) {
	parsed, err := parseJobAttrs(attrs)
	if err != nil {
		return nil, err
	}
	parsed.hires = hires
	media, err := ResolveMedia(parsed.media, st)
	if err != nil {
		return nil, err
	}
	parsed.media = media
	if format == "" || format == "application/octet-stream" {
		format = sniffFormat(doc)
	}

	res := &QLJobs{Media: parsed.media}
	switch format {
	case FormatPWGRaster:
		pages, err := DecodePWGRaster(doc)
		if err != nil {
			return nil, err
		}
		if !pwgMediaCompatible(parsed.media, pages[0]) {
			return nil, fmt.Errorf("document page size %q does not match the selected media %q", pages[0].PageSizeName, parsed.media.Name)
		}
		for i, page := range pages {
			logf("ipp: pwg page %d/%d: %dx%dpx orientation=%d bpp=%d colorspace=%d",
				i+1, len(pages), page.Width, page.Height, page.Orientation, page.BitsPerPixel, page.ColorSpace)
			path, err := writeTempPNG(parsed.prefix, i+1, page.Image)
			if err != nil {
				res.Cleanup()
				return nil, err
			}
			res.TempFiles = append(res.TempFiles, path)
			res.Jobs = append(res.Jobs, pageJob(parsed, path))
		}
	case FormatPDF:
		if err := pdf.Init(); err != nil {
			return nil, fmt.Errorf("starting the PDF engine: %w", err)
		}
		count, err := pdf.PageCount(doc)
		if err != nil {
			return nil, fmt.Errorf("parsing PDF: %w", err)
		}
		if count == 0 {
			return nil, fmt.Errorf("PDF has no pages")
		}
		if count > pdf.MaxPages {
			return nil, fmt.Errorf("PDF has %d pages (max %d); split the document or submit it in parts", count, pdf.MaxPages)
		}
		idxs := make([]int, count)
		for i := range idxs {
			idxs[i] = i
		}
		paths, err := pdf.RenderPNGFiles(doc, idxs, pdf.MaxPixels, os.TempDir(), parsed.prefix)
		if err != nil {
			return nil, fmt.Errorf("rendering PDF: %w", err)
		}
		res.TempFiles = append(res.TempFiles, paths...)
		// Android sends PDFs unrotated and conveys the user's
		// portrait/landscape choice via the orientation-requested job
		// attribute; the printer applies it.
		if parsed.orientation != 0 {
			for i, path := range paths {
				img, err := readGrayPNG(path)
				if err != nil {
					res.Cleanup()
					return nil, fmt.Errorf("reading rendered page %d: %w", i+1, err)
				}
				rotated := applyOrientation(img, parsed.orientation)
				if rotated != img {
					if err := writeGrayPNG(path, rotated); err != nil {
						res.Cleanup()
						return nil, fmt.Errorf("storing oriented page %d: %w", i+1, err)
					}
					logf("ipp: pdf page %d/%d: rotated %dx%dpx -> %dx%dpx (orientation-requested=%d)",
						i+1, count, img.Rect.Dx(), img.Rect.Dy(), rotated.Rect.Dx(), rotated.Rect.Dy(), parsed.orientation)
				}
			}
		}
		for _, path := range paths {
			res.Jobs = append(res.Jobs, pageJob(parsed, path))
		}
	default:
		return nil, fmt.Errorf("document format %q is not supported (supported: %s, %s)", format, FormatPWGRaster, FormatPDF)
	}
	return res, nil
}

// parsedJobAttrs is the subset of the IPP job template attributes the
// mapper understands.
type parsedJobAttrs struct {
	media       MediaOption
	mediaSet    bool
	copies      int
	orientation int
	hires       bool
	prefix      string
}

// parseJobAttrs extracts media, copies, orientation and a temp-file prefix
// from IPP job attributes.
func parseJobAttrs(attrs goipp.Attributes) (parsedJobAttrs, error) {
	out := parsedJobAttrs{
		media:  DefaultMediaOption(),
		copies: 1,
		prefix: tempPrefix(),
	}

	// media-col (a collection with a media-size member) is preferred;
	// Android sends this. Fall back to the "media" keyword for clients
	// that only send legacy media names.
	if attr := findAttr(attrs, "media-col"); attr != nil {
		media, err := mediaFromCollection(attr)
		if err != nil {
			return out, err
		}
		out.media = media
		out.mediaSet = true
	} else if attr := findAttr(attrs, "media"); attr != nil {
		if name, ok := attrValueString(attr); ok {
			if media, err := LookupIPPMediaByName(name); err == nil {
				out.media = media
				out.mediaSet = true
			} else {
				return out, err
			}
		}
	}

	if attr := findAttr(attrs, "copies"); attr != nil {
		if n, ok := attrValueInt(attr); ok {
			if n < 1 || n > 255 {
				return out, fmt.Errorf("copies must be 1..255, got %d", n)
			}
			out.copies = n
		}
	}
	if attr := findAttr(attrs, "orientation-requested"); attr != nil {
		if n, ok := attrValueInt(attr); ok {
			switch n {
			case orientationPortrait, orientationLandscape, orientationReversePortrait, orientationReverseLandscape:
				out.orientation = n
			}
		}
	}
	return out, nil
}

// mediaFromCollection extracts the media option from a media-col attribute.
func mediaFromCollection(attr *goipp.Attribute) (MediaOption, error) {
	if len(attr.Values) == 0 {
		return MediaOption{}, fmt.Errorf("empty media-col")
	}
	col, ok := attr.Values[0].V.(goipp.Collection)
	if !ok {
		return MediaOption{}, fmt.Errorf("media-col has unexpected value type %T", attr.Values[0].V)
	}
	size := findAttr(goipp.Attributes(col), "media-size")
	if size == nil {
		return MediaOption{}, fmt.Errorf("media-col has no media-size member")
	}
	if len(size.Values) == 0 {
		return MediaOption{}, fmt.Errorf("empty media-size")
	}
	scol, ok := size.Values[0].V.(goipp.Collection)
	if !ok {
		return MediaOption{}, fmt.Errorf("media-size has unexpected value type %T", size.Values[0].V)
	}
	xAttr := findAttr(goipp.Attributes(scol), "x-dimension")
	yAttr := findAttr(goipp.Attributes(scol), "y-dimension")
	if xAttr == nil || yAttr == nil {
		return MediaOption{}, fmt.Errorf("media-size has no x-dimension/y-dimension")
	}
	x, xok := attrValueInt(xAttr)
	y, yok := attrValueInt(yAttr)
	if !xok || !yok {
		return MediaOption{}, fmt.Errorf("media-size dimensions are not integers")
	}
	return LookupIPPMedia(x, y)
}

// pwgMediaCompatible reports whether the PWG page's self-describing media
// name is compatible with the selected media option. An empty or unknown
// name is accepted (the media-col selection governs).
func pwgMediaCompatible(opt MediaOption, page *PWGPage) bool {
	if page.PageSizeName == "" {
		return true
	}
	m, err := LookupIPPMediaByName(page.PageSizeName)
	if err != nil {
		return true
	}
	return m.Name == opt.Name
}

// pageJob builds the ql.Job for one rendered page.
func pageJob(parsed parsedJobAttrs, imagePath string) ql.Job {
	job := ql.Job{
		Media:    parsed.media.Media.ID,
		Image:    imagePath,
		ImageFit: ql.ImageFitLabel,
		Copies:   parsed.copies,
		LengthMM: parsed.media.LengthMM,
		Hires:    parsed.hires,
	}
	job.DefaultJobValues()
	return job
}

// writeTempPNG stores a grayscale image as a temporary PNG file and
// returns its path.
func writeTempPNG(prefix string, page int, img *image.Gray) (string, error) {
	path := filepath.Join(os.TempDir(), fmt.Sprintf("%s-p%d.png", prefix, page))
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("storing page image: %w", err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("encoding page image: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("storing page image: %w", err)
	}
	return path, nil
}

// sniffFormat guesses the document format from its magic bytes.
func sniffFormat(data []byte) string {
	if pdf.IsPDF(data) {
		return FormatPDF
	}
	if len(data) >= 4 && string(data[:4]) == "RaS2" {
		return FormatPWGRaster
	}
	return ""
}

// findAttr returns the first attribute with the given name.
func findAttr(attrs goipp.Attributes, name string) *goipp.Attribute {
	for i := range attrs {
		if attrs[i].Name == name {
			return &attrs[i]
		}
	}
	return nil
}

// attrValueInt returns the integer value of an attribute.
func attrValueInt(attr *goipp.Attribute) (int, bool) {
	for _, v := range attr.Values {
		if iv, ok := v.V.(goipp.Integer); ok {
			return int(iv), true
		}
	}
	return 0, false
}

// attrValueString returns the string value of an attribute.
func attrValueString(attr *goipp.Attribute) (string, bool) {
	for _, v := range attr.Values {
		if sv, ok := v.V.(goipp.String); ok {
			return string(sv), true
		}
	}
	return "", false
}

// attrValueBool returns the boolean value of an attribute.
func attrValueBool(attr *goipp.Attribute) (bool, bool) {
	for _, v := range attr.Values {
		if bv, ok := v.V.(goipp.Boolean); ok {
			return bool(bv), true
		}
	}
	return false, false
}

// tempPrefix returns a random file name prefix for temporary page images.
func tempPrefix() string {
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return fmt.Sprintf("ql570-ipp-%d", time.Now().UnixNano())
	}
	return "ql570-ipp-" + hex.EncodeToString(id[:])
}

// JobState is the lifecycle state of an IPP job.
type JobState string

// Job states reported through Get-Jobs / Get-Job-Attributes.
const (
	StatePending    JobState = "pending"
	StateProcessing JobState = "processing"
	StateCompleted  JobState = "completed"
	StateAborted    JobState = "aborted"
	StateCanceled   JobState = "canceled"
)

// IPP job-state enum values (RFC 8011 section 5.3.7).
const (
	jobStatePending    = 3
	jobStateProcessing = 5
	jobStateCanceled   = 7
	jobStateAborted    = 8
	jobStateCompleted  = 9
)

// jobStateEnum maps a JobState to its IPP job-state enum value.
func jobStateEnum(s JobState) int {
	switch s {
	case StatePending:
		return jobStatePending
	case StateProcessing:
		return jobStateProcessing
	case StateCanceled:
		return jobStateCanceled
	case StateAborted:
		return jobStateAborted
	case StateCompleted:
		return jobStateCompleted
	}
	return jobStatePending
}

// StoredJob is one job tracked by the server's in-memory job store.
type StoredJob struct {
	ID   int32
	Name string
	User string
	URI  string

	mu        sync.Mutex
	state     JobState
	stateMsg  string
	createdAt time.Time
	doneAt    time.Time

	// Attrs are the job template attributes submitted with the job.
	Attrs goipp.Attributes
	// Doc holds the document data once received (Create-Job +
	// Send-Document flow).
	Doc       []byte
	DocSet    bool
	DocFormat string

	cancel  atomic.Bool
	started atomic.Bool
}

// setState records a state transition.
func (j *StoredJob) setState(s JobState, msg string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.state == s {
		j.stateMsg = msg
		return
	}
	j.state = s
	j.stateMsg = msg
	if s == StateCompleted || s == StateAborted || s == StateCanceled {
		j.doneAt = time.Now()
	}
}

// snapshot returns a consistent view of the job's dynamic fields.
func (j *StoredJob) snapshot() (state JobState, msg string, created, done time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state, j.stateMsg, j.createdAt, j.doneAt
}

// Cancel marks the job for cancellation. It reports true if the job was
// not yet finished.
func (j *StoredJob) Cancel() bool {
	state, _, _, _ := j.snapshot()
	if state == StateCompleted || state == StateAborted || state == StateCanceled {
		return false
	}
	j.cancel.Store(true)
	return true
}

// Canceled reports whether cancellation was requested.
func (j *StoredJob) Canceled() bool { return j.cancel.Load() }

// JobStore is a small in-memory job registry backing Get-Jobs,
// Get-Job-Attributes and Cancel-Job.
type JobStore struct {
	mu   sync.Mutex
	next int32
	jobs map[int32]*StoredJob
}

// NewJobStore returns an empty job store.
func NewJobStore() *JobStore {
	return &JobStore{jobs: map[int32]*StoredJob{}}
}

// Add registers a new pending job and returns it.
func (s *JobStore) Add(name, user, uri string, attrs goipp.Attributes) *StoredJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	j := &StoredJob{
		ID:        s.next,
		Name:      name,
		User:      user,
		URI:       uri,
		state:     StatePending,
		createdAt: time.Now(),
		Attrs:     attrs,
	}
	s.jobs[j.ID] = j
	return j
}

// Get returns the job with the given id.
func (s *JobStore) Get(id int32) (*StoredJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	return j, ok
}

// List returns all jobs, newest first.
func (s *JobStore) List() []*StoredJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*StoredJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, j)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Count returns the number of stored jobs.
func (s *JobStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.jobs)
}
