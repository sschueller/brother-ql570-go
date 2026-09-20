package ipp

import (
	"testing"

	"github.com/sschueller/brother-ql570-go/pkg/ql"
)

func TestBuildMediaCatalog(t *testing.T) {
	catalog := BuildMediaCatalog()

	// 6 continuous widths x 4 lengths + 9 die-cut + 3 round die-cut = 36,
	// minus the 29x90 and 38x90 continuous variants that collide with the
	// die-cut labels of the same dimensions = 34.
	if len(catalog) != 34 {
		t.Fatalf("catalog has %d entries, want 34", len(catalog))
	}

	// Names must be unique.
	seen := map[string]bool{}
	for _, o := range catalog {
		if o.Name == "" {
			t.Fatal("empty media name")
		}
		if seen[o.Name] {
			t.Fatalf("duplicate media name %q", o.Name)
		}
		seen[o.Name] = true
	}
}

func TestLookupIPPMedia(t *testing.T) {
	o, err := LookupIPPMedia(2900, 6200)
	if err != nil {
		t.Fatalf("LookupIPPMedia(2900, 6200): %v", err)
	}
	if o.Name != "oem_ql570-29x62mm" {
		t.Fatalf("name = %q, want oem_ql570-29x62mm", o.Name)
	}
	if o.Media.ID != "29" || o.LengthMM != 62 {
		t.Fatalf("media = %q length = %g, want 29/62", o.Media.ID, o.LengthMM)
	}

	// Die-cut: length is fixed by the media, LengthMM is 0.
	o, err = LookupIPPMedia(6200, 10000)
	if err != nil {
		t.Fatalf("LookupIPPMedia(6200, 10000): %v", err)
	}
	if o.Media.ID != "62x100" || o.LengthMM != 0 {
		t.Fatalf("media = %q length = %g, want 62x100/0", o.Media.ID, o.LengthMM)
	}

	// 29x90 exists as both a die-cut label and a continuous length; the
	// die-cut entry must win so the mapping stays unambiguous.
	o, err = LookupIPPMedia(2900, 9000)
	if err != nil {
		t.Fatalf("LookupIPPMedia(2900, 9000): %v", err)
	}
	if o.Media.ID != "29x90" || o.Media.FormFactor != ql.DieCut {
		t.Fatalf("29x90 resolved to %q (%s), want die-cut 29x90", o.Media.ID, o.Media.FormFactor)
	}

	// Round die-cut.
	if o, err = LookupIPPMedia(1200, 1200); err != nil || o.Media.ID != "d12" {
		t.Fatalf("LookupIPPMedia(1200, 1200) = %v, %v; want d12", o, err)
	}

	if _, err := LookupIPPMedia(21000, 29700); err == nil {
		t.Fatal("A4 dimensions unexpectedly matched")
	}
	if _, err := LookupIPPMedia(0, 0); err == nil {
		t.Fatal("0x0 unexpectedly matched")
	}
}

func TestLookupIPPMediaByName(t *testing.T) {
	for _, o := range BuildMediaCatalog() {
		got, err := LookupIPPMediaByName(o.Name)
		if err != nil {
			t.Fatalf("LookupIPPMediaByName(%q): %v", o.Name, err)
		}
		if got != o {
			t.Fatalf("LookupIPPMediaByName(%q) = %+v, want %+v", o.Name, got, o)
		}
	}
	if _, err := LookupIPPMediaByName("iso_a4_210x297mm"); err == nil {
		t.Fatal("unknown name unexpectedly matched")
	}
}

func TestDefaultMediaOption(t *testing.T) {
	o := DefaultMediaOption()
	if o.Name != "om_card_54x86mm" {
		t.Fatalf("default media = %q, want om_card_54x86mm", o.Name)
	}
	if !o.Standard {
		t.Fatal("default media is not a standard size")
	}
}

func TestStandardMediaLookup(t *testing.T) {
	o, err := LookupIPPMedia(5400, 8600)
	if err != nil {
		t.Fatalf("LookupIPPMedia(5400, 8600): %v", err)
	}
	if !o.Standard || o.Name != "om_card_54x86mm" {
		t.Fatalf("got %+v, want standard om_card_54x86mm", o)
	}
	o, err = LookupIPPMediaByName("na_index-4x6_4x6in")
	if err != nil || !o.Standard {
		t.Fatalf("LookupIPPMediaByName(na_index-4x6_4x6in) = %+v, %v", o, err)
	}
	// A4 is still not offered.
	if _, err := LookupIPPMedia(21000, 29700); err == nil {
		t.Fatal("A4 unexpectedly matched")
	}
}

func TestResolveMedia(t *testing.T) {
	// Standard size on 62 mm continuous tape: auto-fit (LengthMM 0) so
	// the label grows with the content.
	o, err := LookupIPPMedia(5400, 8600)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveMedia(o, &ql.Status{MediaType: ql.MediaTypeContinuous, MediaWidthMM: 62})
	if err != nil {
		t.Fatalf("ResolveMedia(62mm continuous): %v", err)
	}
	if resolved.Media.ID != "62" || resolved.LengthMM != 0 || resolved.Standard {
		t.Fatalf("resolved to %+v, want 62mm auto-fit continuous", resolved)
	}
	// The standard name is kept for media compatibility checks.
	if resolved.Name != "om_card_54x86mm" {
		t.Fatalf("resolved name = %q, want om_card_54x86mm", resolved.Name)
	}

	// Same standard size on 29 mm tape.
	resolved, err = ResolveMedia(o, &ql.Status{MediaType: ql.MediaTypeContinuous, MediaWidthMM: 29})
	if err != nil {
		t.Fatalf("ResolveMedia(29mm continuous): %v", err)
	}
	if resolved.Media.ID != "29" || resolved.LengthMM != 0 {
		t.Fatalf("resolved to %+v, want 29mm auto-fit continuous", resolved)
	}

	// Die-cut loaded: the loaded label is used as-is.
	resolved, err = ResolveMedia(o, &ql.Status{MediaType: ql.MediaTypeDieCut, MediaWidthMM: 62, MediaLengthMM: 100})
	if err != nil {
		t.Fatalf("ResolveMedia(die-cut): %v", err)
	}
	if resolved.Media.ID != "62x100" || resolved.LengthMM != 0 {
		t.Fatalf("resolved to %+v, want 62x100 die-cut", resolved)
	}

	// No status: 62 mm continuous is assumed.
	resolved, err = ResolveMedia(o, nil)
	if err != nil {
		t.Fatalf("ResolveMedia(nil): %v", err)
	}
	if resolved.Media.ID != "62" || resolved.LengthMM != 0 {
		t.Fatalf("resolved to %+v, want 62mm auto-fit continuous", resolved)
	}

	// oem sizes pass through untouched.
	oem, err := LookupIPPMedia(6200, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err = ResolveMedia(oem, nil); err != nil || resolved != oem {
		t.Fatalf("oem option changed by ResolveMedia: %+v, %v", resolved, err)
	}
}

func TestMediaReady(t *testing.T) {
	st := &ql.Status{
		MediaWidthMM:  29,
		MediaLengthMM: 0,
		MediaType:     ql.MediaTypeContinuous,
	}
	// All continuous 29 mm entries of the catalog are ready (25, 40 and
	// 62 mm; the 90 mm variant is superseded by the die-cut 29x90).
	want := 0
	for _, o := range BuildMediaCatalog() {
		if o.Media.ID == "29" && o.Media.FormFactor == ql.Endless {
			want++
		}
	}
	ready := MediaReady(st)
	if len(ready) != want {
		t.Fatalf("ready media = %d entries, want %d", len(ready), want)
	}
	for _, o := range ready {
		if o.Media.ID != "29" {
			t.Fatalf("ready media %q, want width 29", o.Media.ID)
		}
	}

	// Die-cut 62x100 loaded.
	st = &ql.Status{MediaWidthMM: 62, MediaLengthMM: 100, MediaType: ql.MediaTypeDieCut}
	ready = MediaReady(st)
	if len(ready) != 1 || ready[0].Media.ID != "62x100" {
		t.Fatalf("die-cut ready = %+v, want exactly 62x100", ready)
	}

	// No media information.
	if got := MediaReady(&ql.Status{}); got != nil {
		t.Fatalf("empty status returned %+v, want nil", got)
	}
}
