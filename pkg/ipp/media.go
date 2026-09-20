package ipp

import (
	"fmt"

	"github.com/OpenPrinting/goipp"
	"github.com/sschueller/brother-ql570-go/pkg/ql"
)

// ContinuousLengthsMM are the selectable label lengths for continuous tape
// (endless media) offered to IPP clients, in millimeters.
var ContinuousLengthsMM = []int{25, 40, 62, 90}

// MediaOption is one entry of the IPP media catalog: a media size the IPP
// client can pick, mapped to the ql media it prints on.
type MediaOption struct {
	// Name is the IPP media name / media-size-name keyword, e.g.
	// "oem_ql570-29x62mm".
	Name string
	// XUM and YUM are the media-size x-dimension and y-dimension in
	// hundredths of a millimeter (1/100 mm), the unit required by the
	// IPP media-size attribute (PWG 5101.1).
	XUM, YUM int
	// Media is the ql media this option prints on. Zero for standard
	// sizes, which are resolved against the loaded media at print time.
	Media ql.Media
	// LengthMM is the label length for continuous media. It is 0 for
	// die-cut media where the length is fixed by the media itself.
	LengthMM float64
	// Standard marks a PWG standard media size (from the fixed table
	// Android's built-in print service understands). Standard sizes are
	// mapped onto the loaded QL-570 media when a job arrives.
	Standard bool
	// Aspect is the width/height ratio of the option, used to map
	// standard sizes onto continuous tape.
	Aspect float64
}

// standardMediaSizes are the PWG standard media sizes advertised to IPP
// clients. Android's built-in print service only offers paper sizes it
// recognizes from its own fixed table of standard sizes (it ignores
// vendor names), so the phone offers exactly these. Each size has a
// label-like aspect ratio and is mapped onto the loaded QL-570 media when
// a job arrives (see ResolveMedia).
var standardMediaSizes = []MediaOption{
	{Name: "om_card_54x86mm", XUM: 5400, YUM: 8600, Standard: true, Aspect: 5400.0 / 8600.0},        // address label
	{Name: "na_index-4x6_4x6in", XUM: 10160, YUM: 15240, Standard: true, Aspect: 10160.0 / 15240.0}, // photo
	{Name: "oe_photo-l_3.5x5in", XUM: 8890, YUM: 12700, Standard: true, Aspect: 8890.0 / 12700.0},   // small photo
	{Name: "na_5x7_5x7in", XUM: 12700, YUM: 17780, Standard: true, Aspect: 12700.0 / 17780.0},       // large photo
}

// StandardMediaOptions returns the standard media sizes offered to IPP
// clients.
func StandardMediaOptions() []MediaOption {
	return standardMediaSizes
}

// MediaName builds the IPP media name for a ql media and label length.
func MediaName(m ql.Media, lengthMM int) string {
	return fmt.Sprintf("oem_ql570-%dx%dmm", m.TapeWidthMM, lengthMM)
}

// mediaOptionFor maps a ql media (plus length) to an IPP media option.
// LengthMM is only set for continuous media; for die-cut the length is
// fixed by the media itself.
func mediaOptionFor(m ql.Media, lengthMM int) MediaOption {
	o := MediaOption{
		Name:  MediaName(m, lengthMM),
		XUM:   m.TapeWidthMM * 100,
		YUM:   lengthMM * 100,
		Media: m,
	}
	if m.FormFactor == ql.Endless {
		o.LengthMM = float64(lengthMM)
	}
	return o
}

// BuildMediaCatalog returns the IPP media catalog derived from the full ql
// media catalog: every die-cut size 1:1 plus the continuous tape widths
// combined with ContinuousLengthsMM.
//
// Die-cut entries come first. Where a continuous width x length
// combination would collide with a die-cut size of the same dimensions
// (29x90 and 38x90 exist as die-cut labels), the die-cut entry wins and
// the continuous variant is omitted so every media name and size maps to
// exactly one ql media. Those label lengths stay reachable through the
// web UI / API.
func BuildMediaCatalog() []MediaOption {
	var out []MediaOption
	seen := map[string]bool{}
	add := func(o MediaOption) {
		if seen[o.Name] {
			return
		}
		seen[o.Name] = true
		out = append(out, o)
	}
	for _, m := range ql.MediaCatalog() {
		switch m.FormFactor {
		case ql.DieCut, ql.RoundDieCut:
			add(mediaOptionFor(m, m.TapeLengthMM))
		}
	}
	for _, m := range ql.MediaCatalog() {
		if m.FormFactor != ql.Endless {
			continue
		}
		for _, l := range ContinuousLengthsMM {
			add(mediaOptionFor(m, l))
		}
	}
	return out
}

// DefaultMediaOption is the media used when an IPP job does not select
// one: the 54x86mm address-card standard size, which resolves to the
// loaded label media at print time (aspect ratio preserved).
func DefaultMediaOption() MediaOption {
	return standardMediaSizes[0]
}

// LookupIPPMedia finds the media option matching an IPP media-size
// x-dimension / y-dimension pair (in 1/100 mm). Both the oem label sizes
// and the advertised standard sizes match.
func LookupIPPMedia(xUM, yUM int) (MediaOption, error) {
	for _, o := range BuildMediaCatalog() {
		if o.XUM == xUM && o.YUM == yUM {
			return o, nil
		}
	}
	for _, o := range standardMediaSizes {
		if o.XUM == xUM && o.YUM == yUM {
			return o, nil
		}
	}
	return MediaOption{}, fmt.Errorf("media size %dx%d (1/100 mm) is not supported by the QL-570", xUM, yUM)
}

// LookupIPPMediaByName finds a media option by its IPP media name.
func LookupIPPMediaByName(name string) (MediaOption, error) {
	for _, o := range BuildMediaCatalog() {
		if o.Name == name {
			return o, nil
		}
	}
	for _, o := range standardMediaSizes {
		if o.Name == name {
			return o, nil
		}
	}
	return MediaOption{}, fmt.Errorf("media %q is not supported by the QL-570", name)
}

// lookupDieCut finds the die-cut option with the given width and length.
func lookupDieCut(widthMM, lengthMM int) (MediaOption, error) {
	for _, o := range BuildMediaCatalog() {
		if o.Media.FormFactor != ql.Endless && o.Media.TapeWidthMM == widthMM && o.Media.TapeLengthMM == lengthMM {
			return o, nil
		}
	}
	return MediaOption{}, fmt.Errorf("die-cut %dx%dmm labels are not supported", widthMM, lengthMM)
}

// ResolveMedia maps an IPP media option onto the concrete ql media to
// print on, using the printer's loaded media. oem label sizes pass
// through unchanged. Standard sizes are mapped to the loaded media: a
// die-cut label is used as-is; continuous tape uses auto-fit labels
// (LengthMM 0) so each page is exactly as tall as its content — the
// chosen paper size's aspect ratio and the portrait/landscape choice
// therefore change the printed label. With no status available, 62 mm
// continuous tape is assumed. The standard name is kept so the
// document's self-describing media name still matches.
func ResolveMedia(opt MediaOption, st *ql.Status) (MediaOption, error) {
	if !opt.Standard {
		return opt, nil
	}
	widthMM := 62
	if st != nil && st.MediaWidthMM > 0 {
		widthMM = st.MediaWidthMM
		if st.MediaType == ql.MediaTypeDieCut && st.MediaLengthMM > 0 {
			if m, err := lookupDieCut(widthMM, st.MediaLengthMM); err == nil {
				return m, nil
			}
		}
	}
	for _, o := range BuildMediaCatalog() {
		if o.Media.FormFactor == ql.Endless && o.Media.TapeWidthMM == widthMM {
			o.LengthMM = 0 // auto-fit: the label grows with the content
			o.Name = opt.Name
			return o, nil
		}
	}
	return MediaOption{}, fmt.Errorf("continuous %d mm tape is not supported", widthMM)
}

// MediaReady returns the media options matching the media currently loaded
// in the printer per its status. For continuous tape all lengths of the
// loaded width are ready; for die-cut the exact size is ready. It returns
// nil when the status carries no media information.
func MediaReady(st *ql.Status) []MediaOption {
	if st == nil || st.MediaWidthMM == 0 {
		return nil
	}
	var out []MediaOption
	for _, o := range BuildMediaCatalog() {
		if o.Media.TapeWidthMM != st.MediaWidthMM {
			continue
		}
		switch st.MediaType {
		case ql.MediaTypeContinuous:
			if o.Media.FormFactor == ql.Endless {
				out = append(out, o)
			}
		case ql.MediaTypeDieCut:
			if o.Media.FormFactor != ql.Endless && o.Media.TapeLengthMM == st.MediaLengthMM {
				out = append(out, o)
			}
		}
	}
	return out
}

// mediaSizeCollection returns the "media-size" collection attribute with
// x-dimension and y-dimension members.
func mediaSizeCollection(xUM, yUM int) goipp.Attribute {
	collection := make(goipp.Collection, 0, 2)
	collection.Add(goipp.MakeAttribute("x-dimension", goipp.TagInteger, goipp.Integer(xUM)))
	collection.Add(goipp.MakeAttribute("y-dimension", goipp.TagInteger, goipp.Integer(yUM)))
	return goipp.MakeAttribute("media-size", goipp.TagBeginCollection, collection)
}

// mediaColCollection returns the "media-col" collection attribute
// describing one media option.
func mediaColCollection(o MediaOption) goipp.Attribute {
	collection := make(goipp.Collection, 0, 2)
	collection.Add(mediaSizeCollection(o.XUM, o.YUM))
	collection.Add(goipp.MakeAttribute("media-size-name", goipp.TagKeyword, goipp.String(o.Name)))
	return goipp.MakeAttribute("media-col", goipp.TagBeginCollection, collection)
}

// MediaColDatabase returns the IPP "media-col-database" attribute listing
// the media catalog as media-col collections: the standard sizes first
// (the only ones Android offers), then the oem label sizes.
func MediaColDatabase() goipp.Attribute {
	attr := goipp.Attribute{Name: "media-col-database"}
	for _, o := range standardMediaSizes {
		attr.Values.Add(goipp.TagBeginCollection, mediaColCollectionValue(o))
	}
	for _, o := range BuildMediaCatalog() {
		attr.Values.Add(goipp.TagBeginCollection, mediaColCollectionValue(o))
	}
	return attr
}

// mediaColCollectionValue is mediaColCollection without the attribute
// wrapper, for use as one value of a 1setOf collection attribute.
func mediaColCollectionValue(o MediaOption) goipp.Collection {
	collection := make(goipp.Collection, 0, 2)
	collection.Add(mediaSizeCollection(o.XUM, o.YUM))
	collection.Add(goipp.MakeAttribute("media-size-name", goipp.TagKeyword, goipp.String(o.Name)))
	return collection
}

// MediaColDefault returns the IPP "media-col-default" attribute (a
// media-col collection) for the default media.
func MediaColDefault() goipp.Attribute {
	return goipp.MakeAttribute("media-col-default", goipp.TagBeginCollection, mediaColCollectionValue(DefaultMediaOption()))
}

// MediaDefaultKeyword returns the legacy IPP "media-default" attribute as
// a keyword. Android's built-in print service (an old CUPS-derived IPP
// stack) only understands the keyword form; real printers return both.
func MediaDefaultKeyword() goipp.Attribute {
	return goipp.MakeAttribute("media-default", goipp.TagKeyword, goipp.String(DefaultMediaOption().Name))
}

// MediaColReady returns the IPP "media-col-ready" attribute from the
// printer status. It lists the standard sizes (always offered, they map
// onto whatever is loaded) followed by the exact options matching the
// loaded media.
func MediaColReady(st *ql.Status) goipp.Attribute {
	attr := goipp.Attribute{Name: "media-col-ready"}
	for _, o := range standardMediaSizes {
		attr.Values.Add(goipp.TagBeginCollection, mediaColCollectionValue(o))
	}
	for _, o := range MediaReady(st) {
		attr.Values.Add(goipp.TagBeginCollection, mediaColCollectionValue(o))
	}
	return attr
}

// MediaReadyKeywords returns the legacy "media-ready" attribute as a
// keyword list (the only form Android's built-in print service parses).
func MediaReadyKeywords(st *ql.Status) goipp.Attribute {
	attr := goipp.Attribute{Name: "media-ready"}
	for _, o := range standardMediaSizes {
		attr.Values.Add(goipp.TagKeyword, goipp.String(o.Name))
	}
	for _, o := range MediaReady(st) {
		attr.Values.Add(goipp.TagKeyword, goipp.String(o.Name))
	}
	return attr
}

// MediaSupported returns the IPP "media-supported" attribute listing all
// media names (standard sizes first, then the oem label sizes).
func MediaSupported() goipp.Attribute {
	attr := goipp.Attribute{Name: "media-supported"}
	for _, o := range standardMediaSizes {
		attr.Values.Add(goipp.TagKeyword, goipp.String(o.Name))
	}
	for _, o := range BuildMediaCatalog() {
		attr.Values.Add(goipp.TagKeyword, goipp.String(o.Name))
	}
	return attr
}
