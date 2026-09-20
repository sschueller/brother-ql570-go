package ql

import (
	"context"
	"fmt"
	"time"
)

// Device settings are configuration values stored in the printer's
// non-volatile memory (auto power off, auto power on, ...). The official
// raster command reference does not document them; the commands below were
// reverse-engineered from USB captures of Brother's "Printer Setting Tool"
// (see pklaus/brother_ql issue #50) and are confirmed to work on QL-570,
// QL-700 and QL-800 models.
//
// All device settings are write-only: no command to read the current
// values back is known.

// Device setting sub-commands of the "ESC i U" command family
// (ESC i U {setting} {n1} {n2}); the 16-bit value is big-endian.
const (
	settingAutoPowerOff byte = 'A' // auto power off after idle: 0=none, 1..6=10..60 min
	settingAutoPowerOn  byte = 'p' // auto power on when power is connected: 0=off, 1=on
)

// Settings describes device settings to apply to the printer. Nil fields
// leave the corresponding setting unchanged.
type Settings struct {
	// AutoPowerOffMinutes is the idle time after which the printer turns
	// itself off: 0 disables auto power off, otherwise one of 10, 20, 30,
	// 40, 50 or 60 minutes.
	AutoPowerOffMinutes *int
	// AutoPowerOn enables or disables the automatic power on when the
	// power cord is plugged in.
	AutoPowerOn *bool
}

// AutoPowerOffChoice is one supported auto power off value.
type AutoPowerOffChoice struct {
	// Minutes is the idle time before the printer turns itself off
	// (0 = disabled).
	Minutes int
	// Name is the human-readable name of the choice.
	Name string
}

// AutoPowerOffChoices lists the supported auto power off values in the
// order accepted by the printer.
var AutoPowerOffChoices = []AutoPowerOffChoice{
	{Minutes: 0, Name: "disabled (never power off)"},
	{Minutes: 10, Name: "10 minutes"},
	{Minutes: 20, Name: "20 minutes"},
	{Minutes: 30, Name: "30 minutes"},
	{Minutes: 40, Name: "40 minutes"},
	{Minutes: 50, Name: "50 minutes"},
	{Minutes: 60, Name: "60 minutes"},
}

// AutoPowerOffName returns the human-readable name for the given idle time
// in minutes and reports whether it is a supported value.
func AutoPowerOffName(minutes int) (string, bool) {
	for _, c := range AutoPowerOffChoices {
		if c.Minutes == minutes {
			return c.Name, true
		}
	}
	return "", false
}

// cmdSetAutoPowerOff returns the "auto power off" device setting command
// (ESC i U A {n1} {n2}). The value is the index of the idle time:
// 0 = never, 1 = 10 minutes, ..., 6 = 60 minutes.
func cmdSetAutoPowerOff(idx int) []byte {
	return []byte{esc, 'i', 'U', settingAutoPowerOff, byte(idx >> 8), byte(idx & 0xFF)}
}

// cmdSetAutoPowerOn returns the "auto power on" device setting command
// (ESC i U p {n1} {n2}): value 0 = disabled, 1 = enabled.
func cmdSetAutoPowerOn(on bool) []byte {
	v := byte(0)
	if on {
		v = 1
	}
	return []byte{esc, 'i', 'U', settingAutoPowerOn, 0x00, v}
}

// Validate checks the settings and returns an error for unsupported
// values or when there is nothing to apply.
func (s Settings) Validate() error {
	if s.AutoPowerOffMinutes == nil && s.AutoPowerOn == nil {
		return fmt.Errorf("no device settings to apply")
	}
	if s.AutoPowerOffMinutes != nil {
		if _, ok := AutoPowerOffName(*s.AutoPowerOffMinutes); !ok {
			return fmt.Errorf("auto power off must be 0 (disabled) or one of 10, 20, 30, 40, 50, 60 minutes, got %d", *s.AutoPowerOffMinutes)
		}
	}
	return nil
}

// BuildSettingsStream assembles the command stream that applies the given
// device settings: invalidate, initialize, then one "ESC i U" command per
// set field.
func BuildSettingsStream(s Settings) ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	buf := make([]byte, 0, QL570.InvalidateBytes+16)
	buf = append(buf, cmdInvalidate(QL570.InvalidateBytes)...)
	buf = append(buf, cmdInitialize()...)
	if s.AutoPowerOffMinutes != nil {
		buf = append(buf, cmdSetAutoPowerOff(*s.AutoPowerOffMinutes/10)...)
	}
	if s.AutoPowerOn != nil {
		buf = append(buf, cmdSetAutoPowerOn(*s.AutoPowerOn)...)
	}
	return buf, nil
}

// Configure applies device settings to the printer. The settings are
// stored in the printer's non-volatile memory and survive power cycles;
// there is no command to read them back.
func (p *Printer) Configure(ctx context.Context, s Settings) error {
	stream, err := BuildSettingsStream(s)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, err := writeAll(p.backend, stream); err != nil {
		p.resetUSB()
		return fmt.Errorf("writing settings to printer: %w", err)
	}
	if _, err := p.statusLocked(ctx, 3*time.Second); err != nil {
		return fmt.Errorf("verifying printer after settings update: %w", err)
	}
	return nil
}
