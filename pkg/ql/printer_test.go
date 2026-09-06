package ql

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestCheckMediaMatch(t *testing.T) {
	endless29, _ := LookupMedia("29")
	dieCut62100, _ := LookupMedia("62x100")

	cases := []struct {
		name    string
		media   Media
		status  *Status
		wantErr string
	}{
		{"endless matches", endless29, &Status{MediaType: MediaTypeContinuous, MediaWidthMM: 29}, ""},
		{"die-cut matches", dieCut62100, &Status{MediaType: MediaTypeDieCut, MediaWidthMM: 62, MediaLengthMM: 100}, ""},
		{"no media info", endless29, &Status{MediaType: MediaTypeNone}, ""},
		{"type mismatch", dieCut62100, &Status{MediaType: MediaTypeContinuous, MediaWidthMM: 29}, "continuous"},
		{"width mismatch", endless29, &Status{MediaType: MediaTypeContinuous, MediaWidthMM: 62}, "62 mm"},
		{"die-cut length mismatch", dieCut62100, &Status{MediaType: MediaTypeDieCut, MediaWidthMM: 62, MediaLengthMM: 29}, "29 mm media"},
	}
	for _, tc := range cases {
		err := checkMediaMatch(tc.status, tc.media)
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected error: %v", tc.name, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: expected error containing %q", tc.name, tc.wantErr)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: error %q does not contain %q", tc.name, err, tc.wantErr)
		}
	}
}

// fakeBackend is a scripted Backend for tests: every Read returns the next
// scripted response (or a timeout when the script is exhausted), and every
// Write is recorded as a separate call.
type fakeBackend struct {
	mu        sync.Mutex
	writes    [][]byte
	responses [][]byte
}

func (f *fakeBackend) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.responses) == 0 {
		return 0, os.ErrDeadlineExceeded
	}
	r := f.responses[0]
	f.responses = f.responses[1:]
	return copy(p, r), nil
}

func (f *fakeBackend) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, append([]byte(nil), p...))
	return len(p), nil
}

func (f *fakeBackend) Close() error { return nil }

// makeStatus builds a 32-byte status response with the given fields.
func makeStatus(statusType, phaseType byte, mediaType byte, widthMM, lengthMM int) []byte {
	raw := make([]byte, 32)
	raw[0], raw[1], raw[2] = 0x80, 0x20, 0x42
	raw[3], raw[4] = 0x34, 0x32 // QL-570 model code
	raw[5] = 0x30
	raw[10] = byte(widthMM)
	raw[11] = mediaType
	raw[17] = byte(lengthMM)
	raw[18] = statusType
	raw[19] = phaseType
	return raw
}

// TestPrintJobsEndToEnd runs a two-label batch through the whole
// Print/PrintJobs path against a scripted backend and checks the produced
// command stream.
func TestPrintJobsEndToEnd(t *testing.T) {
	ok29 := makeStatus(StatusTypeReply, PhaseTypeWaitingToReceive, MediaTypeContinuous, 29, 0)
	fb := &fakeBackend{responses: [][]byte{
		ok29, // pre-flight status check
		ok29, // status request inside the job stream
		makeStatus(StatusTypePhaseChange, PhaseTypePrinting, MediaTypeContinuous, 29, 0),
		makeStatus(StatusTypePrintingCompleted, PhaseTypePrinting, MediaTypeContinuous, 29, 0),
		makeStatus(StatusTypePhaseChange, PhaseTypeWaitingToReceive, MediaTypeContinuous, 29, 0),
	}}
	p := NewPrinter(fb)
	jobs := []Job{
		{Text: []string{"SW-01"}, LengthMM: 30},
		{Text: []string{"SW-02"}, LengthMM: 40},
	}
	res, err := p.PrintJobs(context.Background(), jobs)
	if err != nil {
		t.Fatalf("PrintJobs: %v", err)
	}
	if !res.Printed || !res.Ready {
		t.Errorf("printed=%v ready=%v", res.Printed, res.Ready)
	}
	if len(fb.writes) != 2 {
		t.Fatalf("expected 2 write calls (pre-flight + stream), got %d", len(fb.writes))
	}
	stream := fb.writes[1]
	// One print-information command per page.
	if n := bytes.Count(stream, []byte{0x1B, 0x69, 0x7A}); n != 2 {
		t.Errorf("expected 2 print-information commands, got %d", n)
	}
	if stream[len(stream)-1] != 0x1A {
		t.Errorf("stream must end with 1A, got %02X", stream[len(stream)-1])
	}
	// The second page's starting-page flag must be 1.
	first := bytes.Index(stream, []byte{0x1B, 0x69, 0x7A})
	second := bytes.Index(stream[first+3:], []byte{0x1B, 0x69, 0x7A}) + first + 3
	if stream[second+3+8] != 0x01 {
		t.Errorf("second page starting-page byte: got %02X want 01", stream[second+3+8])
	}
}

// TestPrintJobsMediaMismatch verifies that a batch mixing two media sizes
// is rejected before anything is written to the printer.
func TestPrintJobsMediaMismatch(t *testing.T) {
	ok29 := makeStatus(StatusTypeReply, PhaseTypeWaitingToReceive, MediaTypeContinuous, 29, 0)
	fb := &fakeBackend{responses: [][]byte{ok29}}
	p := NewPrinter(fb)
	jobs := []Job{
		{Text: []string{"a"}, LengthMM: 30},
		{Text: []string{"b"}, Media: "62x100"},
	}
	if _, err := p.PrintJobs(context.Background(), jobs); err == nil {
		t.Fatal("expected error for mixed media in one batch")
	} else if !strings.Contains(err.Error(), "same media") {
		t.Errorf("unexpected error: %v", err)
	}
	if len(fb.writes) != 0 {
		t.Errorf("no data may be sent for an invalid batch, got %d writes", len(fb.writes))
	}
}

// TestPrintJobsEmpty verifies the empty-batch guard.
func TestPrintJobsEmpty(t *testing.T) {
	p := NewPrinter(&fakeBackend{})
	if _, err := p.PrintJobs(context.Background(), nil); err == nil {
		t.Error("expected error for empty job list")
	}
}

// TestBuildJobsBytes verifies the dry-run path for a batch.
func TestBuildJobsBytes(t *testing.T) {
	jobs := []Job{
		{Text: []string{"a"}, LengthMM: 30},
		{Text: []string{"b"}, LengthMM: 30, Copies: 2},
	}
	stream, medias, err := BuildJobsBytes(jobs)
	if err != nil {
		t.Fatal(err)
	}
	if len(medias) != 2 || medias[0].ID != "29" {
		t.Errorf("medias: %+v", medias)
	}
	// 3 pages: job 1 once, job 2 twice.
	if n := bytes.Count(stream, []byte{0x1B, 0x69, 0x7A}); n != 3 {
		t.Errorf("expected 3 pages (3 print-info commands), got %d", n)
	}
	if stream[len(stream)-1] != 0x1A {
		t.Errorf("stream must end with 1A")
	}
}
