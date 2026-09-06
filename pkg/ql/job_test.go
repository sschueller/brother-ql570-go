package ql

import (
	"encoding/json"
	"testing"
)

func validJob() *Job {
	return &Job{
		Text:     []string{"hello"},
		LengthMM: 40,
	}
}

func TestJobDefaults(t *testing.T) {
	j := validJob()
	j.DefaultJobValues()
	if j.Media != DefaultMediaID {
		t.Errorf("default media: %q", j.Media)
	}
	if j.Copies != 1 || j.CutEvery != 1 {
		t.Errorf("copies/cut_every defaults: %d/%d", j.Copies, j.CutEvery)
	}
	if j.FontSize != 10 {
		t.Errorf("font size default: %g", j.FontSize)
	}
	if !j.AutoCut() {
		t.Error("auto cut should default to true")
	}
	if !j.QualityPriority() {
		t.Error("quality should default to true")
	}
	if j.Align != AlignLeft || j.Compress != CompressNone {
		t.Errorf("align/compress defaults: %q/%q", j.Align, j.Compress)
	}
}

func TestJobValidateContinuous(t *testing.T) {
	j := validJob()
	j.DefaultJobValues()
	media, err := j.Validate()
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if media.ID != "29" || media.FormFactor != Endless {
		t.Errorf("resolved media: %+v", media)
	}
	if j.LengthDots(media) != MMToDots(40) {
		t.Errorf("length dots: %d", j.LengthDots(media))
	}
}

func TestJobValidateContinuousAutoLength(t *testing.T) {
	// Continuous media without an explicit length is valid: the length
	// auto-fits the content.
	j := validJob()
	j.LengthMM = 0
	j.DefaultJobValues()
	media, err := j.Validate()
	if err != nil {
		t.Fatalf("continuous media without length should be valid (auto-fit): %v", err)
	}
	if media.FormFactor != Endless {
		t.Errorf("media: %+v", media)
	}
}

func TestJobValidateLengthBounds(t *testing.T) {
	for _, mm := range []float64{1, 12.6, 1000.1, 5000} {
		j := validJob()
		j.LengthMM = mm
		if _, err := j.Validate(); err == nil {
			t.Errorf("expected error for length %.1f", mm)
		}
	}
	j := validJob()
	j.LengthMM = 12.7
	j.DefaultJobValues()
	if _, err := j.Validate(); err != nil {
		t.Errorf("12.7mm should be valid: %v", err)
	}
}

func TestJobValidateDieCut(t *testing.T) {
	j := validJob()
	j.Media = "62x100"
	j.LengthMM = 50 // must be rejected for die-cut
	j.DefaultJobValues()
	if _, err := j.Validate(); err == nil {
		t.Error("expected error when length is set for die-cut media")
	}
	j.LengthMM = 0
	media, err := j.Validate()
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if j.LengthDots(media) != 1109 {
		t.Errorf("die-cut length dots: %d", j.LengthDots(media))
	}
}

func TestJobValidateCable(t *testing.T) {
	// Cable wrap labels need continuous media.
	j := validJob()
	j.Media = "62x100"
	j.LengthMM = 0
	j.Cable = true
	j.DefaultJobValues()
	if _, err := j.Validate(); err == nil {
		t.Error("expected error for cable label on die-cut media")
	}

	j = validJob()
	j.LengthMM = 0
	j.Cable = true
	j.DefaultJobValues()
	if _, err := j.Validate(); err != nil {
		t.Errorf("cable label on continuous media should be valid: %v", err)
	}
	if j.CableFactor != DefaultCableFactor {
		t.Errorf("cable factor default: %g", j.CableFactor)
	}

	// Out-of-range factor.
	j.CableFactor = 0.5
	if _, err := j.Validate(); err == nil {
		t.Error("expected error for cable_factor 0.5")
	}
	j.CableFactor = 42
	if _, err := j.Validate(); err == nil {
		t.Error("expected error for cable_factor 42")
	}
}

func TestJobValidateOptions(t *testing.T) {
	bad := []struct {
		name string
		mut  func(*Job)
	}{
		{"copies 0", func(j *Job) { j.Copies = 0 }},
		{"copies 256", func(j *Job) { j.Copies = 256 }},
		{"cut every 0", func(j *Job) { j.CutEvery = 0 }},
		{"rotate 45", func(j *Job) { j.Rotate = 45 }},
		{"align bogus", func(j *Job) { j.Align = "middle" }},
		{"compress tiff", func(j *Job) { j.Compress = CompressTIFF }},
		{"threshold 101", func(j *Job) { j.Threshold = 101 }},
		{"font size negative", func(j *Job) { j.FontSize = -1 }},
		{"no content", func(j *Job) { j.Text = nil; j.QR = ""; j.Barcode = ""; j.Image = "" }},
	}
	for _, tc := range bad {
		j := validJob()
		j.DefaultJobValues()
		tc.mut(j)
		if _, err := j.Validate(); err == nil {
			t.Errorf("%s: expected error", tc.name)
		}
	}
}

func TestJobJSON(t *testing.T) {
	j := validJob()
	j.DefaultJobValues()
	data, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	var back Job
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Text[0] != "hello" || back.LengthMM != 40 {
		t.Errorf("JSON round trip: %+v", back)
	}
}

func TestParseJobsSingle(t *testing.T) {
	jobs, err := ParseJobs([]byte(`{"text":["hello"],"length_mm":40}`))
	if err != nil {
		t.Fatalf("ParseJobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Text[0] != "hello" || jobs[0].LengthMM != 40 {
		t.Errorf("unexpected jobs: %+v", jobs)
	}
}

func TestParseJobsArray(t *testing.T) {
	data := []byte(`[
		{"text":["SW-01"],"length_mm":30},
		{"text":["SW-02"],"length_mm":30},
		{"barcode":"ABC123","length_mm":40}
	]`)
	jobs, err := ParseJobs(data)
	if err != nil {
		t.Fatalf("ParseJobs: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("got %d jobs, want 3", len(jobs))
	}
	if jobs[1].Text[0] != "SW-02" || jobs[2].Barcode != "ABC123" {
		t.Errorf("unexpected jobs: %+v", jobs)
	}
}

func TestParseJobsErrors(t *testing.T) {
	for name, data := range map[string]string{
		"empty":       "",
		"whitespace":  "   \n\t ",
		"empty array": "[]",
		"invalid":     `{"text":`,
		"not json":    "hello",
	} {
		if _, err := ParseJobs([]byte(data)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
