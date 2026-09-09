// Command ql570 prints labels on a Brother QL-570 label printer using the
// native raster command protocol (no CUPS required).
//
// Usage:
//
//	ql570 print --text "SW-01 uplink" --length 40
//	ql570 status
//	ql570 info
//	ql570 serve --listen 0.0.0.0:9101
//	ql570 version
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sschueller/brother-ql570-go/pkg/pdf"
	"github.com/sschueller/brother-ql570-go/pkg/ql"
)

// Version is the CLI/library release version.
const Version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "print":
		err = cmdPrint(os.Args[2:])
	case "status":
		err = cmdStatus(os.Args[2:])
	case "info":
		err = cmdInfo(os.Args[2:])
	case "serve":
		err = cmdServe(os.Args[2:])
	case "version":
		fmt.Println("ql570", Version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `ql570 - Brother QL-570 label printer CLI

Usage:
  ql570 print [flags]            print a label (auto-discovers the printer)
  ql570 status [--device PATH]   show the 32-byte printer status
  ql570 info [--device PATH]     show model + media info
  ql570 serve [flags]            run the HTTP print daemon
  ql570 version                  print the version

print flags:
  --text "line"            text line (repeatable, printed top to bottom)
  --qr "content"           render a QR code
  --barcode "content"      render a Code128 barcode
  --image file.png         print a PNG/JPEG/GIF image (scaled to width)
  --pdf file.pdf           print a PDF (rasterized at 300 dpi; one label
                           per page, or use --pdf-page to pick one page)
  --pdf-page N             print only page N (1-based) of --pdf
  --fit                    fit the image/PDF page onto the label (whole
                           picture inside the printable area, aspect kept)
  --font file.ttf          TTF font (default: embedded Go Regular)
  --font-size 10           font size in points
  --length 40              label length in mm (continuous media; default:
                           fit to content, min 12.7 mm)
  --cable                  cable wrap label: auto length x 2.5 (wraps the
                           cable with overlap)
  --cable-factor 2.5       cable wrap multiplier (1-10)
  --media 29mm             media: 29mm (default) 62mm 38mm 24mm... 62x100...
  --copies 1               number of copies (1-255)
  --cut=true               auto cut (default true)
  --cut-every 1            cut after every n labels
  --mirror                 mirror the label horizontally
  --rotate 0|90|180|270    rotate the content (rotated images are scaled
                           to fill the label width)
  --margin-top 0           top margin in mm
  --margin-bottom 0        bottom margin in mm
  --margin-left 0          left offset/margin in mm
  --margin-right 0         right margin in mm
  --align left|center|right
  --compress none|tiff     tiff is rejected on the QL-570 (unsupported)
  --dither                 Floyd-Steinberg dithering
  --threshold 50           grayscale threshold in percent (0-100)
  --hires                  600 dpi in the length direction
  --feed-dots 35           override the feed/margin amount in dots
  --quality=true           priority to print quality
  --job file.json          load a job definition (JSON object or array of
                           objects - one label per array entry)
  --device /dev/usb/lp0    printer device (default: auto-discover)
  --dry-run out.bin        write the raw command stream instead of printing

serve flags:
  --listen 0.0.0.0:9101   listen address (the web UI is served at /)
  --token SECRET          require "Authorization: Bearer SECRET" on /v1/*
                          (default: $QL570_TOKEN)
  --device /dev/usb/lp0   printer device (default: auto-discover)
`)
}

// stringList collects repeatable --text flags.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, "|") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func signalContext() context.Context {
	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return ctx
}

// tempPrefix returns a random file name prefix for temporary render
// outputs (e.g. PDF pages rasterized for printing).
func tempPrefix(kind string) string {
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return fmt.Sprintf("ql570-%s-%d", kind, time.Now().UnixNano())
	}
	return fmt.Sprintf("ql570-%s-%s", kind, hex.EncodeToString(id[:]))
}

// parseSetFlags parses args and returns the set of flag names that were
// provided on the command line.
func parseSetFlags(fs *flag.FlagSet, args []string) (map[string]bool, error) {
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set, nil
}

func cmdPrint(args []string) error {
	fs := flag.NewFlagSet("print", flag.ContinueOnError)
	var texts stringList
	var (
		qr, barcode, image, font, media, device, align, compress, jobFile, dryRun string
		pdfFile                                                                   string
		fontSize, length, marginTop, marginBottom, cableFactor                    float64
		marginLeft, marginRight                                                   float64
		copies, cutEvery, rotate, threshold, feedDots, pdfPage                    int
		cut, mirror, dither, hires, quality, cable, fit                           bool
	)
	fs.Var(&texts, "text", "text line (repeatable)")
	fs.StringVar(&qr, "qr", "", "QR code content")
	fs.StringVar(&barcode, "barcode", "", "Code128 barcode content")
	fs.StringVar(&image, "image", "", "image file (PNG/JPEG/GIF)")
	fs.StringVar(&pdfFile, "pdf", "", "PDF file to print (one label per page)")
	fs.IntVar(&pdfPage, "pdf-page", 0, "print only this PDF page (1-based)")
	fs.BoolVar(&fit, "fit", false, "fit the image/PDF page onto the label instead of full width")
	fs.StringVar(&font, "font", "", "TTF font file")
	fs.Float64Var(&fontSize, "font-size", 10, "font size in points")
	fs.Float64Var(&length, "length", 0, "label length in mm (continuous media, default fits content)")
	fs.BoolVar(&cable, "cable", false, "cable wrap label (auto length x factor)")
	fs.Float64Var(&cableFactor, "cable-factor", 2.5, "cable wrap multiplier")
	fs.StringVar(&media, "media", "", "media size (default 29mm)")
	fs.IntVar(&copies, "copies", 1, "copies")
	fs.BoolVar(&cut, "cut", true, "auto cut")
	fs.IntVar(&cutEvery, "cut-every", 1, "cut after every n labels")
	fs.BoolVar(&mirror, "mirror", false, "mirror the label")
	fs.IntVar(&rotate, "rotate", 0, "rotate content 0/90/180/270")
	fs.Float64Var(&marginTop, "margin-top", 0, "top margin in mm")
	fs.Float64Var(&marginBottom, "margin-bottom", 0, "bottom margin in mm")
	fs.Float64Var(&marginLeft, "margin-left", 0, "left offset/margin in mm")
	fs.Float64Var(&marginRight, "margin-right", 0, "right margin in mm")
	fs.StringVar(&align, "align", "", "left|center|right")
	fs.StringVar(&compress, "compress", "", "none|tiff")
	fs.BoolVar(&dither, "dither", false, "Floyd-Steinberg dithering")
	fs.IntVar(&threshold, "threshold", 0, "grayscale threshold percent (default 50)")
	fs.BoolVar(&hires, "hires", false, "600 dpi in length direction")
	fs.IntVar(&feedDots, "feed-dots", 0, "feed/margin amount in dots")
	fs.BoolVar(&quality, "quality", true, "priority to print quality")
	fs.StringVar(&jobFile, "job", "", "load job from JSON file")
	fs.StringVar(&device, "device", "", "printer device path")
	fs.StringVar(&dryRun, "dry-run", "", "write raw command stream to this file instead of printing")

	set, err := parseSetFlags(fs, args)
	if err != nil {
		return err
	}

	// Content flags that make no sense combined with a multi-label job
	// file (each label carries its own options in the JSON array).
	contentFlags := []string{
		"text", "qr", "barcode", "image", "pdf", "pdf-page", "fit", "font", "font-size", "length",
		"media", "copies", "cut", "cut-every", "mirror", "rotate",
		"margin-top", "margin-bottom", "margin-left", "margin-right", "align", "compress", "dither",
		"threshold", "hires", "feed-dots", "quality", "cable", "cable-factor",
	}

	var jobs []ql.Job
	if jobFile != "" {
		data, err := os.ReadFile(jobFile)
		if err != nil {
			return fmt.Errorf("reading job file: %w", err)
		}
		jobs, err = ql.ParseJobs(data)
		if err != nil {
			return err
		}
		if len(jobs) > 1 {
			for _, f := range contentFlags {
				if set[f] {
					return fmt.Errorf("--%s cannot be combined with a multi-label job file; set the option inside each JSON job instead", f)
				}
			}
			if set["device"] {
				for i := range jobs {
					jobs[i].Device = device
				}
			}
		}
	}

	if len(jobs) <= 1 {
		job := ql.Job{}
		if len(jobs) == 1 {
			job = jobs[0]
		}
		// Flags explicitly set on the command line override the job file.
		if set["text"] {
			job.Text = texts
		}
		if set["qr"] {
			job.QR = qr
		}
		if set["barcode"] {
			job.Barcode = barcode
		}
		if set["image"] {
			job.Image = image
		}
		if set["fit"] {
			if fit {
				job.ImageFit = ql.ImageFitLabel
			} else {
				job.ImageFit = ql.ImageFitWidth
			}
		}
		if set["font"] {
			job.Font = font
		}
		if set["font-size"] {
			job.FontSize = fontSize
		}
		if set["length"] {
			job.LengthMM = length
		}
		if set["cable"] {
			job.Cable = cable
		}
		if set["cable-factor"] {
			job.CableFactor = cableFactor
		}
		if set["media"] {
			job.Media = media
		}
		if set["copies"] {
			job.Copies = copies
		}
		if set["cut"] {
			job.Cut = &cut
		}
		if set["cut-every"] {
			job.CutEvery = cutEvery
		}
		if set["mirror"] {
			job.Mirror = mirror
		}
		if set["rotate"] {
			job.Rotate = rotate
		}
		if set["margin-top"] {
			job.MarginTopMM = marginTop
		}
		if set["margin-bottom"] {
			job.MarginBottomMM = marginBottom
		}
		if set["margin-left"] {
			job.MarginLeftMM = marginLeft
		}
		if set["margin-right"] {
			job.MarginRightMM = marginRight
		}
		if set["align"] {
			job.Align = align
		}
		if set["compress"] {
			job.Compress = compress
		}
		if set["dither"] {
			job.Dither = dither
		}
		if set["threshold"] {
			job.Threshold = threshold
		}
		if set["hires"] {
			job.Hires = hires
		}
		if set["feed-dots"] {
			job.FeedDots = feedDots
		}
		if set["quality"] {
			job.Quality = &quality
		}
		if set["device"] {
			job.Device = device
		}
		jobs = []ql.Job{job}

		// --pdf renders the PDF's pages to temporary PNG files and turns
		// the job into one label per page (or a single label with
		// --pdf-page). The files are removed after the print.
		if pdfFile != "" {
			if set["image"] {
				return fmt.Errorf("--pdf cannot be combined with --image")
			}
			if set["pdf-page"] && pdfPage < 1 {
				return fmt.Errorf("--pdf-page must be a positive page number, got %d", pdfPage)
			}
			data, err := os.ReadFile(pdfFile)
			if err != nil {
				return fmt.Errorf("reading PDF: %w", err)
			}
			pages, err := pdf.PageCount(data)
			if err != nil {
				return fmt.Errorf("parsing PDF: %w", err)
			}
			if pages == 0 {
				return fmt.Errorf("PDF has no pages")
			}
			idxs := make([]int, 0, pages)
			if pdfPage > 0 {
				if pdfPage > pages {
					return fmt.Errorf("--pdf-page %d out of range: the PDF has %d page(s)", pdfPage, pages)
				}
				idxs = append(idxs, pdfPage-1)
			} else {
				if pages > pdf.MaxPages {
					return fmt.Errorf("PDF has %d pages (max %d with --pdf); use --pdf-page to print one page or split the document", pages, pdf.MaxPages)
				}
				for i := 0; i < pages; i++ {
					idxs = append(idxs, i)
				}
			}
			paths, err := pdf.RenderPNGFiles(data, idxs, pdf.MaxPixels, os.TempDir(), tempPrefix("pdf"))
			if err != nil {
				return fmt.Errorf("rendering PDF: %w", err)
			}
			defer func() {
				for _, p := range paths {
					_ = os.Remove(p)
				}
			}()
			rendered := make([]ql.Job, 0, len(paths))
			for _, p := range paths {
				jj := job
				jj.Image = p
				rendered = append(rendered, jj)
			}
			jobs = rendered
		}
	}

	if dryRun != "" {
		stream, medias, err := ql.BuildJobsBytes(jobs)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dryRun, stream, 0o644); err != nil {
			return fmt.Errorf("writing dry-run output: %w", err)
		}
		pages := 0
		for _, j := range jobs {
			c := j.Copies
			if c == 0 {
				c = 1
			}
			pages += c
		}
		fmt.Fprintf(os.Stderr, "dry run: wrote %d bytes (%d labels, %d pages, media %s) to %s\n",
			len(stream), len(jobs), pages, medias[0].ID, dryRun)
		return nil
	}

	ctx := signalContext()
	p, err := ql.Open(jobs[0].Device)
	if err != nil {
		return err
	}
	defer p.Close()

	res, err := p.PrintJobs(ctx, jobs)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "printed: %v, ready for next job: %v\n", res.Printed, res.Ready)
	if res.Status != nil {
		fmt.Fprintln(os.Stderr, res.Status)
	}
	return nil
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	device := fs.String("device", "", "printer device path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := signalContext()
	p, err := ql.Open(*device)
	if err != nil {
		return err
	}
	defer p.Close()
	st, err := p.Status(ctx)
	if err != nil {
		return err
	}
	printStatusDetail(st)
	return nil
}

func cmdInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	device := fs.String("device", "", "printer device path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := signalContext()
	p, err := ql.Open(*device)
	if err != nil {
		return err
	}
	defer p.Close()
	st, err := p.Info(ctx)
	if err != nil {
		return err
	}
	printStatusDetail(st)
	fmt.Fprintln(os.Stderr, "note: the QL-570 raster protocol does not expose firmware version or serial number")
	return nil
}

func printStatusDetail(st *ql.Status) {
	fmt.Printf("model:            %s\n", st.Model)
	fmt.Printf("model code:       %02X %02X\n", st.ModelCode[0], st.ModelCode[1])
	fmt.Printf("media type:       %s\n", st.MediaTypeName)
	fmt.Printf("media width:      %d mm\n", st.MediaWidthMM)
	fmt.Printf("media length:     %d mm\n", st.MediaLengthMM)
	fmt.Printf("status type:      %s\n", st.StatusTypeName)
	fmt.Printf("phase type:       %s\n", st.PhaseTypeName)
	fmt.Printf("phase number:     %d\n", st.PhaseNumber)
	fmt.Printf("notification:     %s\n", st.NotificationName)
	if len(st.Errors) > 0 {
		fmt.Printf("errors:           %s\n", strings.Join(st.Errors, "; "))
	} else {
		fmt.Println("errors:           none")
	}
	fmt.Printf("raw status bytes: % X\n", st.Raw)
}
