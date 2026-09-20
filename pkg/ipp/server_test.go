package ipp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/OpenPrinting/goipp"
	"github.com/sschueller/brother-ql570-go/pkg/ql"
)

// fakePrinter implements Printer without a physical device. It records the
// jobs it is asked to print and reports a configurable status.
type fakePrinter struct {
	mu      sync.Mutex
	printed [][]ql.Job
	status  *ql.Status
	err     error
	// imagesExist records whether every submitted job's Image file was
	// present on disk when the print was requested.
	imagesExist bool
}

func (f *fakePrinter) PrintJobs(ctx context.Context, jobs []ql.Job) (*ql.PrintResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]ql.Job, len(jobs))
	copy(cp, jobs)
	f.printed = append(f.printed, cp)
	allExist := true
	for _, j := range jobs {
		if _, err := os.Stat(j.Image); err != nil {
			allExist = false
		}
	}
	f.imagesExist = allExist
	return &ql.PrintResult{Printed: true, Ready: true}, nil
}

func (f *fakePrinter) Status(ctx context.Context) (*ql.Status, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.status == nil {
		return &ql.Status{MediaType: ql.MediaTypeContinuous, MediaWidthMM: 29}, nil
	}
	return f.status, nil
}

func (f *fakePrinter) batchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.printed)
}

func (f *fakePrinter) lastBatch() []ql.Job {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.printed) == 0 {
		return nil
	}
	return f.printed[len(f.printed)-1]
}

func newTestServer(t *testing.T, fp Printer) (*Server, *httptest.Server) {
	t.Helper()
	s := NewServer(fp, "Brother QL-570 @ test", "urn:uuid:12345678-1234-1234-1234-123456789abc")
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

// postIPP sends an IPP request (message + optional document) and decodes
// the response.
func postIPP(t *testing.T, ts *httptest.Server, msg *goipp.Message, doc []byte) *goipp.Message {
	t.Helper()
	body, err := msg.EncodeBytes()
	if err != nil {
		t.Fatalf("encoding request: %v", err)
	}
	body = append(body, doc...)
	resp, err := http.Post(ts.URL+"/ipp/print", goipp.ContentType, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /ipp/print: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP status %d, want 200", resp.StatusCode)
	}
	out := &goipp.Message{}
	if err := out.Decode(resp.Body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	return out
}

// newRequest builds an IPP request with the common operation attributes.
func newRequest(op goipp.Op) *goipp.Message {
	req := goipp.NewRequest(goipp.DefaultVersion, op, 42)
	req.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset, goipp.String("utf-8")))
	req.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage, goipp.String("en-us")))
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI, goipp.String("ipp://test:9101/ipp/print")))
	return req
}

// mediaColAttr builds a media-col attribute for the given size in 1/100 mm.
func mediaColAttr(x, y int) goipp.Attribute {
	size := make(goipp.Collection, 0, 2)
	size.Add(goipp.MakeAttribute("x-dimension", goipp.TagInteger, goipp.Integer(x)))
	size.Add(goipp.MakeAttribute("y-dimension", goipp.TagInteger, goipp.Integer(y)))
	col := make(goipp.Collection, 0, 1)
	col.Add(goipp.MakeAttribute("media-size", goipp.TagBeginCollection, size))
	return goipp.MakeAttribute("media-col", goipp.TagBeginCollection, col)
}

// pwgDoc returns a small valid 1-page PWG Raster document (2x1 pixels,
// 8-bit black).
func pwgDoc() []byte {
	return pwgFixture(2, 1, 8, ColorSpaceBlack, 1, OrientationPortrait, 1, literalLine(2, []byte{1, 2}))
}

// minimalPDF returns a small one-page PDF (mirrors pkg/pdf's test
// fixture).
func minimalPDF() []byte {
	stream := "0 0 0 rg\n20 20 160 60 re f"
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] /Resources << >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n", len(objs)+1)
	b.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

// waitFor polls cond until it is true or the timeout expires.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	waitForTimeout(t, what, 5*time.Second, cond)
}

// waitForTimeout is waitFor with a custom timeout. The PDF path uses a
// longer one: the WebAssembly PDF engine is slow under the race detector.
func waitForTimeout(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func statusOf(resp *goipp.Message) goipp.Status { return goipp.Status(resp.Code) }

func TestGetPrinterAttributes(t *testing.T) {
	fp := &fakePrinter{}
	_, ts := newTestServer(t, fp)

	resp := postIPP(t, ts, newRequest(goipp.OpGetPrinterAttributes), nil)
	if statusOf(resp) != goipp.StatusOk {
		t.Fatalf("status = %v, want successful-ok", statusOf(resp))
	}
	byName := map[string]*goipp.Attribute{}
	for i := range resp.Printer {
		byName[resp.Printer[i].Name] = &resp.Printer[i]
	}
	for _, name := range []string{
		"ipp-versions-supported", "operations-supported", "document-format-supported",
		"color-supported", "printer-resolution-supported", "copies-supported",
		"sides-supported", "printer-uuid", "charset-configured", "uri-security-supported",
		"media-col-database", "media-default", "media-ready", "printer-state",
		"printer-state-reasons",
	} {
		if byName[name] == nil {
			t.Errorf("printer attribute %q missing", name)
		}
	}
	if a := byName["printer-uuid"]; a != nil {
		if v, ok := attrValueString(a); !ok || v != "urn:uuid:12345678-1234-1234-1234-123456789abc" {
			t.Errorf("printer-uuid = %q", v)
		}
	}
	if a := byName["printer-state"]; a != nil {
		if v, ok := attrValueInt(a); !ok || v != 3 {
			t.Errorf("printer-state = %v, want 3 (idle)", v)
		}
	}
	if a := byName["document-format-supported"]; a != nil {
		if len(a.Values) != 2 {
			t.Errorf("document-format-supported has %d values, want 2", len(a.Values))
		}
	}
	if a := byName["media-col-database"]; a != nil {
		want := len(BuildMediaCatalog()) + len(StandardMediaOptions())
		if len(a.Values) != want {
			t.Errorf("media-col-database has %d entries, want %d", len(a.Values), want)
		}
	}
	if a := byName["copies-supported"]; a != nil {
		if r, ok := a.Values[0].V.(goipp.Range); !ok || r.Lower != 1 || r.Upper != 255 {
			t.Errorf("copies-supported = %v", a.Values)
		}
	}
}

func TestPrintJobPWG(t *testing.T) {
	fp := &fakePrinter{}
	srv, ts := newTestServer(t, fp)

	req := newRequest(goipp.OpPrintJob)
	req.Operation.Add(goipp.MakeAttribute("document-format", goipp.TagMimeType, goipp.String(FormatPWGRaster)))
	req.Operation.Add(goipp.MakeAttribute("job-name", goipp.TagName, goipp.String("label-42")))
	req.Job.Add(mediaColAttr(2900, 6200)) // 29 mm x 62 mm continuous
	req.Job.Add(goipp.MakeAttribute("copies", goipp.TagInteger, goipp.Integer(2)))

	resp := postIPP(t, ts, req, pwgDoc())
	if statusOf(resp) != goipp.StatusOk {
		t.Fatalf("status = %v, want successful-ok (message: %v)", statusOf(resp), statusMessage(resp))
	}
	if id, ok := attrValueInt(findAttr(resp.Job, "job-id")); !ok || id < 1 {
		t.Fatalf("job-id = %v", findAttr(resp.Job, "job-id"))
	}

	waitFor(t, "print to be submitted", func() bool { return fp.batchCount() == 1 })
	waitFor(t, "job to complete", func() bool {
		j, _ := srv.store.Get(1)
		state, _, _, _ := j.snapshot()
		return state == StateCompleted
	})
	batch := fp.lastBatch()
	if len(batch) != 1 {
		t.Fatalf("printed %d jobs, want 1", len(batch))
	}
	j := batch[0]
	if j.Media != "29" {
		t.Errorf("Media = %q, want 29", j.Media)
	}
	if j.LengthMM != 62 {
		t.Errorf("LengthMM = %g, want 62", j.LengthMM)
	}
	if j.Copies != 2 {
		t.Errorf("Copies = %d, want 2", j.Copies)
	}
	if j.ImageFit != ql.ImageFitLabel {
		t.Errorf("ImageFit = %q, want %q", j.ImageFit, ql.ImageFitLabel)
	}
	if !fp.imagesExist {
		t.Error("Image file did not exist on disk when the print was requested")
	}
	// The temp file must be removed once the print finished.
	if _, err := os.Stat(j.Image); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temp image %s still exists after print, err=%v", j.Image, err)
	}
}

func TestPrintJobPDF(t *testing.T) {
	fp := &fakePrinter{}
	srv, ts := newTestServer(t, fp)

	req := newRequest(goipp.OpPrintJob)
	req.Operation.Add(goipp.MakeAttribute("document-format", goipp.TagMimeType, goipp.String(FormatPDF)))
	req.Job.Add(mediaColAttr(6200, 10000)) // 62x100 die-cut

	resp := postIPP(t, ts, req, minimalPDF())
	if statusOf(resp) != goipp.StatusOk {
		t.Fatalf("status = %v, want successful-ok (message: %v)", statusOf(resp), statusMessage(resp))
	}
	waitForTimeout(t, "job to finish", 60*time.Second, func() bool {
		j, ok := srv.store.Get(1)
		if !ok {
			return false
		}
		state, _, _, _ := j.snapshot()
		return state == StateCompleted || state == StateAborted
	})
	batch := fp.lastBatch()
	if len(batch) != 1 {
		t.Fatalf("printed %d jobs, want 1", len(batch))
	}
	j := batch[0]
	if j.Media != "62x100" {
		t.Errorf("Media = %q, want 62x100", j.Media)
	}
	if j.LengthMM != 0 {
		t.Errorf("LengthMM = %g, want 0 (die-cut fixes the length)", j.LengthMM)
	}
}

func TestPrintJobDuplexRejected(t *testing.T) {
	fp := &fakePrinter{}
	_, ts := newTestServer(t, fp)

	req := newRequest(goipp.OpPrintJob)
	req.Operation.Add(goipp.MakeAttribute("document-format", goipp.TagMimeType, goipp.String(FormatPWGRaster)))
	req.Job.Add(goipp.MakeAttribute("sides", goipp.TagKeyword, goipp.String("two-sided-long-edge")))

	resp := postIPP(t, ts, req, pwgDoc())
	if statusOf(resp) != goipp.StatusErrorAttributesOrValues {
		t.Fatalf("status = %v, want client-error-attributes-or-values-not-supported", statusOf(resp))
	}
	if findAttr(resp.Unsupported, "sides") == nil {
		t.Error("sides missing from unsupported attributes group")
	}
	if fp.batchCount() != 0 {
		t.Error("printer received a job despite rejection")
	}
}

func TestPrintJobUnknownMedia(t *testing.T) {
	fp := &fakePrinter{}
	_, ts := newTestServer(t, fp)

	req := newRequest(goipp.OpPrintJob)
	req.Operation.Add(goipp.MakeAttribute("document-format", goipp.TagMimeType, goipp.String(FormatPWGRaster)))
	req.Job.Add(mediaColAttr(21000, 29700)) // A4: not a label size

	resp := postIPP(t, ts, req, pwgDoc())
	if statusOf(resp) != goipp.StatusErrorAttributesOrValues {
		t.Fatalf("status = %v, want client-error-attributes-or-values-not-supported", statusOf(resp))
	}
	if findAttr(resp.Unsupported, "media-col") == nil {
		t.Error("media-col missing from unsupported attributes group")
	}
}

func TestPrintJobOffline(t *testing.T) {
	fp := &fakePrinter{err: errors.New("printer not connected")}
	_, ts := newTestServer(t, fp)

	req := newRequest(goipp.OpPrintJob)
	req.Operation.Add(goipp.MakeAttribute("document-format", goipp.TagMimeType, goipp.String(FormatPWGRaster)))

	resp := postIPP(t, ts, req, pwgDoc())
	if statusOf(resp) != goipp.StatusErrorServiceUnavailable {
		t.Fatalf("status = %v, want server-error-service-unavailable", statusOf(resp))
	}

	// Get-Printer-Attributes must report the printer as stopped with
	// printer-unreachable.
	attrResp := postIPP(t, ts, newRequest(goipp.OpGetPrinterAttributes), nil)
	if a := findAttr(attrResp.Printer, "printer-state-reasons"); a != nil {
		if v, ok := attrValueString(a); !ok || v != "printer-unreachable" {
			t.Errorf("printer-state-reasons = %q, want printer-unreachable", v)
		}
	}
}

func TestCreateJobSendDocument(t *testing.T) {
	fp := &fakePrinter{}
	srv, ts := newTestServer(t, fp)

	create := newRequest(goipp.OpCreateJob)
	create.Operation.Add(goipp.MakeAttribute("job-name", goipp.TagName, goipp.String("two-phase")))
	create.Job.Add(mediaColAttr(2900, 2500)) // 29x25 continuous
	createResp := postIPP(t, ts, create, nil)
	if statusOf(createResp) != goipp.StatusOk {
		t.Fatalf("Create-Job status = %v", statusOf(createResp))
	}
	idAttr := findAttr(createResp.Job, "job-id")
	if idAttr == nil {
		t.Fatal("Create-Job response has no job-id")
	}
	id, _ := attrValueInt(idAttr)

	send := newRequest(goipp.OpSendDocument)
	send.Operation.Add(goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(id)))
	send.Document.Add(goipp.MakeAttribute("document-format", goipp.TagMimeType, goipp.String(FormatPWGRaster)))
	send.Document.Add(goipp.MakeAttribute("last-document", goipp.TagBoolean, goipp.Boolean(true)))
	sendResp := postIPP(t, ts, send, pwgDoc())
	if statusOf(sendResp) != goipp.StatusOk {
		t.Fatalf("Send-Document status = %v", statusOf(sendResp))
	}

	waitFor(t, "job to complete", func() bool {
		j, _ := srv.store.Get(int32(id))
		state, _, _, _ := j.snapshot()
		return state == StateCompleted
	})
	batch := fp.lastBatch()
	if len(batch) != 1 || batch[0].LengthMM != 25 {
		t.Fatalf("printed batch = %+v, want one 29x25 job", batch)
	}
}

func TestCancelPendingJob(t *testing.T) {
	srv, ts := newTestServer(t, &fakePrinter{})

	create := newRequest(goipp.OpCreateJob)
	createResp := postIPP(t, ts, create, nil)
	id, _ := attrValueInt(findAttr(createResp.Job, "job-id"))

	cancel := newRequest(goipp.OpCancelJob)
	cancel.Operation.Add(goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(id)))
	cancelResp := postIPP(t, ts, cancel, nil)
	if statusOf(cancelResp) != goipp.StatusOk {
		t.Fatalf("Cancel-Job status = %v", statusOf(cancelResp))
	}

	job, ok := srv.store.Get(int32(id))
	if !ok {
		t.Fatal("job not in store")
	}
	if state, _, _, _ := job.snapshot(); state != StateCanceled {
		t.Fatalf("job state = %s, want canceled", state)
	}

	// A canceled job rejects further Send-Document calls.
	send := newRequest(goipp.OpSendDocument)
	send.Operation.Add(goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(id)))
	sendResp := postIPP(t, ts, send, pwgDoc())
	if statusOf(sendResp) != goipp.StatusErrorNotPossible {
		t.Fatalf("Send-Document after cancel status = %v, want client-error-not-possible", statusOf(sendResp))
	}
}

func TestValidateJob(t *testing.T) {
	fp := &fakePrinter{}
	_, ts := newTestServer(t, fp)

	ok := newRequest(goipp.OpValidateJob)
	ok.Job.Add(mediaColAttr(2900, 4000))
	resp := postIPP(t, ts, ok, nil)
	if statusOf(resp) != goipp.StatusOk {
		t.Fatalf("Validate-Job status = %v, want successful-ok", statusOf(resp))
	}
	if fp.batchCount() != 0 {
		t.Error("Validate-Job printed something")
	}

	bad := newRequest(goipp.OpValidateJob)
	bad.Job.Add(mediaColAttr(99999, 99999))
	resp = postIPP(t, ts, bad, nil)
	if statusOf(resp) != goipp.StatusErrorAttributesOrValues {
		t.Fatalf("Validate-Job (bad media) status = %v", statusOf(resp))
	}
}

func TestGetJobsAndGetJobAttributes(t *testing.T) {
	fp := &fakePrinter{}
	srv, ts := newTestServer(t, fp)

	req := newRequest(goipp.OpPrintJob)
	req.Operation.Add(goipp.MakeAttribute("document-format", goipp.TagMimeType, goipp.String(FormatPWGRaster)))
	req.Operation.Add(goipp.MakeAttribute("job-name", goipp.TagName, goipp.String("listed")))
	postIPP(t, ts, req, pwgDoc())

	waitFor(t, "job to complete", func() bool {
		j, _ := srv.store.Get(1)
		state, _, _, _ := j.snapshot()
		return state == StateCompleted
	})

	jobs := postIPP(t, ts, newRequest(goipp.OpGetJobs), nil)
	if statusOf(jobs) != goipp.StatusOk {
		t.Fatalf("Get-Jobs status = %v", statusOf(jobs))
	}
	jobGroups := 0
	for _, g := range jobs.Groups {
		if g.Tag == goipp.TagJobGroup {
			jobGroups++
		}
	}
	if jobGroups != 1 {
		t.Fatalf("Get-Jobs returned %d job groups, want 1", jobGroups)
	}
	if a := findAttr(jobs.Job, "job-state"); a != nil {
		if v, ok := attrValueInt(a); !ok || v != jobStateCompleted {
			t.Errorf("job-state = %v, want 9 (completed)", v)
		}
	}

	attr := newRequest(goipp.OpGetJobAttributes)
	attr.Operation.Add(goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(1)))
	attrResp := postIPP(t, ts, attr, nil)
	if statusOf(attrResp) != goipp.StatusOk {
		t.Fatalf("Get-Job-Attributes status = %v", statusOf(attrResp))
	}
	if a := findAttr(attrResp.Job, "job-name"); a != nil {
		if v, ok := attrValueString(a); !ok || v != "listed" {
			t.Errorf("job-name = %q, want listed", v)
		}
	}

	missing := newRequest(goipp.OpGetJobAttributes)
	missing.Operation.Add(goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(999)))
	if resp := postIPP(t, ts, missing, nil); statusOf(resp) != goipp.StatusErrorNotFound {
		t.Errorf("Get-Job-Attributes (unknown id) status = %v, want client-error-not-found", statusOf(resp))
	}
}

func TestPrintJobStandardMedia(t *testing.T) {
	// A standard size (54x86 address card) must be resolved against the
	// loaded media: the fake printer reports 62 mm continuous tape, so
	// the job lands on 62 mm auto-fit (the label grows with the content).
	fp := &fakePrinter{status: &ql.Status{MediaType: ql.MediaTypeContinuous, MediaWidthMM: 62}}
	srv, ts := newTestServer(t, fp)

	req := newRequest(goipp.OpPrintJob)
	req.Operation.Add(goipp.MakeAttribute("document-format", goipp.TagMimeType, goipp.String(FormatPWGRaster)))
	req.Job.Add(mediaColAttr(5400, 8600)) // om_card_54x86mm

	resp := postIPP(t, ts, req, pwgDoc())
	if statusOf(resp) != goipp.StatusOk {
		t.Fatalf("status = %v, want successful-ok (message: %v)", statusOf(resp), statusMessage(resp))
	}
	waitFor(t, "job to complete", func() bool {
		j, ok := srv.store.Get(1)
		if !ok {
			return false
		}
		state, _, _, _ := j.snapshot()
		return state == StateCompleted || state == StateAborted
	})
	batch := fp.lastBatch()
	if len(batch) != 1 {
		t.Fatalf("printed %d jobs, want 1", len(batch))
	}
	j := batch[0]
	if j.Media != "62" || j.LengthMM != 0 {
		t.Errorf("standard size resolved to Media=%q LengthMM=%g, want 62/auto-fit", j.Media, j.LengthMM)
	}
}

func TestPrintJobHires(t *testing.T) {
	// With Hires enabled, IPP jobs print in the QL-570's 600 dpi mode
	// along the label length.
	fp := &fakePrinter{}
	srv, ts := newTestServer(t, fp)
	srv.Hires = true

	req := newRequest(goipp.OpPrintJob)
	req.Operation.Add(goipp.MakeAttribute("document-format", goipp.TagMimeType, goipp.String(FormatPWGRaster)))

	resp := postIPP(t, ts, req, pwgDoc())
	if statusOf(resp) != goipp.StatusOk {
		t.Fatalf("status = %v, want successful-ok", statusOf(resp))
	}
	waitFor(t, "job to complete", func() bool {
		j, ok := srv.store.Get(1)
		if !ok {
			return false
		}
		state, _, _, _ := j.snapshot()
		return state == StateCompleted || state == StateAborted
	})
	batch := fp.lastBatch()
	if len(batch) != 1 {
		t.Fatalf("printed %d jobs, want 1", len(batch))
	}
	if !batch[0].Hires {
		t.Error("Hires not set on IPP job")
	}
}

func TestValidateJobStandardMedia(t *testing.T) {
	// Validate-Job with a standard size must pass validation.
	fp := &fakePrinter{}
	_, ts := newTestServer(t, fp)

	req := newRequest(goipp.OpValidateJob)
	req.Job.Add(mediaColAttr(5400, 8600))
	resp := postIPP(t, ts, req, nil)
	if statusOf(resp) != goipp.StatusOk {
		t.Fatalf("Validate-Job status = %v, want successful-ok", statusOf(resp))
	}
}

func TestIdentifyPrinter(t *testing.T) {
	_, ts := newTestServer(t, &fakePrinter{})
	resp := postIPP(t, ts, newRequest(goipp.OpIdentifyPrinter), nil)
	if statusOf(resp) != goipp.StatusOk {
		t.Fatalf("Identify-Printer status = %v", statusOf(resp))
	}
}

// statusMessage extracts the status-message operation attribute.
func statusMessage(resp *goipp.Message) string {
	if a := findAttr(resp.Operation, "status-message"); a != nil {
		if v, ok := attrValueString(a); ok {
			return v
		}
	}
	return ""
}
