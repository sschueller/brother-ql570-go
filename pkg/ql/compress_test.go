package ql

import (
	"bytes"
	"testing"
)

func TestPackBitsOfficialExample(t *testing.T) {
	// Example from the official Command Reference, section 5
	// "Compression mode selection".
	enc, ok := PackBitsEncode(packBitsExampleRaw)
	if !ok {
		t.Fatal("compressed result should be smaller than the raw input")
	}
	if err := ensurePackBitsExampleIsCorrect(enc); err != nil {
		t.Fatalf("official example mismatch: %v", err)
	}
	dec, err := PackBitsDecode(enc)
	if err != nil {
		t.Fatalf("PackBitsDecode: %v", err)
	}
	if !bytes.Equal(dec, packBitsExampleRaw) {
		t.Errorf("round trip mismatch:\ngot  % X\nwant % X", dec, packBitsExampleRaw)
	}
}

func TestPackBitsRoundTrip(t *testing.T) {
	// A 90-byte raster row as the printer would see it.
	cases := [][]byte{
		bytes.Repeat([]byte{0x00}, 90),
		bytes.Repeat([]byte{0xFF}, 90),
		{0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F},
		{0xAA, 0xAA, 0xAA, 0xAA, 0xBB, 0xBB, 0xBB},
	}
	for i, raw := range cases {
		enc, ok := PackBitsEncode(raw)
		if !ok {
			t.Fatalf("case %d: encoding did not shrink input", i)
		}
		dec, err := PackBitsDecode(enc)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if !bytes.Equal(dec, raw) {
			t.Errorf("case %d round trip mismatch", i)
		}
	}
}

func TestPackBitsIncompressible(t *testing.T) {
	// Alternating data does not compress; the encoder must report it so
	// the caller transmits the raw line.
	raw := make([]byte, 90)
	for i := range raw {
		raw[i] = byte(i * 3 & 0xFF)
	}
	_, ok := PackBitsEncode(raw)
	if ok {
		t.Error("alternating data should not be reported as smaller")
	}
}

func TestPackBitsLongRun(t *testing.T) {
	// 200 identical bytes need two run entries (128 + 72).
	raw := bytes.Repeat([]byte{0x11}, 200)
	enc, ok := PackBitsEncode(raw)
	if !ok {
		t.Fatal("long run should compress")
	}
	if len(enc) != 4 {
		t.Fatalf("expected 4 encoded bytes, got % X", enc)
	}
	if enc[0] != 0x81 || enc[1] != 0x11 || enc[2] != 0xB9 || enc[3] != 0x11 {
		t.Fatalf("unexpected encoding: % X", enc)
	}
	dec, err := PackBitsDecode(enc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dec, raw) {
		t.Error("long run round trip mismatch")
	}
}
