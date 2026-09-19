package ql

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
)

func TestParseFontStyle(t *testing.T) {
	good := map[string]fontStyle{
		"":            {},
		"normal":      {},
		"NORMAL":      {},
		"bold":        {bold: true},
		"Bold":        {bold: true},
		"italic":      {italic: true},
		"italics":     {italic: true},
		"bold-italic": {bold: true, italic: true},
		"bold,italic": {bold: true, italic: true},
		"italic,bold": {bold: true, italic: true},
		"italic bold": {bold: true, italic: true},
		"bold+italic": {bold: true, italic: true},
		" italic ":    {italic: true},
	}
	for in, want := range good {
		got, err := parseFontStyle(in)
		if err != nil {
			t.Errorf("parseFontStyle(%q): unexpected error %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseFontStyle(%q) = %+v, want %+v", in, got, want)
		}
	}
	for _, in := range []string{"slanted", "b0ld", "condensed", "bold-underline"} {
		if _, err := parseFontStyle(in); err == nil {
			t.Errorf("parseFontStyle(%q): expected error", in)
		}
	}
}

func TestFontStyleString(t *testing.T) {
	for _, tc := range []struct {
		st   fontStyle
		want string
	}{
		{st: fontStyle{}, want: TextStyleNormal},
		{st: fontStyle{bold: true}, want: TextStyleBold},
		{st: fontStyle{italic: true}, want: TextStyleItalic},
		{st: fontStyle{bold: true, italic: true}, want: TextStyleBoldItalic},
	} {
		if got := tc.st.String(); got != tc.want {
			t.Errorf("fontStyle%+v String() = %q, want %q", tc.st, got, tc.want)
		}
	}
}

func TestEmbeddedFontFamilies(t *testing.T) {
	for _, name := range []string{"", "go", "go-mono"} {
		for _, st := range []fontStyle{{}, {bold: true}, {italic: true}, {bold: true, italic: true}} {
			face, synth, err := loadStyledFace(name, st, 40)
			if err != nil {
				t.Fatalf("loadStyledFace(%q, %s): %v", name, st, err)
			}
			if !synth.isPlain() {
				t.Errorf("loadStyledFace(%q, %s): embedded fonts must not synthesize, got synth=%s", name, st, synth)
			}
			if w := measureString(face, "Test"); w <= 0 {
				t.Errorf("loadStyledFace(%q, %s): face cannot measure text", name, st)
			}
		}
	}
	names := EmbeddedFonts()
	if len(names) != 2 || names[0] != "go" || names[1] != "go-mono" {
		t.Errorf("EmbeddedFonts() = %v, want [go go-mono]", names)
	}
}

// TestCustomFontSynthesis verifies that a TTF file loaded as a custom
// font reports the requested style for synthetic application, while the
// embedded families resolve real variants instead.
func TestCustomFontSynthesis(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.ttf")
	if err := os.WriteFile(path, goregular.TTF, 0o644); err != nil {
		t.Fatal(err)
	}
	face, synth, err := loadStyledFace(path, fontStyle{bold: true, italic: true}, 40)
	if err != nil {
		t.Fatalf("loadStyledFace custom: %v", err)
	}
	if synth != (fontStyle{bold: true, italic: true}) {
		t.Errorf("custom font synth = %+v, want bold+italic", synth)
	}
	plain := measureStyledString(face, "Bold", fontStyle{})
	boldItalic := measureStyledString(face, "Bold", synth)
	if boldItalic <= plain {
		t.Errorf("synthetic bold+italic width %g should exceed plain width %g", boldItalic, plain)
	}
}

func TestMissingFontError(t *testing.T) {
	_, _, err := loadStyledFace("/nonexistent/ql570-missing.ttf", fontStyle{}, 10)
	if err == nil {
		t.Fatal("expected error for a missing font file")
	}
}
