//go:build !linux

package ql

import (
	"errors"
	"fmt"
	"time"
)

// USBBackend is a placeholder for platforms without a direct-USB backend
// (macOS, Windows, ...). Direct USB printing is not supported here; run
// `ql570 serve` on a Linux or Android host attached to the printer and
// submit jobs over HTTP instead.
type USBBackend struct{}

// OpenUSB is unavailable on this platform.
func OpenUSB(device string) (*USBBackend, error) {
	return nil, errors.New("direct USB printing is only supported on Linux and Android; use `ql570 serve` on a host attached to the printer")
}

// DiscoverUSB is unavailable on this platform.
func DiscoverUSB() ([]USBDevice, error) {
	return nil, errors.New("direct USB printing is only supported on Linux and Android; use `ql570 serve` on a host attached to the printer")
}

// USBDevice mirrors the Linux backend's device description.
type USBDevice struct {
	Path      string `json:"path"`
	VendorID  string `json:"vendor_id"`
	ProductID string `json:"product_id"`
	Model     string `json:"model"`
	IsQL570   bool   `json:"is_ql570"`
}

func (b *USBBackend) Read(p []byte) (int, error) {
	return 0, errors.New("no USB backend on this platform")
}
func (b *USBBackend) Write(p []byte) (int, error) {
	return 0, errors.New("no USB backend on this platform")
}
func (b *USBBackend) Close() error { return nil }
func (b *USBBackend) Reopen() error {
	return errors.New("no USB backend on this platform")
}
func (b *USBBackend) Path() string   { return "" }
func (b *USBBackend) String() string { return fmt.Sprintf("unsupported") }
func (b *USBBackend) SetReadDeadline(t time.Time) error {
	return errors.New("no USB backend on this platform")
}
