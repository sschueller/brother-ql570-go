# brother-ql570-go

Pure-Go library + CLI for the **Brother QL-570** label printer, speaking the
native QL raster command protocol directly over USB (`/dev/usb/lp*` on
Linux, usbdevfs via `termux-usb` on Android). No CUPS, no printer drivers,
no cgo — the label is rendered host-side and the raw command stream is sent
straight to the device.

Also ships an HTTP print daemon with an **embedded web UI** (label designer
with live preview, image upload, printer status and print), so any client —
including the cross-compiled macOS ARM binary — can print labels over the
network. A Docker image and compose file are included for Raspberry Pi
(ARM64) deployment.

- [Quick start](#quick-start)
- [CLI](#cli)
- [HTTP daemon](#http-daemon)
- [Docker](#docker)
- [Library usage](#library-usage)
- [Media table](#media-table)
- [How it works / protocol notes](#how-it-works--protocol-notes)
- [Permissions](#permissions)
- [macOS](#macos)
- [Android (Termux)](#android-termux)
- [Build](#build)

## Quick start

```sh
make build                       # builds ./ql570
./ql570 status                   # printer status (auto-discovers the QL-570)
./ql570 info                     # model + loaded media
./ql570 print --text "SW-01 uplink"
```

On continuous tape (the default DK-22210, 29 mm) the label length is
**auto-fitted** to the printed content (minimum 12.7 mm, the printer's
smallest feed). `--length` overrides it with a fixed length in
millimeters; `--cable` multiplies the fitted length by 2.5 (cable wrap
labels, see below).

## CLI

```
ql570 print [flags]
ql570 status [--device PATH]
ql570 info   [--device PATH]
ql570 serve  [flags]
ql570 version
```

### print

| Flag | Default | Description |
|------|---------|-------------|
| `--text "line"` | - | text line, repeatable, printed top to bottom |
| `--qr "content"` | - | render a QR code (up to 30 mm, auto version) |
| `--barcode "content"` | - | render a Code128 barcode |
| `--image file.png` | - | print a PNG/JPEG/GIF (scaled to full width) |
| `--font file.ttf` | embedded Go Regular | TTF font file |
| `--font-size` | 10 | font size in points |
| `--length` | auto-fit | label length in mm (continuous tape only; default fits the content, 12.7..1000 mm) |
| `--cable` | false | cable wrap label: fitted length x 2.5 (wrap + overlap) |
| `--cable-factor` | 2.5 | cable wrap multiplier (1-10) |
| `--media` | `29mm` | media size, e.g. `62mm`, `38mm`, `62x100` (see [Media table](#media-table)) |
| `--copies` | 1 | number of pages (1-255) |
| `--cut` | true | automatic cutter (`--cut=false` to disable) |
| `--cut-every` | 1 | cut after every n-th label |
| `--mirror` | false | mirror the label horizontally |
| `--rotate` | 0 | rotate content 90/180/270 degrees |
| `--margin-top` / `--margin-bottom` | 0 | content margins in mm |
| `--align` | left | `left` \| `center` \| `right` |
| `--compress` | none | `tiff` is rejected: the QL-570 has no compression support |
| `--dither` | false | Floyd-Steinberg dithering (photographs) |
| `--threshold` | 50 | grayscale threshold in percent |
| `--hires` | false | 600 dpi in the length direction (ESC i K bit 6) |
| `--feed-dots` | 35 / 0 | override the feed/margin amount in dots |
| `--quality` | true | priority to print quality (PI_QUALITY) |
| `--job file.json` | - | load the complete job from JSON (flags override file values); accepts a single object or an **array** of objects (one label per entry) |
| `--device /dev/usb/lp0` | auto | printer device path |
| `--dry-run out.bin` | - | write the raw command stream to a file instead of printing |

Examples:

```sh
# Cable label: two lines + QR, length auto-fits the content
ql570 print --text "SW-01 uplink" --text "eth1/1 -> eth1/2" \
            --qr "https://nms.example.com/devices/sw-01"

# Cable wrap label: auto-fitted length x 2.5 so the label wraps the cable
ql570 print --text "eth1/1 -> eth1/2" --cable

# Fixed 60 mm label
ql570 print --text "SW-01 uplink" --length 60

# 2 copies, cut after the second label
ql570 print --text "VLAN 10" --copies 2 --cut-every 2

# Full-width barcode, no auto cut
ql570 print --barcode "ABC-123456" --cut=false --length 30

# Pre-rendered PNG (e.g. a patch panel layout), centered
ql570 print --image panel.png --align center --length 100

# JSON job file (same schema as the HTTP API)
ql570 print --job examples/job.json

# Multiple labels in one print job: --job also accepts a JSON array,
# one label per entry (all entries must use the same media)
ql570 print --job examples/jobs.json
```

Every print job starts with a **status pre-check**: if the media loaded in
the printer does not match the job (type, width, length) the job is rejected
before any data is sent, and the printer is never left in an error state.

### status / info

Decode the 32-byte status response: model, media type/width/length, status
type, phase, notification and error bits. `info` additionally notes that the
QL-570 raster protocol does not expose the firmware version or serial number
(no such command exists in the official command reference).

## HTTP daemon

The daemon is the integration point for remote clients and macOS: only the
daemon touches USB, clients stay platform-independent. It serves an
embedded single-page **web UI** (label designer with live preview, image
upload, printer status and print) at `/`, plus the JSON API below.

```sh
ql570 serve --listen 0.0.0.0:9101 [--token SECRET] [--device /dev/usb/lp0]
```

Then open `http://<host>:9101` in a browser.

| Flag | Default | Description |
|------|---------|-------------|
| `--listen` | `0.0.0.0:9101` | listen address (the web UI is served at `/`) |
| `--token` | `$QL570_TOKEN` | require `Authorization: Bearer <token>` on `/v1/*` |
| `--device` | auto | printer device path (auto-discovers `/dev/usb/lp*`) |

The daemon starts even when the printer is switched off or unplugged: the
UI, `/v1/preview` and `/v1/upload` work without it, `/v1/status`,
`/v1/info` and `/v1/print` answer `503 "printer not connected"`, and the
connection is retried every 10 seconds. A failed status read invalidates
the connection so an unplugged device is reopened cleanly. The `--token`
default comes from `$QL570_TOKEN`.

| Endpoint | Method | Body | Description |
|----------|--------|------|-------------|
| `/` | GET | - | embedded web UI (label designer) |
| `/v1/print` | POST | `ql.Job` JSON (object or **array**) | print synchronously, returns statuses |
| `/v1/preview` | POST | `ql.Job` JSON (single object) | PNG preview of the rendered label (no printer needed) |
| `/v1/upload` | POST | multipart `file` (PNG/JPEG/GIF) | stores the image, returns `{"path": "..."}` for the job's `image` field |
| `/v1/status` | GET | - | current printer status |
| `/v1/info` | GET | - | model + media info |
| `/healthz` | GET | - | liveness probe (no auth) |

With `--token` set, `/v1/*` requires `Authorization: Bearer <token>` (the
web UI asks for the token in the header field).

```sh
curl -s -X POST http://localhost:9101/v1/print \
     -H 'Authorization: Bearer SECRET' \
     -H 'Content-Type: application/json' \
     -d '{"text":["SW-01 uplink"],"length_mm":40}'
```

The job schema is exactly `ql.Job` (see [job.go](pkg/ql/job.go)), so a Go
application can share struct definitions with the library. The body may be
a single job object or an array of jobs — one label per entry, printed as
pages of a single job. All entries must use the same media.

### Series labels

The web UI expands Bash-style brace ranges in text lines, QR content and
barcode content, so one design can print a numbered series:

- `{1..10}` → `1`…`10`; `{01..10}` → `01`…`10` (zero-padded to the widest
  number); `{1..10..2}` → `1,3,5,7,9`; `{10..1}` counts down.
- `SW-{01..10}` in a text line prints ten labels (`SW-01` … `SW-10`),
  posted to `/v1/print` as a job array (one label per entry).
- Multiple ranges must share one count (e.g. `SW-{1..3} port {1..3}`);
  fields without a range stay constant, and every label keeps the UI Copies
  value (copies 2 = two of each). Maximum 1000 labels per series.
- The preview shows the first label, with "label 1 of N" in the meta line.

## Docker

The daemon runs in a container, e.g. on a Raspberry Pi (ARM64). The image
is a multi-stage build (`golang:1.25-bookworm` → `debian:12-slim`) with the
web UI embedded in the binary:

```sh
make docker-build                    # docker build -t ql570 .
docker build --platform linux/arm64 -t ql570 .   # on a non-ARM host
```

Host requirements:

- The `usblp` kernel module must be loaded (persist it in
  `/etc/modules-load.d/usblp.conf`):
  ```sh
  sudo modprobe usblp
  ```
- The container needs the USB bus mapped. Either run with
  `--device=/dev/bus/usb`, or use the compose file which also maps the
  port and an optional token:

  ```sh
  QL570_TOKEN=secret docker compose up -d
  # or manually:
  docker run --rm -d --name ql570 \
    --device=/dev/bus/usb \
    -p 9101:9101 \
    -e QL570_TOKEN=optional-secret \
    ql570
  ```

Open `http://<host-ip>:9101` in a browser. Like the native daemon, the
container starts without the printer connected and reconnects every 10
seconds; the daemon auto-discovers `/dev/usb/lp*` inside the container
when the USB bus is mapped.

## Library usage

```go
import "github.com/sschueller/brother-ql570-go/pkg/ql"

p, err := ql.Open("") // "" = auto-discover the QL-570
if err != nil {
    log.Fatal(err)
}
defer p.Close()

res, err := p.Print(context.Background(), &ql.Job{
    Text:     []string{"SW-01 uplink"},
    QR:       "https://example.com/sw-01",
    LengthMM: 60,
})
if err != nil {
    log.Fatal(err)
}
fmt.Printf("printed=%v ready=%v\n", res.Printed, res.Ready)
```

Useful library entry points:

- `ql.Open(device)` / `ql.NewPrinter(backend)` — printer handle
- `(*ql.Printer).Print(ctx, *Job)` — print one label (thin wrapper over PrintJobs)
- `(*ql.Printer).PrintJobs(ctx, []Job)` — print a batch of labels as one job
- `(*ql.Printer).Status(ctx)` / `.Info(ctx)` — decoded 32-byte status
- `ql.BuildJobBytes(*Job)` / `ql.BuildJobsBytes([]Job)` — full raw command
  stream without a printer (dry-run)
- `ql.ParseJobs([]byte)` — parse a job file (single object or array)
- `ql.RenderJob(*Job, Media)` — raster lines for a job
- `ql.RenderJobImage(*Job, Media)` — grayscale image of the rendered label
  before thresholding (used by the web UI preview)
- `ql.BuildJobStream(model, PrintOptions, rows)` — protocol-level command
  assembly (single page design, N copies)
- `ql.BuildJobsStream(model, []JobPage)` — protocol-level command assembly
  for distinct pages
- `ql.ParseStatus([]byte)` — status decoding (unit-testable, no device)
- `ql.LookupMedia("62x100")` / `ql.MediaIDs()` — media table
- `ql.DiscoverUSB()` — list `/dev/usb/lp*` with vendor/product IDs
- `ql.Backend` — interface to plug in another transport (network print
  server, raw USB, ...)

## Media table

Dot counts at 300 dpi from the official Command Reference (section 3.2.2 /
3.2.5). The QL-570 print head is 720 pins (90 bytes) wide; the right-margin
column shows where the print area sits inside the raster row.

| ID | Size | Type | Printable (dots) | Right margin |
|----|------|------|------------------|--------------|
| `12` | 12 mm | continuous | 106 | 29 |
| `29` | 29 mm | continuous (default, DK-22210) | 306 | 6 |
| `38` | 38 mm | continuous | 413 | 12 |
| `50` | 50 mm | continuous | 554 | 12 |
| `54` | 54 mm | continuous | 590 | 0 |
| `62` | 62 mm | continuous | 696 | 12 |
| `17x54` | 17x54 mm | die-cut | 165x566 | 0 |
| `17x87` | 17x87 mm | die-cut | 165x956 | 0 |
| `23x23` | 23x23 mm | die-cut | 236x202 | 42 |
| `29x90` | 29x90 mm | die-cut | 306x991 | 6 |
| `38x90` | 38x90 mm | die-cut | 413x991 | 12 |
| `39x48` | 39x48 mm | die-cut | 425x495 | 6 |
| `52x29` | 52x29 mm | die-cut | 578x271 | 0 |
| `62x29` | 62x29 mm | die-cut | 696x271 | 12 |
| `62x100` | 62x100 mm | die-cut | 696x1109 | 12 |
| `d12` | 12 mm dia | round die-cut | 94x94 | 113 |
| `d24` | 24 mm dia | round die-cut | 236x236 | 42 |
| `d58` | 58 mm dia | round die-cut | 618x618 | 51 |

Continuous tape length: 12.7..1000 mm (150..11811 dots).

## How it works / protocol notes

Everything is cross-checked against the official *"Brother
QL-500/550/560/570/580N/650TD/700/1050/1060N Command Reference"* and the
battle-tested [brother_ql](https://github.com/pklaus/brother_ql) project,
then validated against a physical QL-570.

Per print job (section 3 of the reference):

```
200x 00                      clear the command buffer
1B 40                        initialize
1B 69 53                     status request
1B 69 7A + 10 bytes          print information (media type/width/length, raster number)
1B 69 4D 40                  set each mode: auto cut
1B 69 41 01                  cut every 1 label
1B 69 4B 08                  expanded mode: cut at end (bit 3), 600dpi (bit 6)
1B 69 64 23 00               margin/feed amount: 35 dots (continuous), 0 (die-cut)
67 00 5A <90 bytes> x N      raster lines (MSB first; right margin pins first)
1A                           print with feeding (0C between pages)
```

- The printer replies with a **32-byte status** (`80 20 42 ...`) which the
  CLI decodes: error bits, media type/width/length, status/phase, notification.
- **QL-570 has no TIFF/PackBits compression** (only QL-580N/650TD/1050/1060N
  do) — `--compress tiff` is rejected during validation.
- **No built-in fonts, no two-color, no auto media detection** — all
  rendering is host-side in Go (embedded Go Regular font or any TTF),
  converted to 1-bit at 300 dpi (threshold or Floyd-Steinberg dithering).
  Print density/contrast is controlled via `--threshold`/`--dither` because
  the reference defines no print-density command.
- The printer has an **automatic cutter** (no half-cut); auto cut, cut-every
  and cut-at-end are all exposed.
- After printing, the daemon/CLI wait for the printer to report "printing
  completed" + "waiting to receive" phase change, so jobs are confirmed.

## Permissions

`/dev/usb/lp0` is owned by `root:lp`. Add your user to the `lp` group:

```sh
sudo usermod -aG lp $USER   # re-login afterwards
```

For multi-user setups that can't use the `lp` group, an optional udev rule
opens the device to a dedicated group:

```
# /etc/udev/rules.d/99-brother-ql570.rules
SUBSYSTEM=="usbmisc", ATTRS{idVendor}=="04f9", ATTRS{idProduct}=="2028", GROUP="printers", MODE="0664"
```

## macOS

Direct USB printing from macOS is out of scope (libusb there needs cgo and a
native build). The intended path for the ARM Mac CLI is the daemon:

1. On the Linux host with the printer: `ql570 serve --listen 0.0.0.0:9101 --token SECRET`
2. On the Mac: `curl -H "Authorization: Bearer SECRET" -d '{"text":["..."],"length_mm":40}' http://linux-host:9101/v1/print`

The cross-compiled `dist/ql570-darwin-arm64` binary is a full-featured HTTP
client entry point for the library; its direct-USB commands (`print`,
`status`, `info`) return a clear error on non-Linux platforms.

## Android (Termux)

The CLI can print straight from an Android phone over USB-OTG, without
root. The Android backend speaks usbdevfs directly on a raw file descriptor
that `termux-usb` (termux-api) obtains through the Android `UsbManager` —
the same fd-handoff mechanism libusb uses on Android. Endpoints are
discovered from the device's USB descriptors at runtime, nothing is
hardcoded.

1. Install [Termux](https://termux.dev/) and the
   [Termux:API](https://f-droid.org/packages/com.termux.api/) app
   (both from F-Droid), then `pkg install termux-api`.
2. Transfer the `dist/ql570-android-arm64` binary to the phone and make it
   executable:
   ```sh
   chmod +x ~/ql570
   ```
3. Find the printer device:
   ```sh
   termux-usb -l          # e.g. /dev/bus/usb/001/002
   ```
4. Run the CLI through `termux-usb -E`, which hands the device's file
   descriptor over in `$TERMUX_USB_FD` (a permission dialog appears on
   first use):
   ```sh
   termux-usb -r -E -e "$HOME/ql570 status" /dev/bus/usb/001/002
   termux-usb -r -E -e "$HOME/ql570 info" /dev/bus/usb/001/002
   termux-usb -r -E -e "$HOME/ql570 print --text 'SW-01 uplink'" /dev/bus/usb/001/002
   ```

Notes:

- `--device` is not needed: when `$TERMUX_USB_FD` is set the CLI uses it
  automatically. If the fd is passed as a plain argument instead (`-e`
  without `-E`), use `--device fd:N`. `--dry-run` and the HTTP daemon
  client mode work unchanged.
- If `termux-usb -l` hangs without output (known termux-api issue on some
  Android 13+/14 ROMs), run `termux-api-start` first — it keeps the
  Termux:API app's service alive so requests get processed. Also make sure
  the Termux:API app is v0.51.0+ (F-Droid) and exempt from battery
  optimization.
- If `termux-api-start` fails with `Error: Not found; no service started`,
  the Termux:API **Android app** is not installed: `pkg install termux-api`
  only installs the CLI helpers. Install the app from F-Droid
  (`com.termux.api`), open it once, and verify with
  `pm list packages | grep termux.api`.
- Known caveat: `termux-usb` reliability varies on some Android 13+/14
  ROMs. If the permission handoff fails there, the daemon path
  (`ql570 serve` on a computer attached to the printer + the phone as HTTP
  client) remains the fallback, as does rooted direct access
  (`--device /dev/bus/usb/BBB/DDD`).

## Build

```sh
make build               # native build
make test vet            # unit tests + vet
make build-darwin-arm64  # cross-compile for Apple Silicon (no cgo)
make build-android-arm64 # cross-compile for Android/Termux (no cgo)
make docker-build        # daemon Docker image (web UI embedded)
```

All dependencies are pure Go: `golang.org/x/image` (fonts/raster),
`github.com/skip2/go-qrcode`, `github.com/boombuler/barcode`.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the branch workflow, conventional
commit format, testing requirements, and the automated release process.

