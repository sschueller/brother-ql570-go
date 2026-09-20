package ql

import (
	"bytes"
	"testing"
)

func TestCmdDeviceSettings(t *testing.T) {
	cases := []struct {
		name string
		got  []byte
		want []byte
	}{
		// Sniffed from Brother's Printer Setting Tool (pklaus/brother_ql
		// issue #50): ESC i U A 00 {n} with n = 0..6.
		{"auto power off disabled", cmdSetAutoPowerOff(0), []byte{0x1B, 0x69, 0x55, 0x41, 0x00, 0x00}},
		{"auto power off 10 min", cmdSetAutoPowerOff(1), []byte{0x1B, 0x69, 0x55, 0x41, 0x00, 0x01}},
		{"auto power off 60 min", cmdSetAutoPowerOff(6), []byte{0x1B, 0x69, 0x55, 0x41, 0x00, 0x06}},
		{"auto power on disabled", cmdSetAutoPowerOn(false), []byte{0x1B, 0x69, 0x55, 0x70, 0x00, 0x00}},
		{"auto power on enabled", cmdSetAutoPowerOn(true), []byte{0x1B, 0x69, 0x55, 0x70, 0x00, 0x01}},
	}
	for _, tc := range cases {
		if !bytes.Equal(tc.got, tc.want) {
			t.Errorf("%s: got % X, want % X", tc.name, tc.got, tc.want)
		}
	}
}

func TestBuildSettingsStream(t *testing.T) {
	off := intPtr(0)
	on := boolPtr(true)
	stream, err := BuildSettingsStream(Settings{AutoPowerOffMinutes: off, AutoPowerOn: on})
	if err != nil {
		t.Fatalf("BuildSettingsStream: %v", err)
	}
	if len(stream) != QL570.InvalidateBytes+2+6+6 {
		t.Fatalf("stream length %d, want %d", len(stream), QL570.InvalidateBytes+14)
	}
	// 200 invalidate bytes + initialize.
	for i := 0; i < QL570.InvalidateBytes; i++ {
		if stream[i] != 0x00 {
			t.Fatalf("invalidate byte %d: got %02X want 00", i, stream[i])
		}
	}
	if !bytes.Equal(stream[200:202], []byte{0x1B, 0x40}) {
		t.Fatalf("initialize: got % X", stream[200:202])
	}
	// Auto power off command (0 minutes -> disabled).
	if !bytes.Equal(stream[202:208], []byte{0x1B, 0x69, 0x55, 0x41, 0x00, 0x00}) {
		t.Fatalf("auto power off: got % X", stream[202:208])
	}
	// Auto power on command (enabled).
	if !bytes.Equal(stream[208:214], []byte{0x1B, 0x69, 0x55, 0x70, 0x00, 0x01}) {
		t.Fatalf("auto power on: got % X", stream[208:214])
	}
}

func TestBuildSettingsStreamMinutesMapping(t *testing.T) {
	// 30 minutes maps to the printer's index value 3.
	stream, err := BuildSettingsStream(Settings{AutoPowerOffMinutes: intPtr(30)})
	if err != nil {
		t.Fatalf("BuildSettingsStream: %v", err)
	}
	want := []byte{0x1B, 0x69, 0x55, 0x41, 0x00, 0x03}
	if !bytes.Equal(stream[len(stream)-6:], want) {
		t.Errorf("auto power off 30 min: got % X, want % X", stream[len(stream)-6:], want)
	}
}

func TestBuildSettingsStreamErrors(t *testing.T) {
	if _, err := BuildSettingsStream(Settings{}); err == nil {
		t.Error("expected error for empty settings")
	}
	if _, err := BuildSettingsStream(Settings{AutoPowerOffMinutes: intPtr(15)}); err == nil {
		t.Error("expected error for 15 minutes (not a 10-minute step)")
	}
	if _, err := BuildSettingsStream(Settings{AutoPowerOffMinutes: intPtr(-10)}); err == nil {
		t.Error("expected error for negative minutes")
	}
}

func TestAutoPowerOffChoices(t *testing.T) {
	for _, c := range AutoPowerOffChoices {
		name, ok := AutoPowerOffName(c.Minutes)
		if !ok || name != c.Name {
			t.Errorf("AutoPowerOffName(%d) = %q, %v; want %q, true", c.Minutes, name, ok, c.Name)
		}
	}
	if _, ok := AutoPowerOffName(25); ok {
		t.Error("AutoPowerOffName(25) must not be supported")
	}
}

func intPtr(v int) *int    { return &v }
func boolPtr(v bool) *bool { return &v }
