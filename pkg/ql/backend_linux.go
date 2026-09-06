//go:build linux && !android

package ql

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// USBBackend talks to a Brother QL printer through the Linux kernel usblp
// driver (/dev/usb/lpN). It mirrors the approach of brother_ql's
// linux_kernel backend: open O_RDWR, write instructions, poll-read 32-byte
// status responses.
//
// Reads use a select() poll loop with an explicit deadline instead of
// Go's runtime poller: the usblp driver returns 0/EOF for non-blocking
// reads on this device, which the Go poller does not handle well.
//
// Writes run with a timeout: if the printer stops consuming data (e.g.
// after a media-mismatch error it stalls its bulk endpoint), the device is
// closed to cancel the pending URBs and unblock the write, instead of
// hanging forever.
type USBBackend struct {
	f        *os.File
	path     string
	deadline time.Time
}

// writeTimeout bounds a single Write call. The largest possible job
// (11811 raster lines x 93 bytes ~= 1.1 MB) transfers in well under a
// second over USB bulk; 20 s is very generous.
const writeTimeout = 20 * time.Second

// OpenUSB opens the given usblp device (e.g. /dev/usb/lp0). If device is
// empty, a QL-570 attached to this machine is auto-discovered.
func OpenUSB(device string) (*USBBackend, error) {
	if device == "" {
		devs, err := DiscoverUSB()
		if err != nil {
			return nil, err
		}
		if len(devs) == 0 {
			return nil, fmt.Errorf("no Brother QL-570 found: no /dev/usb/lp* device with ID %04x:%04x (is the printer connected and switched on?)", VendorID, ProductID)
		}
		device = devs[0].Path
	}
	f, err := os.OpenFile(device, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w (is the printer switched on and is your user in the 'lp' group?)", device, err)
	}
	return &USBBackend{f: f, path: device}, nil
}

// Path returns the device path.
func (b *USBBackend) Path() string { return b.path }

// String returns a human-readable description of the backend.
func (b *USBBackend) String() string { return b.path }

// Read reads up to len(p) bytes from the printer, honoring the deadline
// set by SetReadDeadline. It polls with select() and returns
// os.ErrDeadlineExceeded if no data arrives in time.
func (b *USBBackend) Read(p []byte) (int, error) {
	for {
		remaining := time.Until(b.deadline)
		if remaining <= 0 {
			return 0, os.ErrDeadlineExceeded
		}
		tv := remaining
		if tv > 100*time.Millisecond {
			tv = 100 * time.Millisecond
		}
		ready, err := pollReadable(int(b.f.Fd()), tv)
		if err != nil {
			return 0, err
		}
		if !ready {
			continue
		}
		n, err := syscall.Read(int(b.f.Fd()), p)
		if err == syscall.EINTR || err == syscall.EAGAIN {
			continue
		}
		if n < 0 {
			n = 0
		}
		return n, err
	}
}

// pollReadable waits up to timeout for the file descriptor to become
// readable, returning whether it did.
func pollReadable(fd int, timeout time.Duration) (bool, error) {
	for {
		fds := &syscall.FdSet{}
		fds.Bits[fd/64] |= 1 << (uint(fd) % 64)
		n, err := syscall.Select(fd+1, fds, nil, nil, &syscall.Timeval{
			Sec:  int64(timeout / time.Second),
			Usec: int64(timeout % time.Second / time.Microsecond),
		})
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return false, err
		}
		return n > 0, nil
	}
}

// Write writes all of p to the printer, bounded by writeTimeout. On
// timeout the device is closed (cancelling pending URBs and unblocking
// the write); the backend is unusable afterwards until Reopen is called.
func (b *USBBackend) Write(p []byte) (int, error) {
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() {
		n, err := b.f.Write(p)
		done <- result{n, err}
	}()
	select {
	case r := <-done:
		return r.n, r.err
	case <-time.After(writeTimeout):
		_ = b.f.Close()
		select {
		case <-done:
			// The blocked write has been cancelled; reopen the device so
			// the backend remains usable for the next attempt.
			_ = b.Reopen()
		case <-time.After(2 * time.Second):
		}
		return 0, fmt.Errorf("write to %s timed out after %s (printer unresponsive)", b.path, writeTimeout)
	}
}

// Reopen closes the current device handle and opens the device again.
// Closing a usblp handle releases the USB interface, which clears endpoint
// stalls and error states; a fresh handle starts with a clean slate.
func (b *USBBackend) Reopen() error {
	if b.f != nil {
		_ = b.f.Close()
		b.f = nil
	}
	f, err := os.OpenFile(b.path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("reopening %s: %w", b.path, err)
	}
	b.f = f
	return nil
}

// Close closes the device.
func (b *USBBackend) Close() error { return b.f.Close() }

// SetReadDeadline bounds the next Read call in time.
func (b *USBBackend) SetReadDeadline(t time.Time) error {
	b.deadline = t
	return nil
}

// USBDevice describes one discovered USB printer.
type USBDevice struct {
	Path      string `json:"path"`
	VendorID  string `json:"vendor_id"`
	ProductID string `json:"product_id"`
	Model     string `json:"model"`
	IsQL570   bool   `json:"is_ql570"`
}

// DiscoverUSB lists all USB printer devices (/dev/usb/lp*) with their
// vendor/product IDs read from sysfs. QL-570 devices (04f9:2028) are
// sorted first, so callers can pick the first entry.
func DiscoverUSB() ([]USBDevice, error) {
	paths, err := filepath.Glob("/dev/usb/lp*")
	if err != nil {
		return nil, fmt.Errorf("listing /dev/usb/lp*: %w", err)
	}
	devs := make([]USBDevice, 0, len(paths))
	for _, p := range paths {
		d := USBDevice{Path: p}
		if id, err := readUSBID(p); err == nil {
			d.VendorID, d.ProductID = id.vendor, id.product
			if d.VendorID == fmt.Sprintf("%04x", VendorID) && d.ProductID == fmt.Sprintf("%04x", ProductID) {
				d.IsQL570 = true
				d.Model = "QL-570"
			}
		}
		devs = append(devs, d)
	}
	sort.SliceStable(devs, func(i, j int) bool {
		if devs[i].IsQL570 != devs[j].IsQL570 {
			return devs[i].IsQL570
		}
		return devs[i].Path < devs[j].Path
	})
	return devs, nil
}

type usbID struct{ vendor, product string }

// readUSBID resolves the vendor/product ID of a /dev/usb/lpN device
// through sysfs: /sys/class/usbmisc/lpN/device points to the USB interface
// directory; idVendor and idProduct live on the parent USB device
// directory (one level up).
func readUSBID(devPath string) (usbID, error) {
	base := filepath.Base(devPath)
	sysdir := filepath.Join("/sys/class/usbmisc", base, "device")
	vendor, err := readTrimmed(filepath.Join(sysdir, "idVendor"))
	if err != nil {
		vendor, err = readTrimmed(filepath.Join(sysdir, "..", "idVendor"))
		if err != nil {
			return usbID{}, err
		}
	}
	product, err := readTrimmed(filepath.Join(sysdir, "idProduct"))
	if err != nil {
		product, err = readTrimmed(filepath.Join(sysdir, "..", "idProduct"))
		if err != nil {
			return usbID{}, err
		}
	}
	return usbID{vendor: strings.ToLower(vendor), product: strings.ToLower(product)}, nil
}

func readTrimmed(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
