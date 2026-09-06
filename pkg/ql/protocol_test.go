package ql

import (
	"bytes"
	"testing"
)

func TestCommandBytes(t *testing.T) {
	cases := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"initialize", cmdInitialize(), []byte{0x1B, 0x40}},
		{"status request", cmdStatusRequest(), []byte{0x1B, 0x69, 0x53}},
		{"set each mode auto cut on", cmdSetEachMode(true), []byte{0x1B, 0x69, 0x4D, 0x40}},
		{"set each mode auto cut off", cmdSetEachMode(false), []byte{0x1B, 0x69, 0x4D, 0x00}},
		{"cut every 1", cmdCutEvery(1), []byte{0x1B, 0x69, 0x41, 0x01}},
		{"cut every 255", cmdCutEvery(255), []byte{0x1B, 0x69, 0x41, 0xFF}},
		{"expanded cut at end", cmdSetExpandedMode(true, false), []byte{0x1B, 0x69, 0x4B, 0x08}},
		{"expanded hires", cmdSetExpandedMode(true, true), []byte{0x1B, 0x69, 0x4B, 0x48}},
		{"margin 35", cmdSetMargin(35), []byte{0x1B, 0x69, 0x64, 0x23, 0x00}},
		{"margin 1500", cmdSetMargin(1500), []byte{0x1B, 0x69, 0x64, 0xDC, 0x05}},
		{"compression none", cmdSelectCompression(false), []byte{0x4D, 0x00}},
		{"compression tiff", cmdSelectCompression(true), []byte{0x4D, 0x02}},
		{"zero raster", cmdZeroRaster(), []byte{0x5A}},
		{"print intermediate", cmdPrint(true), []byte{0x0C}},
		{"print with feeding", cmdPrint(false), []byte{0x1A}},
	}
	for _, tc := range cases {
		if !bytes.Equal(tc.got, tc.want) {
			t.Errorf("%s: got % X, want % X", tc.name, tc.got, tc.want)
		}
	}
}

func TestCmdRasterRow(t *testing.T) {
	row := []byte{0x00, 0x0F, 0xFF}
	got := cmdRasterRow(row)
	want := []byte{0x67, 0x00, 0x03, 0x00, 0x0F, 0xFF}
	if !bytes.Equal(got, want) {
		t.Errorf("raster row: got % X, want % X", got, want)
	}
}

func TestCmdPrintInformation(t *testing.T) {
	// The official reference example: first page of 29x90mm die-cut
	// labels, 991 raster lines:
	// 1B 69 7A 0E 0B 1D 5A DF 03 00 00 00 00
	pi := PrintInfo{
		MediaType:    MediaTypeDieCut,
		WidthMM:      29,
		LengthMM:     90,
		RasterNumber: 991,
		StartingPage: 0,
	}
	got := cmdPrintInformation(pi)
	want := []byte{0x1B, 0x69, 0x7A, 0x8E, 0x0B, 0x1D, 0x5A, 0xDF, 0x03, 0x00, 0x00, 0x00, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("print information: got % X, want % X", got, want)
	}

	// Continuous tape: flags = 0x80 | 0x02 | 0x04 = 0x86 (length not
	// valid), width 62mm, length 0, raster number 0xD2 = 210.
	pi2 := PrintInfo{
		MediaType:    MediaTypeContinuous,
		WidthMM:      62,
		LengthMM:     0,
		RasterNumber: 210,
		StartingPage: 1,
	}
	got2 := cmdPrintInformation(pi2)
	want2 := []byte{0x1B, 0x69, 0x7A, 0x86, 0x0A, 0x3E, 0x00, 0xD2, 0x00, 0x00, 0x00, 0x01, 0x00}
	if !bytes.Equal(got2, want2) {
		t.Errorf("print information continuous: got % X, want % X", got2, want2)
	}
}

func TestBuildJobStream(t *testing.T) {
	rows := [][]byte{
		bytes.Repeat([]byte{0x00}, 90),
		bytes.Repeat([]byte{0xFF}, 90),
	}
	opts := PrintOptions{
		MediaType: MediaTypeContinuous,
		WidthMM:   29,
		Pages:     1,
		AutoCut:   true,
		CutEvery:  1,
		CutAtEnd:  true,
		Hires600:  false,
		FeedDots:  35,
		Quality:   true,
	}
	got := BuildJobStream(QL570, opts, rows)

	// Verify the expected structure piece by piece.
	wantStart := []byte{0x1B, 0x40} // after 200 invalid bytes
	if len(got) < 200 {
		t.Fatalf("stream too short: %d bytes", len(got))
	}
	for i := 0; i < 200; i++ {
		if got[i] != 0x00 {
			t.Fatalf("invalidate byte %d: got %02X want 00", i, got[i])
		}
	}
	if !bytes.Equal(got[200:202], wantStart) {
		t.Fatalf("initialize: got % X want % X", got[200:202], wantStart)
	}
	if !bytes.Equal(got[202:205], []byte{0x1B, 0x69, 0x53}) {
		t.Fatalf("status request: got % X", got[202:205])
	}
	// print info: 13 bytes
	if !bytes.Equal(got[205:208], []byte{0x1B, 0x69, 0x7A}) {
		t.Fatalf("print info header: got % X", got[205:208])
	}
	// 0xC6 = 0x80|0x02|0x04|0x40 (quality priority, no length)
	if got[208] != 0xC6 {
		t.Fatalf("print info flags: got %02X want C6", got[208])
	}
	if got[209] != MediaTypeContinuous || got[210] != 29 || got[211] != 0x00 {
		t.Fatalf("print info media: % X", got[209:212])
	}
	if !bytes.Equal(got[212:216], []byte{0x02, 0x00, 0x00, 0x00}) {
		t.Fatalf("raster number: got % X", got[212:216])
	}
	if got[216] != 0x00 || got[217] != 0x00 {
		t.Fatalf("starting page/zero: % X", got[216:218])
	}
	if !bytes.Equal(got[218:222], []byte{0x1B, 0x69, 0x4D, 0x40}) {
		t.Fatalf("set each mode: % X", got[218:222])
	}
	if !bytes.Equal(got[222:226], []byte{0x1B, 0x69, 0x41, 0x01}) {
		t.Fatalf("cut every: % X", got[222:226])
	}
	if !bytes.Equal(got[226:230], []byte{0x1B, 0x69, 0x4B, 0x08}) {
		t.Fatalf("expanded: % X", got[226:230])
	}
	if !bytes.Equal(got[230:235], []byte{0x1B, 0x69, 0x64, 0x23, 0x00}) {
		t.Fatalf("margin: % X", got[230:235])
	}
	// raster rows: 67 00 5A + 90 bytes each
	off := 235
	for i, row := range rows {
		if !bytes.Equal(got[off:off+3], []byte{0x67, 0x00, 0x5A}) {
			t.Fatalf("row %d header: % X", i, got[off:off+3])
		}
		if !bytes.Equal(got[off+3:off+93], row) {
			t.Fatalf("row %d data mismatch", i)
		}
		off += 93
	}
	if got[off] != 0x1A {
		t.Fatalf("final print command: got %02X want 1A", got[off])
	}
	if off+1 != len(got) {
		t.Fatalf("trailing bytes: %d", len(got)-off-1)
	}
}

func TestBuildJobStreamMultiPage(t *testing.T) {
	rows := [][]byte{make([]byte, 90)}
	opts := PrintOptions{
		MediaType: MediaTypeContinuous,
		WidthMM:   29,
		Pages:     3,
		AutoCut:   true,
		CutEvery:  1,
		CutAtEnd:  true,
		FeedDots:  35,
		Quality:   true,
	}
	got := BuildJobStream(QL570, opts, rows)
	if !bytes.Contains(got, []byte{0x0C}) {
		t.Errorf("expected intermediate print command 0C in multi-page job")
	}
	if got[len(got)-1] != 0x1A {
		t.Errorf("expected final print command 1A, got %02X", got[len(got)-1])
	}
	// Page 2's print information must have the starting-page byte set.
	// Page block size: statusReq(3) + printInfo(13) + modes(4+4+4) +
	// margin(5) + row(93) + print(1) = 127
	blockSize := 127
	if len(got) != 200+2+3*blockSize {
		t.Errorf("unexpected stream size %d", len(got))
	}
	secondPageInfo := got[200+2+blockSize+3+3+8]
	if secondPageInfo != 0x01 {
		t.Errorf("second page starting-page byte: got %02X want 01", secondPageInfo)
	}
}

func TestBuildJobStreamZeroRasterTIFF(t *testing.T) {
	nonZero := make([]byte, 90)
	nonZero[0], nonZero[1] = 0xFF, 0x00
	rows := [][]byte{make([]byte, 90), nonZero}
	opts := PrintOptions{
		MediaType: MediaTypeContinuous,
		WidthMM:   29,
		Pages:     1,
		AutoCut:   false,
		FeedDots:  35,
		TIFF:      true,
	}
	got := BuildJobStream(QL570, opts, rows)
	if !bytes.Contains(got, []byte{0x5A}) {
		t.Errorf("expected zero-raster command 5A for an all-zero row in TIFF mode")
	}
}

// TestBuildJobsStream verifies a batch of distinct pages: each page has its
// own print-information command (media/length/raster number, starting-page
// flag), intermediate pages end with 0C and the last page with 1A.
func TestBuildJobsStream(t *testing.T) {
	rowA := make([]byte, 90)
	rowA[0] = 0xFF
	rowB := make([]byte, 90)
	rowB[1] = 0x0F
	pages := []JobPage{
		{
			Opts: PrintOptions{MediaType: MediaTypeContinuous, WidthMM: 29, LengthMM: 0, AutoCut: true, CutEvery: 1, CutAtEnd: true, FeedDots: 35, Quality: true},
			Rows: [][]byte{rowA, rowA, rowA},
		},
		{
			Opts: PrintOptions{MediaType: MediaTypeContinuous, WidthMM: 29, LengthMM: 0, AutoCut: true, CutEvery: 1, CutAtEnd: true, FeedDots: 35, Quality: true},
			Rows: [][]byte{rowB},
		},
	}
	got := BuildJobsStream(QL570, pages)

	// Header: 200 invalid + initialize + page 1.
	if len(got) < 200 {
		t.Fatalf("stream too short: %d", len(got))
	}
	if !bytes.Equal(got[200:202], []byte{0x1B, 0x40}) {
		t.Fatalf("initialize: % X", got[200:202])
	}
	if !bytes.Equal(got[202:205], []byte{0x1B, 0x69, 0x53}) {
		t.Fatalf("page 1 status request: % X", got[202:205])
	}
	// Page 1 print info: flags C6, type 0A, width 1D, length 00,
	// raster number 3, starting page 0.
	pi1 := got[205:218]
	if !bytes.Equal(pi1, []byte{0x1B, 0x69, 0x7A, 0xC6, 0x0A, 0x1D, 0x00, 0x03, 0x00, 0x00, 0x00, 0x00, 0x00}) {
		t.Fatalf("page 1 print info: % X", pi1)
	}
	// Page 1 block: printInfo(13) + modes(4+4+4) + margin(5) + 3 rows (93 each) + print.
	block1 := 13 + 4 + 4 + 4 + 5 + 3*93 + 1
	off := 205 + block1
	if got[off-1] != 0x0C {
		t.Fatalf("page 1 must end with 0C, got %02X", got[off-1])
	}
	// Page 2 print info: raster number 1, starting page byte 1.
	if !bytes.Equal(got[off:off+3], []byte{0x1B, 0x69, 0x53}) {
		t.Fatalf("page 2 status request: % X", got[off:off+3])
	}
	pi2 := got[off+3 : off+16]
	if !bytes.Equal(pi2, []byte{0x1B, 0x69, 0x7A, 0xC6, 0x0A, 0x1D, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00}) {
		t.Fatalf("page 2 print info: % X", pi2)
	}
	block2 := 13 + 4 + 4 + 4 + 5 + 93 + 1
	off += 3 + block2
	if off != len(got) {
		t.Fatalf("stream length %d, expected %d", len(got), off)
	}
	if got[len(got)-1] != 0x1A {
		t.Fatalf("last page must end with 1A, got %02X", got[len(got)-1])
	}
	// Exactly one 0C (between the two pages).
	if n := bytes.Count(got, []byte{0x0C}); n != 1 {
		t.Errorf("expected exactly one 0C in the stream, got %d", n)
	}
}

// TestBuildJobsStreamDieCutPages verifies per-page media information for a
// batch of die-cut labels.
func TestBuildJobsStreamDieCutPages(t *testing.T) {
	rows := [][]byte{make([]byte, 90), make([]byte, 90)}
	opts := PrintOptions{MediaType: MediaTypeDieCut, WidthMM: 62, LengthMM: 100, AutoCut: true, CutEvery: 1, CutAtEnd: true, FeedDots: 0, Quality: true}
	pages := []JobPage{
		{Opts: opts, Rows: rows},
		{Opts: opts, Rows: rows},
	}
	got := BuildJobsStream(QL570, pages)
	// Page 1: flags CE (0x80|kind|width|length|quality), width 3E,
	// length 64, raster 2, starting page 0.
	want1 := []byte{0x1B, 0x69, 0x7A, 0xCE, 0x0B, 0x3E, 0x64, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00}
	if !bytes.Equal(got[205:218], want1) {
		t.Fatalf("die-cut page 1 print info: % X", got[205:218])
	}
}

func TestParseStatus(t *testing.T) {
	// A realistic 32-byte status: reply to status request, waiting to
	// receive, 29mm continuous tape loaded.
	raw := []byte{
		0x80, 0x20, 0x42, 0x34, 0x32, 0x30, 0x00, 0x00,
		0x00, 0x00, 0x1D, 0x0A, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}
	st, err := ParseStatus(raw)
	if err != nil {
		t.Fatalf("ParseStatus: %v", err)
	}
	if st.Model != "QL-570" {
		t.Errorf("model: got %q want QL-570", st.Model)
	}
	if st.MediaWidthMM != 29 {
		t.Errorf("media width: got %d want 29", st.MediaWidthMM)
	}
	if st.MediaType != MediaTypeContinuous {
		t.Errorf("media type: got %02X want 0A", st.MediaType)
	}
	if st.StatusType != StatusTypeReply || st.PhaseType != PhaseTypeWaitingToReceive {
		t.Errorf("status/phase: %02X/%02X", st.StatusType, st.PhaseType)
	}
	if st.HasErrors() {
		t.Errorf("unexpected errors: %v", st.Errors)
	}
}

func TestParseStatusErrors(t *testing.T) {
	raw := make([]byte, 32)
	raw[0], raw[1], raw[2] = 0x80, 0x20, 0x42
	raw[3], raw[4] = 0x34, 0x32
	raw[8] = 0x01 | 0x04 // no media + cutter jam
	raw[9] = 0x10        // cover opened
	raw[18] = StatusTypeError
	st, err := ParseStatus(raw)
	if err != nil {
		t.Fatalf("ParseStatus: %v", err)
	}
	if !st.HasErrors() {
		t.Fatal("expected errors")
	}
	found := map[string]bool{}
	for _, e := range st.Errors {
		found[e] = true
	}
	for _, want := range []string{"no media when printing", "tape cutter jam", "cover opened while printing"} {
		if !found[want] {
			t.Errorf("missing error %q in %v", want, st.Errors)
		}
	}
	if st.StatusTypeName != "error occurred" {
		t.Errorf("status type name: %q", st.StatusTypeName)
	}
}

func TestParseStatusBadInput(t *testing.T) {
	if _, err := ParseStatus([]byte{0x80, 0x20}); err == nil {
		t.Error("expected error for short status")
	}
	bad := make([]byte, 32)
	if _, err := ParseStatus(bad); err == nil {
		t.Error("expected error for missing header")
	}
}

func TestStatusString(t *testing.T) {
	raw := make([]byte, 32)
	raw[0], raw[1], raw[2] = 0x80, 0x20, 0x42
	raw[3], raw[4] = 0x34, 0x32
	raw[10], raw[11] = 62, MediaTypeContinuous
	raw[18], raw[19] = StatusTypePhaseChange, PhaseTypePrinting
	st, err := ParseStatus(raw)
	if err != nil {
		t.Fatalf("ParseStatus: %v", err)
	}
	s := st.String()
	for _, want := range []string{"QL-570", "continuous", "62mm", "phase change", "printing state"} {
		if !bytes.Contains([]byte(s), []byte(want)) {
			t.Errorf("String() %q missing %q", s, want)
		}
	}
}
