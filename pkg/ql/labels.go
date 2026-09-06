package ql

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// FormFactor describes the shape of a label type.
type FormFactor int

const (
	// Endless is continuous length tape (DK rolls such as the DK-22210).
	Endless FormFactor = iota
	// DieCut is pre-sized rectangular die-cut labels.
	DieCut
	// RoundDieCut is pre-sized round die-cut labels.
	RoundDieCut
)

func (f FormFactor) String() string {
	switch f {
	case Endless:
		return "continuous"
	case DieCut:
		return "die-cut"
	case RoundDieCut:
		return "round die-cut"
	default:
		return "unknown"
	}
}

// Media describes one supported label size.
//
// Dot counts are at 300 dpi and are taken from the official Command
// Reference, section "3.2.2 Page size" and "3.2.5 Raster line arrangement".
type Media struct {
	// ID is the canonical identifier, e.g. "29" or "62x100".
	ID string
	// TapeWidthMM and TapeLengthMM are the physical tape size in
	// millimeters. TapeLengthMM is 0 for continuous tape.
	TapeWidthMM  int
	TapeLengthMM int
	// FormFactor is the shape of the label.
	FormFactor FormFactor
	// PrintableWidthDots is the usable width of the print area in dots.
	PrintableWidthDots int
	// PrintableHeightDots is the usable length in dots. It is 0 for
	// continuous tape where the length is chosen per job.
	PrintableHeightDots int
	// RightMarginDots is the number of pins between the right edge of the
	// print area and the right edge of the print head. Raster lines must
	// be placed inside the 720-pin row using this offset.
	RightMarginDots int
	// FeedMarginDots is the default margin (feed) amount in dots sent with
	// the "Set margin amount" command (ESC i d). 35 dots for continuous
	// tape and 12mm-dia labels, 0 for die-cut labels.
	FeedMarginDots int
}

// DefaultMediaID is the media assumed when a job does not specify one:
// DK-22210, 29 mm continuous paper tape.
const DefaultMediaID = "29"

// mediaTable contains all label sizes documented for the QL-570 (and the
// wider QL-500..700 family). 102mm media is omitted because the QL-570
// print head is only 62mm wide.
var mediaTable = []Media{
	{ID: "12", TapeWidthMM: 12, FormFactor: Endless, PrintableWidthDots: 106, RightMarginDots: 29, FeedMarginDots: 35},
	{ID: "29", TapeWidthMM: 29, FormFactor: Endless, PrintableWidthDots: 306, RightMarginDots: 6, FeedMarginDots: 35},
	{ID: "38", TapeWidthMM: 38, FormFactor: Endless, PrintableWidthDots: 413, RightMarginDots: 12, FeedMarginDots: 35},
	{ID: "50", TapeWidthMM: 50, FormFactor: Endless, PrintableWidthDots: 554, RightMarginDots: 12, FeedMarginDots: 35},
	{ID: "54", TapeWidthMM: 54, FormFactor: Endless, PrintableWidthDots: 590, RightMarginDots: 0, FeedMarginDots: 35},
	{ID: "62", TapeWidthMM: 62, FormFactor: Endless, PrintableWidthDots: 696, RightMarginDots: 12, FeedMarginDots: 35},

	{ID: "17x54", TapeWidthMM: 17, TapeLengthMM: 54, FormFactor: DieCut, PrintableWidthDots: 165, PrintableHeightDots: 566, RightMarginDots: 0},
	{ID: "17x87", TapeWidthMM: 17, TapeLengthMM: 87, FormFactor: DieCut, PrintableWidthDots: 165, PrintableHeightDots: 956, RightMarginDots: 0},
	{ID: "23x23", TapeWidthMM: 23, TapeLengthMM: 23, FormFactor: DieCut, PrintableWidthDots: 236, PrintableHeightDots: 202, RightMarginDots: 42},
	{ID: "29x90", TapeWidthMM: 29, TapeLengthMM: 90, FormFactor: DieCut, PrintableWidthDots: 306, PrintableHeightDots: 991, RightMarginDots: 6},
	{ID: "38x90", TapeWidthMM: 38, TapeLengthMM: 90, FormFactor: DieCut, PrintableWidthDots: 413, PrintableHeightDots: 991, RightMarginDots: 12},
	{ID: "39x48", TapeWidthMM: 39, TapeLengthMM: 48, FormFactor: DieCut, PrintableWidthDots: 425, PrintableHeightDots: 495, RightMarginDots: 6},
	{ID: "52x29", TapeWidthMM: 52, TapeLengthMM: 29, FormFactor: DieCut, PrintableWidthDots: 578, PrintableHeightDots: 271, RightMarginDots: 0},
	{ID: "62x29", TapeWidthMM: 62, TapeLengthMM: 29, FormFactor: DieCut, PrintableWidthDots: 696, PrintableHeightDots: 271, RightMarginDots: 12},
	{ID: "62x100", TapeWidthMM: 62, TapeLengthMM: 100, FormFactor: DieCut, PrintableWidthDots: 696, PrintableHeightDots: 1109, RightMarginDots: 12},

	{ID: "d12", TapeWidthMM: 12, TapeLengthMM: 12, FormFactor: RoundDieCut, PrintableWidthDots: 94, PrintableHeightDots: 94, RightMarginDots: 113, FeedMarginDots: 35},
	{ID: "d24", TapeWidthMM: 24, TapeLengthMM: 24, FormFactor: RoundDieCut, PrintableWidthDots: 236, PrintableHeightDots: 236, RightMarginDots: 42},
	{ID: "d58", TapeWidthMM: 58, TapeLengthMM: 58, FormFactor: RoundDieCut, PrintableWidthDots: 618, PrintableHeightDots: 618, RightMarginDots: 51},
}

// LookupMedia returns the media with the given identifier. Identifiers are
// case-insensitive, may contain spaces and an optional "mm" suffix, so
// "29", "29mm" and "29 mm" all select the 29 mm continuous tape.
func LookupMedia(id string) (Media, error) {
	id = strings.ToLower(strings.ReplaceAll(id, " ", ""))
	id = strings.TrimSuffix(id, "mm")
	if id == "" {
		id = DefaultMediaID
	}
	for _, m := range mediaTable {
		if m.ID == id {
			return m, nil
		}
	}
	return Media{}, fmt.Errorf("unknown media %q (supported: %s)", id, strings.Join(MediaIDs(), ", "))
}

// MediaIDs returns all supported media identifiers, sorted.
func MediaIDs() []string {
	ids := make([]string, 0, len(mediaTable))
	for _, m := range mediaTable {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	return ids
}

// DPI is the fixed print resolution of the QL-570.
const DPI = 300

// MMToDots converts millimeters to dots at 300 dpi.
func MMToDots(mm float64) int {
	return int(math.Round(mm * DPI / 25.4))
}

// DotsToMM converts dots at 300 dpi to millimeters.
func DotsToMM(dots int) float64 {
	return float64(dots) * 25.4 / DPI
}

// MinLengthMM and MaxLengthMM bound the printable length of continuous tape
// for the QL-570 (150..11811 dots at 300 dpi).
const (
	MinLengthMM = 12.7
	MaxLengthMM = 1000.0
)
