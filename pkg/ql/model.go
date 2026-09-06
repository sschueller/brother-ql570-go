// Package ql implements the Brother QL-series raster command protocol and
// host-side label rendering for the Brother QL-570 label printer.
//
// All command bytes are cross-checked against the official "Brother
// QL-500/550/560/570/580N/650TD/700/1050/1060N Command Reference" and the
// battle-tested brother_ql Python project (github.com/pklaus/brother_ql).
package ql

// Model describes the fixed protocol parameters of one Brother QL printer
// model, taken from the official Command Reference.
type Model struct {
	// Name is the Brother model name, e.g. "QL-570".
	Name string
	// ModelCode is the two device-dependent bytes of the 32-byte status
	// response that identify the model (bytes 3 and 4).
	ModelCode [2]byte
	// RowBytes is the number of bytes in one raster line (90 = 720 pins).
	RowBytes int
	// RowPins is the total number of print head pins (720 for QL-570).
	RowPins int
	// MinLengthDots and MaxLengthDots bound the printable length of
	// continuous tape in dots at 300 dpi.
	MinLengthDots int
	MaxLengthDots int
	// MinFeedDots and MaxFeedDots bound the margin/feed amount in dots.
	MinFeedDots int
	MaxFeedDots int
	// SupportsCompressionTIFF is true if the model accepts PackBits/TIFF
	// compressed raster lines. The QL-570 does not.
	SupportsCompressionTIFF bool
	// SupportsModeSwitch is true if the model needs the "command mode
	// switch" command (ESC i a). The QL-570 is always in raster mode.
	SupportsModeSwitch bool
	// InvalidateBytes is the number of NULL bytes sent to clear the
	// command buffer before each job.
	InvalidateBytes int
	// SupportsCutEvery is true if the "cut every n labels" command
	// (ESC i A) is available.
	SupportsCutEvery bool
}

// QL570 is the only model supported by this package so far. Parameters are
// from the official Command Reference, section "3.2.3 Feed amount" and
// "3.2.4 Maximum and minimum lengths".
var QL570 = Model{
	Name:                    "QL-570",
	ModelCode:               [2]byte{0x34, 0x32},
	RowBytes:                90,
	RowPins:                 720,
	MinLengthDots:           150,
	MaxLengthDots:           11811,
	MinFeedDots:             35,
	MaxFeedDots:             1500,
	SupportsCompressionTIFF: false,
	SupportsModeSwitch:      false,
	InvalidateBytes:         200,
	SupportsCutEvery:        true,
}
