package ql

import (
	"fmt"
	"strings"
)

// Command bytes from the official "Brother QL-500/550/560/570/580N/650TD/
// 700/1050/1060N Command Reference", section 5 "Command Details".
//
// Command overview (all values in hex):
//
//	Invalid command                00 (200 bytes to clear the buffer)
//	Initialize                     1B 40
//	Status information request     1B 69 53
//	Print information command      1B 69 7A + 10-byte payload
//	Set each mode                  1B 69 4D + {n}   (bit 6 = auto cut)
//	Cut every n labels             1B 69 41 + {n}   (QL-570 supported)
//	Set expanded mode              1B 69 4B + {n}   (bit 3 cut at end, bit 6 600dpi)
//	Set margin amount              1B 69 64 + {n1} {n2} (LE16 dots)
//	Compression mode selection     4D + {n} (0 = none; TIFF not on QL-570)
//	Raster graphics transfer       67 {s} {n} {d1..dn} (s=00 data, FF stop)
//	Zero raster graphics           5A
//	Print command                  0C  (intermediate page)
//	Print command with feeding     1A  (last page)
const (
	esc byte = 0x1B

	opInit         byte = 0x40 // ESC @
	opStatusReq    byte = 0x53 // ESC i S
	opPrintInfo    byte = 0x7A // ESC i z
	opSetEachMode  byte = 0x4D // ESC i M
	opCutEvery     byte = 0x41 // ESC i A
	opSetExpanded  byte = 0x4B // ESC i K
	opSetMargin    byte = 0x64 // ESC i d
	opRaster       byte = 0x67 // g
	opZeroRaster   byte = 0x5A // Z
	opCompressMode byte = 0x4D // M (single byte command, no ESC prefix)
	opPrintFF      byte = 0x0C // print, intermediate page
	opPrintEOF     byte = 0x1A // print with feeding, last page

	// Print information command valid flag bits ({n1}).
	printInfoFlagAlways  byte = 0x80 // PI_RECOVER, always set
	printInfoFlagKind    byte = 0x02 // PI_KIND: paper type valid
	printInfoFlagWidth   byte = 0x04 // PI_WIDTH: paper width valid
	printInfoFlagLength  byte = 0x08 // PI_LENGTH: paper length valid
	printInfoFlagQuality byte = 0x40 // PI_QUALITY: give priority to print quality

	// Media types in the status response ({byte 11}) and the print
	// information command.
	MediaTypeNone       byte = 0x00
	MediaTypeContinuous byte = 0x0A
	MediaTypeDieCut     byte = 0x0B

	// Status types in the status response (byte 18).
	StatusTypeReply             byte = 0x00
	StatusTypePrintingCompleted byte = 0x01
	StatusTypeError             byte = 0x02
	StatusTypeNotification      byte = 0x05
	StatusTypePhaseChange       byte = 0x06

	// Phase types in the status response (byte 19).
	PhaseTypeWaitingToReceive byte = 0x00
	PhaseTypePrinting         byte = 0x01

	// Notification numbers in the status response (byte 22).
	NotificationNone         byte = 0x00
	NotificationCoolingStart byte = 0x03
	NotificationCoolingEnd   byte = 0x04
)

// Status header bytes of the 32-byte status response.
var statusHeader = [3]byte{0x80, 0x20, 0x42}

// cmdInitialize returns the "Initialize" command (ESC @).
func cmdInitialize() []byte { return []byte{esc, opInit} }

// cmdInvalidate returns n "Invalid command" bytes (NULL), used to clear the
// printer's command buffer.
func cmdInvalidate(n int) []byte { return make([]byte, n) }

// cmdStatusRequest returns the "Status information request" command
// (ESC i S). The printer replies with a 32-byte status response.
func cmdStatusRequest() []byte { return []byte{esc, 'i', opStatusReq} }

// cmdSetEachMode returns the "Set each mode" command (ESC i M).
// Bit 6: auto cut (1 = on, 0 = off).
func cmdSetEachMode(autoCut bool) []byte {
	n := byte(0)
	if autoCut {
		n = 1 << 6
	}
	return []byte{esc, 'i', opSetEachMode, n}
}

// cmdCutEvery returns the "Specify the page number in cut every * labels"
// command (ESC i A), valid only when auto cut is enabled.
func cmdCutEvery(n int) []byte {
	return []byte{esc, 'i', opCutEvery, byte(n & 0xFF)}
}

// cmdSetExpandedMode returns the "Set expanded mode" command (ESC i K).
// Bit 3: cut at end when printing multiple pages (1 = cut, default).
// Bit 6: high resolution printing, 600 dpi in the length direction
// (QL-570/580N/700; 0 = 300 dpi, default).
func cmdSetExpandedMode(cutAtEnd, hires600 bool) []byte {
	n := byte(0)
	if cutAtEnd {
		n |= 1 << 3
	}
	if hires600 {
		n |= 1 << 6
	}
	return []byte{esc, 'i', opSetExpanded, n}
}

// cmdSetMargin returns the "Set margin amount (feed amount)" command
// (ESC i d). Margin amount (dots) = n1 + 256*n2. For continuous tape the
// reference specifies 35 dots (3 mm); for die-cut labels 0.
func cmdSetMargin(dots int) []byte {
	return []byte{esc, 'i', opSetMargin, byte(dots & 0xFF), byte(dots >> 8 & 0xFF)}
}

// cmdSelectCompression returns the "Compression mode selection" command
// (M + {n}). n = 0: no compression, n = 2: TIFF PackBits. TIFF is only
// supported by QL-580N/650TD/1050/1060N, not by the QL-570.
func cmdSelectCompression(tiff bool) []byte {
	n := byte(0)
	if tiff {
		n = 0x02
	}
	return []byte{opCompressMode, n}
}

// cmdRasterRow returns the "Raster graphics transfer" command for one
// raster line: g + 00 + {n} + {data}. {n} is the number of bytes
// transferred (90 for the QL-570).
func cmdRasterRow(row []byte) []byte {
	out := make([]byte, 0, 3+len(row))
	out = append(out, opRaster, 0x00, byte(len(row)))
	out = append(out, row...)
	return out
}

// opZeroRaster returns the "Zero raster graphics" command (Z), one raster
// line filled with zero data.
func cmdZeroRaster() []byte { return []byte{opZeroRaster} }

// cmdPrint returns the "Print command" (0C) for an intermediate page or the
// "Print command with feeding" (1A) for the last page.
func cmdPrint(intermediate bool) []byte {
	if intermediate {
		return []byte{opPrintFF}
	}
	return []byte{opPrintEOF}
}

// PrintInfo is the payload of the "Print information command" (ESC i z).
type PrintInfo struct {
	// MediaType is MediaTypeContinuous or MediaTypeDieCut.
	MediaType byte
	// WidthMM and LengthMM describe the media in millimeters. LengthMM is
	// 0 for continuous tape.
	WidthMM  int
	LengthMM int
	// RasterNumber is the number of raster lines in the page.
	RasterNumber int
	// StartingPage is 0 for the first page of a job, 1 for all others.
	StartingPage int
	// Quality gives priority to print quality (PI_QUALITY bit).
	Quality bool
}

// cmdPrintInformation returns the "Print information command"
// (ESC i z + {n1}..{n10}).
//
//	{n1}:   valid flag (0x80 always, plus bits for kind/width/length/quality)
//	{n2}:   paper type (0A continuous, 0B die-cut)
//	{n3}:   paper width in mm
//	{n4}:   paper length in mm
//	{n5-n8}: raster number, little-endian 32 bit
//	{n9}:   starting page (0 first, 1 otherwise)
//	{n10}:  fixed 0
func cmdPrintInformation(pi PrintInfo) []byte {
	flags := printInfoFlagAlways
	if pi.MediaType != MediaTypeNone {
		flags |= printInfoFlagKind
	}
	if pi.WidthMM != 0 {
		flags |= printInfoFlagWidth
	}
	if pi.LengthMM != 0 {
		flags |= printInfoFlagLength
	}
	if pi.Quality {
		flags |= printInfoFlagQuality
	}
	r := uint32(pi.RasterNumber)
	page := byte(0)
	if pi.StartingPage != 0 {
		page = 1
	}
	return []byte{
		esc, 'i', opPrintInfo,
		flags,
		pi.MediaType,
		byte(pi.WidthMM & 0xFF),
		byte(pi.LengthMM & 0xFF),
		byte(r & 0xFF),
		byte(r >> 8 & 0xFF),
		byte(r >> 16 & 0xFF),
		byte(r >> 24 & 0xFF),
		page,
		0x00,
	}
}

// JobPage is one page of a multi-page print stream: the page's mode
// settings plus its raster lines.
type JobPage struct {
	Opts PrintOptions
	Rows [][]byte
}

// BuildJobStream assembles the complete command stream for a print job of
// one or more identical pages (copies), following the "3. Print Data"
// structure of the official Command Reference:
//
//	invalidate (200x 00), initialize, then per page:
//	status request, print information, set each mode, cut every,
//	set expanded mode, set margin, raster lines, print command
//	(0C between pages, 1A with feeding after the last page).
func BuildJobStream(m Model, opts PrintOptions, rows [][]byte) []byte {
	pages := make([]JobPage, opts.Pages)
	for i := range pages {
		pages[i] = JobPage{Opts: opts, Rows: rows}
	}
	return BuildJobsStream(m, pages)
}

// BuildJobsStream assembles one command stream for a batch of pages (each
// with its own mode settings, media information and raster data). All pages
// of a batch must use the same physical media; the media information and
// the "starting page" flag of the print information command differ per
// page.
func BuildJobsStream(m Model, pages []JobPage) []byte {
	buf := make([]byte, 0, 256+len(pages)*QL570.RowBytes*2)
	buf = append(buf, cmdInvalidate(m.InvalidateBytes)...)
	buf = append(buf, cmdInitialize()...)

	for i, page := range pages {
		buf = append(buf, cmdStatusRequest()...)
		pi := PrintInfo{
			MediaType:    page.Opts.MediaType,
			WidthMM:      page.Opts.WidthMM,
			LengthMM:     page.Opts.LengthMM,
			RasterNumber: len(page.Rows),
			StartingPage: i,
			Quality:      page.Opts.Quality,
		}
		buf = append(buf, cmdPrintInformation(pi)...)
		buf = append(buf, cmdSetEachMode(page.Opts.AutoCut)...)
		if page.Opts.AutoCut && m.SupportsCutEvery {
			buf = append(buf, cmdCutEvery(page.Opts.CutEvery)...)
		}
		buf = append(buf, cmdSetExpandedMode(page.Opts.CutAtEnd, page.Opts.Hires600)...)
		buf = append(buf, cmdSetMargin(page.Opts.FeedDots)...)
		for _, row := range page.Rows {
			if page.Opts.TIFF && allZero(row) {
				buf = append(buf, cmdZeroRaster()...)
			} else {
				buf = append(buf, cmdRasterRow(row)...)
			}
		}
		buf = append(buf, cmdPrint(i != len(pages)-1)...)
	}
	return buf
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// PrintOptions parameterizes a page of the command stream.
type PrintOptions struct {
	MediaType byte
	WidthMM   int
	LengthMM  int
	// Pages is the number of identical pages printed by BuildJobStream.
	// It is ignored by BuildJobsStream, which builds one page per JobPage.
	Pages    int
	AutoCut  bool
	CutEvery int
	CutAtEnd bool
	Hires600 bool
	FeedDots int
	Quality  bool
	TIFF     bool
}

// --- Status response parsing ---------------------------------------------

// Status is the decoded 32-byte status response (section 4 of the official
// Command Reference).
type Status struct {
	Raw              []byte   `json:"-"`
	ModelCode        [2]byte  `json:"model_code"`
	Model            string   `json:"model"`
	ErrorInfo1       byte     `json:"error_info_1"`
	ErrorInfo2       byte     `json:"error_info_2"`
	Errors           []string `json:"errors"`
	MediaWidthMM     int      `json:"media_width_mm"`
	MediaLengthMM    int      `json:"media_length_mm"`
	MediaType        byte     `json:"media_type"`
	MediaTypeName    string   `json:"media_type_name"`
	StatusType       byte     `json:"status_type"`
	StatusTypeName   string   `json:"status_type_name"`
	PhaseType        byte     `json:"phase_type"`
	PhaseTypeName    string   `json:"phase_type_name"`
	PhaseNumber      uint16   `json:"phase_number"`
	Notification     byte     `json:"notification"`
	NotificationName string   `json:"notification_name"`
}

// Status byte layout (offset, size, name):
//
//	0  1  print head mark (80)
//	1  1  size (20 = 32 bytes)
//	2  1  fixed 'B' (42)
//	3  1  device dependent (QL-570: '4' = 34)
//	4  1  device dependent (QL-570: '2' = 32)
//	5  1  fixed '0' (30)
//	6  1  fixed 00
//	7  1  fixed 00
//	8  1  error information 1
//	9  1  error information 2
//	10 1  media width (mm)
//	11 1  media type (00 none, 0A continuous, 0B die-cut)
//	12 1  fixed 00
//	13 1  fixed 00
//	14 1  reserved
//	15 1  reserved
//	16 1  fixed 00
//	17 1  media length (mm)
//	18 1  status type
//	19 1  phase type
//	20 1  phase number, higher order byte
//	21 1  phase number, lower order byte
//	22 1  notification number
//	23-31 reserved
const statusLen = 32

// Error information 1 bit definitions (byte 8), section 4.2.1.
var errorInfo1Bits = []struct {
	mask byte
	name string
}{
	{0x01, "no media when printing"},
	{0x02, "end of media"},
	{0x04, "tape cutter jam"},
	{0x10, "main unit in use"},
	{0x80, "fan error"},
}

// Error information 2 bit definitions (byte 9), section 4.2.1.
// Bit 0 is listed as "not used" in 4.2.1 but is set when the loaded media
// does not match the print information command (section 5); brother_ql
// names it "replace media error".
var errorInfo2Bits = []struct {
	mask byte
	name string
}{
	{0x01, "replace media"},
	{0x04, "transmission error"},
	{0x10, "cover opened while printing"},
	{0x40, "media cannot be fed (or media end)"},
	{0x80, "system error"},
}

var modelCodes = map[[2]byte]string{
	{0x30, 0x4F}: "QL-500/550",
	{0x34, 0x31}: "QL-560",
	{0x34, 0x32}: "QL-570",
	{0x34, 0x33}: "QL-580N",
	{0x30, 0x51}: "QL-650TD",
	{0x34, 0x35}: "QL-700",
	{0x30, 0x50}: "QL-1050",
	{0x34, 0x34}: "QL-1060N",
}

// ParseStatus decodes a 32-byte status response from the printer.
func ParseStatus(data []byte) (*Status, error) {
	if len(data) < statusLen {
		return nil, fmt.Errorf("status response too short: %d bytes (want %d)", len(data), statusLen)
	}
	if !(data[0] == statusHeader[0] && data[1] == statusHeader[1] && data[2] == statusHeader[2]) {
		return nil, fmt.Errorf("status response has unexpected header: % X", data[:3])
	}
	s := &Status{
		Raw:           append([]byte(nil), data[:statusLen]...),
		ModelCode:     [2]byte{data[3], data[4]},
		ErrorInfo1:    data[8],
		ErrorInfo2:    data[9],
		MediaWidthMM:  int(data[10]),
		MediaLengthMM: int(data[17]),
		MediaType:     data[11],
		StatusType:    data[18],
		PhaseType:     data[19],
		PhaseNumber:   uint16(data[20])<<8 | uint16(data[21]),
		Notification:  data[22],
	}
	if name, ok := modelCodes[s.ModelCode]; ok {
		s.Model = name
	} else {
		s.Model = fmt.Sprintf("unknown (%02X/%02X)", s.ModelCode[0], s.ModelCode[1])
	}
	for _, b := range errorInfo1Bits {
		if s.ErrorInfo1&b.mask != 0 {
			s.Errors = append(s.Errors, b.name)
		}
	}
	for _, b := range errorInfo2Bits {
		if s.ErrorInfo2&b.mask != 0 {
			s.Errors = append(s.Errors, b.name)
		}
	}
	s.MediaTypeName = MediaTypeName(s.MediaType)
	s.StatusTypeName = StatusTypeName(s.StatusType)
	s.PhaseTypeName = PhaseTypeName(s.PhaseType)
	s.NotificationName = NotificationName(s.Notification)
	return s, nil
}

// HasErrors reports whether the status carries error information.
func (s *Status) HasErrors() bool { return s.ErrorInfo1 != 0 || s.ErrorInfo2 != 0 }

// MediaTypeName returns the human-readable name of a media type byte.
func MediaTypeName(t byte) string {
	switch t {
	case MediaTypeNone:
		return "no media"
	case MediaTypeContinuous:
		return "continuous length tape"
	case MediaTypeDieCut:
		return "die-cut labels"
	default:
		return fmt.Sprintf("unknown (0x%02X)", t)
	}
}

// StatusTypeName returns the human-readable name of a status type byte.
func StatusTypeName(t byte) string {
	switch t {
	case StatusTypeReply:
		return "reply to status request"
	case StatusTypePrintingCompleted:
		return "printing completed"
	case StatusTypeError:
		return "error occurred"
	case StatusTypeNotification:
		return "notification"
	case StatusTypePhaseChange:
		return "phase change"
	default:
		return fmt.Sprintf("unknown (0x%02X)", t)
	}
}

// PhaseTypeName returns the human-readable name of a phase type byte.
func PhaseTypeName(t byte) string {
	switch t {
	case PhaseTypeWaitingToReceive:
		return "waiting to receive"
	case PhaseTypePrinting:
		return "printing state"
	default:
		return fmt.Sprintf("unknown (0x%02X)", t)
	}
}

// NotificationName returns the human-readable name of a notification byte.
func NotificationName(t byte) string {
	switch t {
	case NotificationNone:
		return "not available"
	case NotificationCoolingStart:
		return "cooling (start)"
	case NotificationCoolingEnd:
		return "cooling (finish)"
	default:
		return fmt.Sprintf("unknown (0x%02X)", t)
	}
}

// String renders the status in a compact, human-readable form.
func (s *Status) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "model: %s", s.Model)
	if len(s.Errors) > 0 {
		fmt.Fprintf(&b, ", errors: %s", strings.Join(s.Errors, "; "))
	} else {
		b.WriteString(", no errors")
	}
	fmt.Fprintf(&b, ", media: %s", s.MediaTypeName)
	if s.MediaWidthMM != 0 {
		fmt.Fprintf(&b, " %dmm", s.MediaWidthMM)
	}
	if s.MediaLengthMM != 0 {
		fmt.Fprintf(&b, " x %dmm", s.MediaLengthMM)
	}
	fmt.Fprintf(&b, ", status: %s, phase: %s", s.StatusTypeName, s.PhaseTypeName)
	if s.Notification != NotificationNone {
		fmt.Fprintf(&b, ", notification: %s", s.NotificationName)
	}
	return b.String()
}
