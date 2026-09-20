# IPP Print Server for `ql570 serve` (driverless Android printing)

## Goal

Let Android phones print to the Brother QL-570 over Wi-Fi with **zero apps and
zero drivers**, using the phone's built-in Default Print Service. The phone
discovers the printer via mDNS/Bonjour and submits jobs via **IPP** (Internet
Printing Protocol, RFC 8010/8011). The daemon converts incoming documents to
QL-570 raster jobs and prints them over USB.

## Decisions (confirmed with user)

| Decision | Choice |
|----------|--------|
| Placement | Extend the existing `ql570 serve` daemon (it already owns the printer pool, web UI, PDF engine) |
| Document formats | `image/pwg-raster` (decoded in pure Go) **and** `application/pdf` (reuse `pkg/pdf`) |
| Paper sizes | Full catalog: all die-cut sizes + continuous widths (12/29/38/50/54/62 mm) x lengths 25/40/62/90 mm, mapped to `ql.Media` + `LengthMM` |
| Enablement/auth | On by default in `serve`, unauthenticated (LAN trust model, like any consumer printer); `--ipp=false` disables. `--token` still protects `/v1/*` and the web UI |
| Out of scope | iOS/AirPrint (URF raster), Windows raw 9100, CUPS integration, authenticated IPP |

## Architecture

- New package **`pkg/ipp`** (pure Go, no cgo): IPP message handling, PWG Raster
  decoding, media catalog, job store.
- New dependencies (pure Go, consistent with project constraints):
  - `github.com/OpenPrinting/goipp` — IPP message codec (maintained by
    OpenPrinting, used by ipp-usb/cups-browsed work). Hand-rolled IPP binary
    encoding is not worth it.
  - `github.com/grandcat/zeroconf` — mDNS/DNS-SD responder.
- Wired into `cmd/ql570/daemon.go`: IPP served at `/ipp/print` on the existing
  listener (mDNS SRV/TXT carries the port, so 631/root is not needed).

## Data flow

```
Android print dialog
  -> mDNS browse (_ipp._tcp / _printer._tcp / _universal._sub._ipp._tcp)
  -> Get-Printer-Attributes (media catalog, formats, state)
  -> Create-Job + Send-Document (PWG Raster or PDF)   [or Print-Job]
  -> pkg/ipp decodes to grayscale image(s)
  -> ql.Job{Media, LengthMM, Image: tmp PNG, Copies, ImageFit: "label"}
  -> printerPool.PrintJobs (existing USB path, status pre-check included)
  -> Get-Job-Attributes / Get-Jobs report completion
```

PWG Raster pages and PDF pages each become one label. For continuous tape,
`LengthMM` comes from the chosen IPP paper size height; for die-cut, the
`ql.Media` entry fixes the length (existing `checkMediaMatch` rejects a size
that does not match the loaded roll before anything prints).

## Implementation steps (ordered)

1. **`pkg/ipp/pwgraster.go`** — PWG Raster decoder (PWG 5100.13) for the
   subset Android emits:
   - Parse 1796-byte page header (big-endian uint32 fields): `HwResolution`
     (require 300x300), `NumCopies`, `Orientation`, `MediaPosition`, page
     count.
   - Per page: `PixelsPerLine`, `BytesPerLine`, `BitDepth=8`, `NumChannels`
     1 (monochrome K) or 3 (sRGB), band iteration.
   - Convert to `*image.Gray`; rotate 90° when `Orientation==4`.
   - Unit test with a small hand-built PWG Raster fixture.
2. **`pkg/ipp/media.go`** — IPP media catalog:
   - Build `media-col-database` entries: die-cut sizes 1:1 from
     `ql.mediaTable` (via exported helpers), continuous widths x lengths
     {25, 40, 62, 90} mm.
   - IPP media names `oem_ql570-<W>x<L>mm`; x/y dimensions in microns.
   - `LookupIPPMedia(xUM, yUM) (ql.Media, LengthMM, error)`.
   - `media-default`: `oem_ql570-29x62mm` (matches the default DK-22210 roll).
   - Unit test mapping both directions.
3. **`pkg/ipp/job.go`** — job mapping + store:
   - `IPPJobToQL(jobAttrs, docBytes, format)` -> `[]ql.Job`:
     - PWG Raster: decode all pages -> temp PNG per page (`os.TempDir()`,
       cleaned after print), `ImageFit: "label"` (aspect already matches the
       paper size).
     - PDF: `pdf.PageCount` + `pdf.RenderPNGFiles` at `pdf.MaxPixels` (existing
       behavior), same mapping.
     - `copies` attr -> `ql.Job.Copies`.
   - In-memory job store (id -> state: pending/processing/completed/aborted,
     timestamps) for `Get-Jobs` / `Get-Job-Attributes` / `Cancel-Job`
     (cancel = flag checked before printing).
4. **`pkg/ipp/server.go`** — HTTP handler for `/ipp/print`:
   - Operations (via goipp codec): `Get-Printer-Attributes`, `Get-Jobs`,
     `Get-Job-Attributes`, `Validate-Job`, `Create-Job`, `Send-Document`,
     `Print-Job`, `Cancel-Job`, `Identify-Printer`.
   - Printer attributes: `ipp-versions-supported` (1.1, 2.0),
     `operations-supported`, `document-format-supported`
     (image/pwg-raster, application/pdf), `color-supported=false`,
     `printer-resolution-supported` 300x300dpi, `copies-supported`,
     `sides-supported=one-sided` (reject duplex with
     `client-error-attributes-or-values-not-supported`), `printer-uuid`,
     `charset-configured=utf-8`, `uri-security-supported=none`,
     `media-col-database`/`media-default` from step 2, plus dynamic:
     `printer-state`/`printer-state-reasons` and `media-ready` from the pool
     (`stopped` + `printer-unreachable` when no printer; jobs rejected with
     `server-error-service-unavailable`).
   - Print jobs are serialized with a mutex (the USB path must not interleave
     two jobs).
   - Body size cap via `http.MaxBytesReader` (64 MiB; a 62x100 mm RGB page is
     ~2.3 MB).
   - Auth: none (by design); served on the same mux as the token-protected
     `/v1/*`.
5. **`cmd/ql570/daemon.go` wiring**:
   - Flags: `--ipp` (default true), `--ipp-name` (default
     `"Brother QL-570 @ <hostname>"`).
   - Register handler at `/ipp/print`; zeroconf `Register` for `_ipp._tcp`,
     `_printer._tcp` and the `_universal._sub._ipp._tcp` subtype; TXT records:
     `txtvers=1`, `qtotal=1`, `rp=ipp/print`, `ty=<ipp-name>`,
     `product=(Brother QL-570)`, `pdl=image/pwg-raster,application/pdf`.
   - To keep the handler testable, extract a small interface from
     `printerPool` (e.g. `type ippPrinter interface { PrintJobs(...) ; Status(...) }`)
     so tests can use a fake.
   - Update `usage()` text in `cmd/ql570/main.go`.
6. **Tests** — `pkg/ipp/*_test.go`:
   - PWG Raster fixture decode (both channel layouts, landscape rotation).
   - Media catalog lookups (valid, unknown size, die-cut vs continuous).
   - Handler tests over `httptest` with goipp-encoded requests and a fake
     printer: attributes round-trip, Create-Job+Send-Document -> correct
     `ql.Job`, PDF path, copies, printer-offline -> 503-class response.
   - `make vet` + `make test`.
7. **Docs** (AGENTS.md requires keeping README in sync):
   - README: new "Android (driverless IPP)" section — enable Default Print
     Service on the phone (Settings > Connected devices > Printing), same
     subnet requirement (mDNS is link-local), paper size picker notes,
     `--ipp=false` escape hatch.
   - Docker caveat: mDNS multicast does not cross the default bridge; document
     `network_mode: host` for the container (or note discovery will not work).
   - Windows/macOS note: IPP Everywhere clients there also work via
     `ipp://<host>:9101/ipp/print` manually (no mDNS dependency needed).

## Risks / failure modes

- **Android not showing the printer**: usually missing `_universal._sub._ipp._tcp`
  subtype or malformed attributes; validate with `avahi-browse -rt _ipp._tcp`
  and a real phone early in step 5.
- **OEM print-service quirks** (Samsung/Xiaomi ships non-Mopria services):
  mitigated by supporting PDF as fallback; not further addressed.
- **mDNS in Docker**: needs host networking — documented, compose file
  optionally updated.
- **Printer offline mid-job**: pool invalidation already handles reopen; IPP
  reports `server-error-service-unavailable`; retry from the phone dialog.
- **goipp API complexity**: it is a low-level codec; attribute assembly lives
  in one file (`attributes.go`) so any corrections are localized.

## Validation

1. `make check` (vet + tests, gofmt-clean).
2. Unit tests as above.
3. Manual smoke test:
   - Run `ql570 serve` on the printer host.
   - Verify mDNS: `avahi-browse -rt _ipp._tcp` shows the instance + TXT.
   - If `ipptool` (cups-client) is available: `ipptool -tv
     ipp://host:9101/ipp/print get-printer-attributes.test`; otherwise curl
     the web UI to confirm the daemon is up.
   - On the phone: Settings > Connected devices > Printing > Default Print
     Service enabled -> open e.g. a photo/note -> Print -> select "Brother
     QL-570" -> pick paper size (e.g. 29x40mm) -> print; verify label length
     and content, then repeat with a PDF.
4. Release notes entry via conventional commit (`feat(ipp): ...`); version
   bumped automatically by release workflow.

## Open questions

None blocking. Minor: exact `--ipp-name` default and whether the compose file
should switch to `network_mode: host` (decide during implementation; both are
one-line changes).
