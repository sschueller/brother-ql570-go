package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/sschueller/brother-ql570-go/pkg/ql"
)

// fakeBackend implements ql.Backend without a printer: it replays a canned
// status response once and then behaves like an empty device.
type fakeBackend struct {
	status []byte
	writes [][]byte
}

func (f *fakeBackend) Read(p []byte) (int, error) {
	if len(f.status) == 0 {
		return 0, os.ErrDeadlineExceeded
	}
	n := copy(p, f.status)
	f.status = f.status[n:]
	return n, nil
}

func (f *fakeBackend) Write(p []byte) (int, error) {
	f.writes = append(f.writes, append([]byte(nil), p...))
	return len(p), nil
}

func (f *fakeBackend) Close() error { return nil }

// cannedStatus returns a valid 32-byte status response: QL-570, 29 mm
// continuous tape loaded, no errors, waiting to receive.
func cannedStatus() []byte {
	s := make([]byte, 32)
	s[0], s[1], s[2] = 0x80, 0x20, 0x42
	s[3], s[4], s[5] = 0x34, 0x32, 0x30 // model code 34 32 = QL-570
	s[10] = 29                          // media width mm
	s[11] = 0x0A                        // continuous tape
	return s
}

func newTestMux(token string) http.Handler {
	pp := newPrinterPool("")
	pp.p = ql.NewPrinter(&fakeBackend{status: cannedStatus()})
	return withCORS(newServeMux(pp, token))
}

func newTestPool(b *fakeBackend) *printerPool {
	pp := newPrinterPool("")
	if b != nil {
		pp.p = ql.NewPrinter(b)
	}
	return pp
}

func doJSON(t *testing.T, h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	rd := io.Reader(http.NoBody)
	if s, ok := body.(string); ok {
		rd = strings.NewReader(s)
	} else if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = strings.NewReader(string(b))
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestServeStaticUI(t *testing.T) {
	h := newTestMux("")
	for _, path := range []string{"/", "/ui/", "/ui/anything"} {
		rec := doJSON(t, h, "GET", path, "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s: content type %q", path, ct)
		}
		if !strings.Contains(rec.Body.String(), "<title>QL-570 Label Designer</title>") {
			t.Errorf("GET %s: embedded UI not served", path)
		}
	}
}

func TestHealthz(t *testing.T) {
	rec := doJSON(t, newTestMux(""), "GET", "/healthz", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz: status %d", rec.Code)
	}
}

func TestStatusEndpoint(t *testing.T) {
	rec := doJSON(t, newTestMux(""), "GET", "/v1/status", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: HTTP %d: %s", rec.Code, rec.Body.String())
	}
	var st ql.Status
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Model != "QL-570" {
		t.Errorf("model: %q, want QL-570", st.Model)
	}
	if st.MediaWidthMM != 29 {
		t.Errorf("media width: %d, want 29", st.MediaWidthMM)
	}
}

func TestPreviewEndpoint(t *testing.T) {
	rec := doJSON(t, newTestMux(""), "POST", "/v1/preview", "", map[string]any{
		"text":      []string{"HELLO"},
		"media":     "29",
		"length_mm": 40,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: HTTP %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content type %q, want image/png", ct)
	}
	img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("decoding preview PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 306 || b.Dy() != ql.MMToDots(40) {
		t.Errorf("preview size %dx%d, want 306x%d", b.Dx(), b.Dy(), ql.MMToDots(40))
	}
}

func TestPreviewBlankLabel(t *testing.T) {
	rec := doJSON(t, newTestMux(""), "POST", "/v1/preview", "", map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("blank preview: HTTP %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := png.Decode(bytes.NewReader(rec.Body.Bytes())); err != nil {
		t.Fatalf("blank preview not a PNG: %v", err)
	}
}

func TestPreviewInvalidJob(t *testing.T) {
	rec := doJSON(t, newTestMux(""), "POST", "/v1/preview", "", "{not json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid job: HTTP %d, want 400", rec.Code)
	}

	rec = doJSON(t, newTestMux(""), "POST", "/v1/preview", "", map[string]any{
		"text":  []string{"HELLO"},
		"media": "nope",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown media: HTTP %d, want 400", rec.Code)
	}
}

func TestUploadAndPreviewImage(t *testing.T) {
	h := newTestMux("")

	// Build a multipart upload with a small PNG.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "test.png")
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(fw, image.NewGray(image.Rect(0, 0, 20, 10))); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: HTTP %d: %s", rec.Code, rec.Body.String())
	}
	var up struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &up); err != nil {
		t.Fatal(err)
	}
	if up.Path == "" || !strings.HasPrefix(up.Path, os.TempDir()) {
		t.Fatalf("unexpected upload path %q", up.Path)
	}
	if _, err := os.Stat(up.Path); err != nil {
		t.Fatalf("uploaded file missing: %v", err)
	}
	defer os.Remove(up.Path)

	// The uploaded path must be usable as the job Image.
	rec = doJSON(t, h, "POST", "/v1/preview", "", map[string]any{
		"image":     up.Path,
		"media":     "29",
		"length_mm": 40,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("preview with image: HTTP %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := png.Decode(bytes.NewReader(rec.Body.Bytes())); err != nil {
		t.Fatalf("preview with image not a PNG: %v", err)
	}
}

func TestUploadRejectsNonImage(t *testing.T) {
	h := newTestMux("")
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "junk.png")
	_, _ = fw.Write([]byte("this is not an image"))
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/v1/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("garbage upload: HTTP %d, want 400", rec.Code)
	}
}

func TestAuthRequired(t *testing.T) {
	h := newTestMux("secret")
	rec := doJSON(t, h, "POST", "/v1/preview", "", map[string]any{"text": []string{"x"}})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: HTTP %d, want 401", rec.Code)
	}
	rec = doJSON(t, h, "POST", "/v1/preview", "secret", map[string]any{"text": []string{"x"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token: HTTP %d: %s", rec.Code, rec.Body.String())
	}
	// The UI itself stays public so the browser can load it.
	rec = doJSON(t, h, "GET", "/", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("static UI with token: HTTP %d, want 200", rec.Code)
	}
}

func TestCORSPreflight(t *testing.T) {
	h := newTestMux("")
	req := httptest.NewRequest("OPTIONS", "/v1/preview", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight: HTTP %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("missing Access-Control-Allow-Origin header")
	}
}

// TestPrinterDisconnected verifies the daemon keeps serving the UI and the
// printer-independent endpoints while no printer is connected, and that
// printer-dependent endpoints report 503.
func TestPrinterDisconnected(t *testing.T) {
	h := withCORS(newServeMux(newTestPool(nil), ""))

	rec := doJSON(t, h, "GET", "/", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("UI without printer: HTTP %d, want 200", rec.Code)
	}
	rec = doJSON(t, h, "GET", "/healthz", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz without printer: HTTP %d, want 200", rec.Code)
	}
	rec = doJSON(t, h, "POST", "/v1/preview", "", map[string]any{"text": []string{"x"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("preview without printer: HTTP %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, "GET", "/v1/status", "", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status without printer: HTTP %d, want 503", rec.Code)
	}
	rec = doJSON(t, h, "GET", "/v1/info", "", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("info without printer: HTTP %d, want 503", rec.Code)
	}
	rec = doJSON(t, h, "POST", "/v1/print", "", map[string]any{"text": []string{"x"}})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("print without printer: HTTP %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "printer not connected") {
		t.Errorf("print error body: %s", rec.Body.String())
	}
}

// TestStatusFailureInvalidatesConnection verifies that a broken device
// (status read times out) closes the pool connection so the retry loop can
// reopen it.
func TestStatusFailureInvalidatesConnection(t *testing.T) {
	pp := newTestPool(&fakeBackend{}) // empty status: reads time out
	h := withCORS(newServeMux(pp, ""))
	rec := doJSON(t, h, "GET", "/v1/status", "", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: HTTP %d, want 503", rec.Code)
	}
	if pp.get() != nil {
		t.Error("failed status read should invalidate the printer connection")
	}
}
