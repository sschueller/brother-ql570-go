package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	// Image decoders for upload validation. image.DecodeConfig sniffs the
	// format; PNG is also imported directly for the preview encoder.
	_ "image/gif"
	_ "image/jpeg"

	"github.com/sschueller/brother-ql570-go/pkg/ql"
	"github.com/sschueller/brother-ql570-go/web"
)

// cmdServe runs the HTTP print daemon. Only the daemon touches the USB
// device; clients (including the macOS ARM build of this CLI, or the
// future Go application) submit JSON jobs over HTTP.
//
//	GET  /              embedded single-page web UI (label designer)
//	POST /v1/print      body = ql.Job JSON; prints synchronously
//	POST /v1/preview    body = ql.Job JSON; PNG preview of the label
//	POST /v1/upload     multipart image upload; returns {"path": "..."}
//	GET  /v1/status     current printer status
//	GET  /v1/info       model/media info (same as status)
//	GET  /healthz       liveness probe
func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", "0.0.0.0:9101", "listen address")
	token := fs.String("token", os.Getenv("QL570_TOKEN"), "require Authorization: Bearer <token> on /v1/* (default: $QL570_TOKEN)")
	device := fs.String("device", "", "printer device path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// The printer is opened lazily: the daemon starts (and keeps serving
	// the UI, preview and upload) even when the printer is switched off
	// or unplugged, and reconnects in the background once it appears.
	pool := newPrinterPool(*device)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pool.retryLoop(ctx)
	defer pool.Close()

	srv := &http.Server{Addr: *listen, Handler: withCORS(newServeMux(pool, *token))}
	if pool.get() == nil {
		log.Printf("printer not connected; the UI and API stay available and the connection is retried every %s", reconnectInterval)
	}
	log.Printf("ql570 %s daemon listening on %s", Version, *listen)
	fmt.Fprintf(os.Stderr, "ql570 %s daemon listening on %s\n", Version, *listen)
	return srv.ListenAndServe()
}

// reconnectInterval is how often the daemon retries the printer connection
// while it is unavailable.
const reconnectInterval = 10 * time.Second

// printerPool lazily manages the USB printer connection. The daemon and
// the web UI work without a printer; /v1/status, /v1/info and /v1/print
// report 503 until one is connected. A failed status read invalidates the
// connection so an unplugged or stalled device is reopened cleanly.
type printerPool struct {
	mu      sync.Mutex
	device  string
	p       *ql.Printer
	lastErr string
}

func newPrinterPool(device string) *printerPool { return &printerPool{device: device} }

// tryOpen attempts to connect the printer. It logs only state changes, so
// the retry loop stays quiet while nothing is attached.
func (pp *printerPool) tryOpen() {
	pp.mu.Lock()
	defer pp.mu.Unlock()
	if pp.p != nil {
		return
	}
	p, err := ql.Open(pp.device)
	if err != nil {
		if err.Error() != pp.lastErr {
			log.Printf("printer not connected: %v", err)
			pp.lastErr = err.Error()
		}
		return
	}
	pp.p = p
	pp.lastErr = ""
	log.Printf("printer connected: %s", printerPath(p))
}

// get returns the connected printer, or nil while it is unavailable.
func (pp *printerPool) get() *ql.Printer {
	pp.mu.Lock()
	defer pp.mu.Unlock()
	return pp.p
}

// invalidate closes the current connection so the retry loop reopens it.
func (pp *printerPool) invalidate() {
	pp.mu.Lock()
	defer pp.mu.Unlock()
	if pp.p == nil {
		return
	}
	_ = pp.p.Close()
	pp.p = nil
	log.Printf("printer connection closed (device unplugged or unresponsive); reconnecting every %s", reconnectInterval)
}

// Close releases the printer connection.
func (pp *printerPool) Close() { pp.invalidate() }

// retryLoop connects once at startup and then keeps retrying until ctx is
// done.
func (pp *printerPool) retryLoop(ctx context.Context) {
	pp.tryOpen()
	t := time.NewTicker(reconnectInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pp.tryOpen()
		}
	}
}

// newServeMux builds the HTTP routes for the daemon: the embedded web UI
// at /, the API under /v1/* and the liveness probe at /healthz.
func newServeMux(pool *printerPool, token string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	auth := newAuth(token)

	mux.Handle("GET /v1/status", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := pool.get()
		if p == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "printer not connected"})
			return
		}
		st, err := p.Status(r.Context())
		if err != nil {
			pool.invalidate()
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, st)
	})))

	mux.Handle("GET /v1/info", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := pool.get()
		if p == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "printer not connected"})
			return
		}
		st, err := p.Info(r.Context())
		if err != nil {
			pool.invalidate()
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, st)
	})))

	mux.Handle("POST /v1/print", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := pool.get()
		if p == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "printer not connected"})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "reading request body: " + err.Error()})
			return
		}
		// The body may be a single job object or an array of jobs
		// (one label per entry).
		jobs, err := ql.ParseJobs(body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid job JSON: " + err.Error()})
			return
		}
		res, err := p.PrintJobs(r.Context(), jobs)
		if err != nil {
			// Transport-level failures (unplugged device, no response)
			// make the connection unusable; the retry loop reopens it.
			if strings.Contains(err.Error(), "printer") {
				pool.invalidate()
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, res)
	})))

	// POST /v1/preview renders a job into a PNG image of the final label
	// (grayscale, before thresholding) without touching the printer.
	mux.Handle("POST /v1/preview", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "reading request body: " + err.Error()})
			return
		}
		var job ql.Job
		if err := json.Unmarshal(body, &job); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid job JSON: " + err.Error()})
			return
		}
		job.DefaultJobValues()
		// An empty job (fresh designer) previews as a blank label instead
		// of failing validation for missing content.
		var media ql.Media
		if !job.HasText() && job.QR == "" && job.Barcode == "" && job.Image == "" {
			job.Text = []string{""}
			media, err = ql.LookupMedia(job.Media)
		} else {
			media, err = job.Validate()
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		img, err := ql.RenderJobImage(&job, media)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "image/png")
		if err := png.Encode(w, img); err != nil {
			log.Printf("preview: encoding PNG: %v", err)
		}
	})))

	// POST /v1/upload accepts a multipart image upload ("file" field),
	// stores it under /tmp/ql570-<random>.png and returns the path for
	// use as the Image field of a job.
	mux.Handle("POST /v1/upload", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const maxUpload = 20 << 20
		r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
		if err := r.ParseMultipartForm(maxUpload); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "parsing upload: " + err.Error()})
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing multipart field 'file': " + err.Error()})
			return
		}
		defer f.Close()
		cfg, _, err := image.DecodeConfig(f)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a supported image (PNG/JPEG/GIF): " + err.Error()})
			return
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reading upload: " + err.Error()})
			return
		}
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "generating file name: " + err.Error()})
			return
		}
		path := filepath.Join(os.TempDir(), "ql570-"+hex.EncodeToString(id[:])+".png")
		dst, err := os.Create(path)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "storing upload: " + err.Error()})
			return
		}
		defer dst.Close()
		if _, err := io.Copy(dst, f); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "storing upload: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":   path,
			"width":  cfg.Width,
			"height": cfg.Height,
		})
	})))

	// The embedded single-page UI is served at / (and any other GET not
	// handled by an API route, so client-side routing keeps working).
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(web.IndexHTML)
	})

	return mux
}

// printerPath reports the device path for logging purposes.
func printerPath(p *ql.Printer) string {
	if ub, ok := p.Backend().(*ql.USBBackend); ok {
		return ub.Path()
	}
	return "unknown"
}

// withCORS allows the API to be called from other origins on the local
// network (e.g. a page served from another host in dev setups).
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// newAuth returns a middleware enforcing a bearer token when one is set.
func newAuth(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token == "" || r.Header.Get("Authorization") == "Bearer "+token {
				next.ServeHTTP(w, r)
				return
			}
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
