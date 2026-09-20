package main

import (
	"bytes"
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
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	// Image decoders for upload validation. image.DecodeConfig sniffs the
	// format; PNG is also imported directly for the preview encoder.
	_ "image/gif"
	_ "image/jpeg"

	"github.com/google/uuid"
	"github.com/grandcat/zeroconf"
	"github.com/sschueller/brother-ql570-go/pkg/ipp"
	"github.com/sschueller/brother-ql570-go/pkg/pdf"
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
//	POST /v1/upload     multipart image or PDF upload; images return
//	                    {"path": "..."}, PDFs are rasterized page by page
//	                    and return {"type": "pdf", "pages": N, "paths": [...]}
//	GET  /v1/status     current printer status
//	GET  /v1/info       model/media info (same as status)
//	GET  /healthz       liveness probe
func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", "0.0.0.0:9101", "listen address")
	token := fs.String("token", os.Getenv("QL570_TOKEN"), "require Authorization: Bearer <token> on /v1/* (default: $QL570_TOKEN)")
	device := fs.String("device", "", "printer device path")
	ippEnabled := fs.Bool("ipp", true, "serve IPP at /ipp/print for driverless printing (Android Default Print Service)")
	ippName := fs.String("ipp-name", "", "printer name advertised over mDNS/IPP (default \"Brother QL-570 @ <hostname>\")")
	ippHires := fs.Bool("ipp-hires", true, "print IPP jobs at 600 dpi along the label length (high-resolution mode)")
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

	var ippHandler http.Handler
	var mdnsServers []*zeroconf.Server
	defer func() {
		for _, s := range mdnsServers {
			s.Shutdown()
		}
	}()
	if *ippEnabled {
		if *ippName == "" {
			host, err := os.Hostname()
			if err != nil || host == "" {
				host = "ql570"
			}
			*ippName = fmt.Sprintf("Brother QL-570 @ %s", host)
		}
		ippSrv := ipp.NewServer(poolAdapter{pool: pool}, *ippName, printerUUID())
		ippSrv.Logf = log.Printf
		ippSrv.Hires = *ippHires
		ipp.Logf = log.Printf
		ippHandler = ippSrv.Handler()
		mdnsServers = registerMDNS(*ippName, ippPort(*listen), ippListenHost(*listen), printerUUID())
	}

	srv := &http.Server{Addr: *listen, Handler: withCORS(newServeMux(pool, *token, ippHandler))}
	if pool.get() == nil {
		log.Printf("printer not connected; the UI and API stay available and the connection is retried every %s", reconnectInterval)
	}
	// Warm the PDF engine in the background: the first PDF upload would
	// otherwise pay the one-time WebAssembly compile cost, which takes
	// tens of seconds on small ARM boards (and could outlast a reverse
	// proxy's read timeout). IPP PDF jobs benefit from the same warm-up.
	go func() {
		if err := pdf.Init(); err != nil {
			log.Printf("PDF engine failed to initialize: %v (PDF uploads will fail)", err)
			return
		}
		log.Printf("PDF engine ready")
	}()
	log.Printf("ql570 %s daemon listening on %s", Version, *listen)
	fmt.Fprintf(os.Stderr, "ql570 %s daemon listening on %s\n", Version, *listen)
	return srv.ListenAndServe()
}

// poolAdapter adapts the daemon's lazy printer pool to the ipp.Printer
// interface. A missing printer fails fast (no USB I/O); transport-level
// failures invalidate the connection so the retry loop reopens it.
type poolAdapter struct {
	pool *printerPool
}

func (a poolAdapter) PrintJobs(ctx context.Context, jobs []ql.Job) (*ql.PrintResult, error) {
	p := a.pool.get()
	if p == nil {
		return nil, fmt.Errorf("printer not connected")
	}
	res, err := p.PrintJobs(ctx, jobs)
	if err != nil && strings.Contains(err.Error(), "printer") {
		a.pool.invalidate()
	}
	return res, err
}

func (a poolAdapter) Status(ctx context.Context) (*ql.Status, error) {
	p := a.pool.get()
	if p == nil {
		return nil, fmt.Errorf("printer not connected")
	}
	return p.Status(ctx)
}

// printerUUID returns a stable printer UUID for this host (a UUIDv5 derived
// from the hostname), so clients recognize the printer across restarts.
// The "/v2" seed distinguishes the current capabilities format: bumping it
// makes clients drop capabilities they cached from older daemon versions.
func printerUUID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "ql570"
	}
	return uuid.NewSHA1(uuid.NameSpaceDNS, []byte("brother-ql570-go/v2@"+host)).URN()
}

// ippPort extracts the TCP port from a listen address (e.g. "0.0.0.0:9101").
func ippPort(listen string) int {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return 9101
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 {
		return 9101
	}
	return n
}

// ippListenHost extracts the bind host from a listen address.
func ippListenHost(listen string) string {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return ""
	}
	return host
}

// isRealInterface reports whether an interface should carry mDNS traffic:
// up, multicast-capable, non-loopback and not virtual (Docker bridges,
// veth pairs, VPNs, ...). Announcing on virtual interfaces makes clients
// receive IP addresses they cannot reach (e.g. Docker bridge networks),
// which stalls discovery on the phone and, through the shared print
// service queue, affects the other printers as well.
func isRealInterface(iface net.Interface) bool {
	if iface.Flags&net.FlagUp == 0 ||
		iface.Flags&net.FlagMulticast == 0 ||
		iface.Flags&net.FlagLoopback != 0 {
		return false
	}
	name := strings.ToLower(iface.Name)
	for _, prefix := range []string{
		"docker", "br-", "veth", "virbr", "vmnet",
		"tun", "tap", "tailscale", "wg", "flannel", "cali", "cni", "kube",
	} {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return true
}

// realInterfaces returns the interfaces worth announcing mDNS on.
func realInterfaces() []net.Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	for _, iface := range ifaces {
		if isRealInterface(iface) {
			out = append(out, iface)
		}
	}
	return out
}

// defaultRouteInterface returns the interface used to reach the outside
// world (via a connectionless UDP dial, which routes without sending
// packets), or nil when it cannot be determined.
func defaultRouteInterface() *net.Interface {
	conn, err := net.Dial("udp", "8.8.8.8:53")
	if err != nil {
		return nil
	}
	defer conn.Close()
	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || local.IP == nil {
		return nil
	}
	for _, iface := range realInterfaces() {
		for _, a := range interfaceAddrs(&iface) {
			if a.IP.Equal(local.IP) {
				return &iface
			}
		}
	}
	return nil
}

// interfaceAddrs returns the IPv4/IPv6 addresses assigned to an interface.
func interfaceAddrs(iface *net.Interface) []*net.IPNet {
	addrs, err := iface.Addrs()
	if err != nil {
		return nil
	}
	var out []*net.IPNet
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsUnspecified() {
			out = append(out, ipn)
		}
	}
	return out
}

// mdnsTarget picks the single interface (and its addresses) the printer
// is announced on: the interface owning an explicit --listen IP, else the
// default-route interface, else all real interfaces. Announcements carry
// only that interface's addresses, so clients never receive unreachable
// Docker/VPN addresses.
func mdnsTarget(listenHost string) ([]net.Interface, []string) {
	ifaces := realInterfaces()
	if len(ifaces) == 0 {
		return nil, nil
	}
	ipStrings := func(iface *net.Interface) []string {
		var out []string
		for _, a := range interfaceAddrs(iface) {
			out = append(out, a.IP.String())
		}
		return out
	}
	if h := net.ParseIP(listenHost); h != nil && !h.IsUnspecified() {
		for _, iface := range ifaces {
			for _, a := range interfaceAddrs(&iface) {
				if a.IP.Equal(h) {
					return []net.Interface{iface}, ipStrings(&iface)
				}
			}
		}
	}
	if iface := defaultRouteInterface(); iface != nil {
		return []net.Interface{*iface}, ipStrings(iface)
	}
	var ips []string
	for _, iface := range ifaces {
		ips = append(ips, ipStrings(&iface)...)
	}
	return ifaces, ips
}

// registerMDNS advertises the IPP service via mDNS/DNS-SD so Android's
// Default Print Service discovers the printer without configuration. It
// registers the standard _ipp._tcp service type that every IPP Everywhere
// client browses; the announcement is limited to one real network
// interface (and only its addresses). Failures are logged and non-fatal
// (IPP stays reachable by direct URL).
//
// _printer._tcp and the Mopria _universal._sub._ipp._tcp subtype were
// deliberately dropped: _printer._tcp is a legacy type clients do not
// need for IPP, and advertising the subtype as a separate service type
// pollutes the _services._dns-sd._udp.local enumeration (a DNS-SD
// violation) and multiplies mDNS traffic on multi-homed hosts.
func registerMDNS(name string, port int, listenHost, printerUUIDStr string) []*zeroconf.Server {
	// The UUID key lets clients track the printer identity across
	// address changes and, crucially, invalidate capabilities they
	// cached from a previous daemon version (Android's built-in print
	// service caches printer capabilities indefinitely, keyed by UUID).
	rawUUID := strings.TrimPrefix(printerUUIDStr, "urn:uuid:")
	txt := []string{
		"txtvers=1",
		"qtotal=1",
		"rp=ipp/print",
		"ty=" + name,
		"product=(Brother QL-570)",
		"pdl=image/pwg-raster,application/pdf",
		"UUID=" + rawUUID,
	}
	ifaces, ips := mdnsTarget(listenHost)

	var (
		s   *zeroconf.Server
		err error
	)
	if len(ifaces) > 0 && len(ips) > 0 {
		host, herr := os.Hostname()
		if herr != nil || host == "" {
			host = "ql570"
		}
		s, err = zeroconf.RegisterProxy(name, "_ipp._tcp", "local.", port, host, ips, txt, ifaces)
	} else {
		s, err = zeroconf.Register(name, "_ipp._tcp", "local.", port, txt, nil)
	}
	if err != nil {
		log.Printf("mDNS: registering _ipp._tcp: %v", err)
		return nil
	}
	names := make([]string, 0, len(ifaces))
	for _, iface := range ifaces {
		names = append(names, iface.Name)
	}
	log.Printf("mDNS: advertising %q on port %d via %v (addresses %v)", name, port, names, ips)
	return []*zeroconf.Server{s}
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
// at /, the API under /v1/*, the IPP print service at /ipp/print (when
// ippHandler is non-nil) and the liveness probe at /healthz.
func newServeMux(pool *printerPool, token string, ippHandler http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	auth := newAuth(token)

	if ippHandler != nil {
		// Driverless printing: unauthenticated by design (LAN trust
		// model, like any consumer printer). --token only protects
		// /v1/* and the web UI.
		mux.Handle("POST /ipp/print", ippHandler)
	}

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

	// POST /v1/upload accepts a multipart upload ("file" field) with an
	// image (PNG/JPEG/GIF) or a PDF. Images are stored under
	// /tmp/ql570-<random>.png and the path is returned for use as the
	// Image field of a job. PDFs are rasterized page by page into PNG
	// files (up to pdf.MaxPages pages) and returned as a list of paths,
	// one per page.
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
		data, err := io.ReadAll(f)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reading upload: " + err.Error()})
			return
		}
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "generating file name: " + err.Error()})
			return
		}
		prefix := "ql570-" + hex.EncodeToString(id[:])

		if pdf.IsPDF(data) {
			pages, err := pdf.PageCount(data)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a valid PDF: " + err.Error()})
				return
			}
			if pages == 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "PDF has no pages"})
				return
			}
			if pages > pdf.MaxPages {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("PDF has %d pages (max %d); split the document or upload it in parts", pages, pdf.MaxPages)})
				return
			}
			idxs := make([]int, pages)
			for i := range idxs {
				idxs[i] = i
			}
			paths, err := pdf.RenderPNGFiles(data, idxs, pdf.MaxPixels, os.TempDir(), prefix)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"type":  "pdf",
				"pages": pages,
				"paths": paths,
			})
			return
		}

		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a supported image (PNG/JPEG/GIF): " + err.Error()})
			return
		}
		path := filepath.Join(os.TempDir(), prefix+".png")
		dst, err := os.Create(path)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "storing upload: " + err.Error()})
			return
		}
		defer dst.Close()
		if _, err := io.Copy(dst, bytes.NewReader(data)); err != nil {
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
