//go:build android

package ql

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// USBBackend talks to a Brother QL printer over usbdevfs on Android, using
// the raw file descriptor handed over by `termux-usb` (termux-api). The
// Android UsbManager opens the device (handling the permission dialog) and
// passes the fd to the executed command; this backend runs all usbdevfs
// ioctls directly on that fd, so no root, cgo or JNI is needed.
//
// All transfers go through the USBDEVFS_BULK / USBDEVFS_CONTROL ioctls via
// raw syscalls, mirroring the Linux backend's approach of bypassing Go's
// runtime poller. The bulk OUT endpoint carries the print command stream,
// the bulk IN endpoint the 32-byte status responses. Endpoints are
// discovered dynamically from the device's descriptors - nothing is
// hardcoded.
//
// The ioctl numbers and transfer struct layouts below are the 64-bit
// (arm64) usbdevfs ABI, matching the supported GOARCH=arm64 build.
type USBBackend struct {
	f        *os.File
	path     string
	iface    int
	epIn     usbEndpoint
	epOut    usbEndpoint
	deadline time.Time
}

// usbdevfs ioctl request numbers on 64-bit Linux (uapi/linux/usbdevice_fs.h
// _IOC macros with 64-bit pointers).
const (
	usbdevfsControl          = 0xc0185500 // _IOWR('U', 0, usbdevfs_ctrltransfer)
	usbdevfsBulk             = 0xc0185502 // _IOWR('U', 2, usbdevfs_bulktransfer)
	usbdevfsResetEP          = 0x80045503 // _IOR('U', 3, unsigned int)
	usbdevfsClaimInterface   = 0x8004550f // _IOR('U', 15, unsigned int)
	usbdevfsReleaseInterface = 0x80045510 // _IOR('U', 16, unsigned int)
)

// maxBulkChunk bounds a single USBDEVFS_BULK transfer. Larger writes are
// split into multiple ioctls by Write.
const maxBulkChunk = 16384

// writeTimeout bounds a single bulk OUT ioctl. The largest possible job
// (~1.1 MB) transfers in well under a second over USB bulk; 20 s is very
// generous.
const writeTimeout = 20 * time.Second

// usbdevfsCtrltransfer mirrors struct usbdevfs_ctrltransfer on 64-bit
// Linux: 24 bytes including the padding between timeout and data.
type usbdevfsCtrltransfer struct {
	bRequestType uint8
	bRequest     uint8
	wValue       uint16
	wIndex       uint16
	wLength      uint16
	timeout      uint32
	data         uintptr
}

// usbdevfsBulktransfer mirrors struct usbdevfs_bulktransfer on 64-bit
// Linux: 24 bytes. ep carries the full endpoint address, direction bit
// included (the kernel picks the IN/OUT pipe from it).
type usbdevfsBulktransfer struct {
	ep      uint32
	len     uint32
	timeout uint32
	data    uintptr
}

// Compile-time guards for the arm64 ABI layouts the ioctl numbers encode.
var (
	_ [24]byte = [unsafe.Sizeof(usbdevfsCtrltransfer{})]byte{}
	_ [24]byte = [unsafe.Sizeof(usbdevfsBulktransfer{})]byte{}
)

// OpenUSB opens the printer backend for the given device string:
//
//   - "fd:N" - use the already-open usbdevfs file descriptor N (as handed
//     over by `termux-usb` as a command argument).
//   - "" - use the TERMUX_USB_FD environment variable (set by
//     `termux-usb -E`).
//   - a /dev/bus/usb/BBB/DDD path - open it directly (works on rooted
//     devices only; not the supported path).
func OpenUSB(device string) (*USBBackend, error) {
	fd, ok, err := deviceFD(device)
	if err != nil {
		return nil, err
	}
	if ok {
		return OpenUSBFD(fd)
	}
	f, err := os.OpenFile(device, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w (opening device nodes directly needs root; without root run through `termux-usb -E`, see the README Android section)", device, err)
	}
	return newBackend(f, device)
}

// OpenUSBFD wraps a raw usbdevfs file descriptor (as handed over by
// termux-usb) in a USBBackend, discovering and claiming the printer
// endpoints on it.
func OpenUSBFD(fd int) (*USBBackend, error) {
	return newBackend(os.NewFile(uintptr(fd), "ql570-usb"), fmt.Sprintf("fd:%d", fd))
}

// deviceFD resolves the file descriptor for "fd:N" device strings and the
// TERMUX_USB_FD environment variable.
func deviceFD(device string) (fd int, ok bool, err error) {
	s := device
	switch {
	case strings.HasPrefix(s, "fd:"):
		s = strings.TrimPrefix(s, "fd:")
	case s == "":
		s = os.Getenv("TERMUX_USB_FD")
		if s == "" {
			return 0, false, fmt.Errorf("no printer device given and $TERMUX_USB_FD is not set: run the command through `termux-usb -E -e ...` (a permission dialog appears on first use), or pass --device fd:N")
		}
	default:
		return 0, false, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0, false, fmt.Errorf("invalid USB file descriptor %q", s)
	}
	return n, true, nil
}

// newBackend verifies the device on f (VID/PID check, descriptor parsing,
// endpoint discovery) and claims the printer interface.
func newBackend(f *os.File, path string) (*USBBackend, error) {
	fail := func(err error) (*USBBackend, error) {
		_ = f.Close()
		return nil, err
	}
	b := &USBBackend{f: f, path: path}

	dd, err := b.getDescriptor(usbDescDevice, 0, 18)
	if err != nil {
		return fail(fmt.Errorf("reading device descriptor: %w (is the printer connected and did you accept the permission dialog?)", err))
	}
	vid, pid, err := parseDeviceDescriptor(dd)
	if err != nil {
		return fail(fmt.Errorf("parsing device descriptor: %w", err))
	}
	if vid != VendorID || pid != ProductID {
		return fail(fmt.Errorf("not a Brother QL-570: device is %04x:%04x (expected %04x:%04x); find the printer with `termux-usb -l`", vid, pid, VendorID, ProductID))
	}

	// Fetch the 9-byte config descriptor header first to learn the total
	// descriptor set length, then fetch the whole set.
	head, err := b.getDescriptor(usbDescConfig, 0, 9)
	if err != nil {
		return fail(fmt.Errorf("reading config descriptor header: %w", err))
	}
	if len(head) < 9 {
		return fail(fmt.Errorf("config descriptor header too short: %d bytes", len(head)))
	}
	total := int(binary.LittleEndian.Uint16(head[2:4]))
	if total < 9 || total > 4096 {
		return fail(fmt.Errorf("implausible config descriptor length %d", total))
	}
	full, err := b.getDescriptor(usbDescConfig, 0, uint16(total))
	if err != nil {
		return fail(fmt.Errorf("reading config descriptor: %w", err))
	}
	ifaces, err := parseConfigDescriptor(full)
	if err != nil {
		return fail(fmt.Errorf("parsing config descriptor: %w", err))
	}
	eps, err := selectPrinterInterface(ifaces)
	if err != nil {
		return fail(err)
	}
	b.iface = int(eps.iface.number)
	b.epIn = eps.bulkIn
	b.epOut = eps.bulkOut

	if err := b.claimInterface(); err != nil {
		return fail(fmt.Errorf("claiming interface %d: %w (another app may hold the device; unplug and replug the printer or close other apps using it)", b.iface, err))
	}
	return b, nil
}

// ioctl performs a raw ioctl on the backend's file descriptor and returns
// the kernel's return value (e.g. the number of bytes transferred).
func (b *USBBackend) ioctl(req uintptr, arg unsafe.Pointer) (uintptr, error) {
	r1, _, errno := syscall.Syscall(syscall.SYS_IOCTL, b.f.Fd(), req, uintptr(arg))
	if errno != 0 {
		return r1, errno
	}
	return r1, nil
}

// control performs a USB control transfer. For IN transfers the received
// data is written to buf and the number of bytes received is returned.
func (b *USBBackend) control(reqType, req uint8, value, index uint16, buf []byte, timeout time.Duration) (int, error) {
	ct := usbdevfsCtrltransfer{
		bRequestType: reqType,
		bRequest:     req,
		wValue:       value,
		wIndex:       index,
		wLength:      uint16(len(buf)),
		timeout:      uint32(timeout / time.Millisecond),
	}
	if len(buf) > 0 {
		ct.data = uintptr(unsafe.Pointer(&buf[0]))
	}
	n, err := b.ioctl(usbdevfsControl, unsafe.Pointer(&ct))
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// getDescriptor fetches a standard descriptor (type, index) from the
// device.
func (b *USBBackend) getDescriptor(descType, index uint8, length uint16) ([]byte, error) {
	buf := make([]byte, length)
	n, err := b.control(0x80, 0x06, uint16(descType)<<8|uint16(index), 0, buf, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("GET_DESCRIPTOR type %d index %d: %w", descType, index, err)
	}
	return buf[:n], nil
}

// claimInterface claims the printer interface via USBDEVFS_CLAIMINTERFACE.
// termux-usb hands over an unclaimed fd, so the claim normally succeeds;
// EBUSY means another process holds the interface.
func (b *USBBackend) claimInterface() error {
	iface := uint32(b.iface)
	_, err := b.ioctl(usbdevfsClaimInterface, unsafe.Pointer(&iface))
	return err
}

// releaseInterface releases the printer interface via
// USBDEVFS_RELEASEINTERFACE. ENODATA (not claimed) is treated as success.
func (b *USBBackend) releaseInterface() error {
	iface := uint32(b.iface)
	_, err := b.ioctl(usbdevfsReleaseInterface, unsafe.Pointer(&iface))
	if err == syscall.ENODATA {
		return nil
	}
	return err
}

// resetEP clears an endpoint's data toggle/stall via USBDEVFS_RESETEP.
func (b *USBBackend) resetEP(ep usbEndpoint) error {
	addr := uint32(ep.address)
	_, err := b.ioctl(usbdevfsResetEP, unsafe.Pointer(&addr))
	return err
}

// bulk performs one USBDEVFS_BULK transfer on the given endpoint, bounded
// to maxBulkChunk bytes.
func (b *USBBackend) bulk(ep usbEndpoint, p []byte, timeout time.Duration) (int, error) {
	if len(p) > maxBulkChunk {
		p = p[:maxBulkChunk]
	}
	bt := usbdevfsBulktransfer{
		ep:      uint32(ep.address),
		len:     uint32(len(p)),
		timeout: uint32(timeout / time.Millisecond),
	}
	if bt.timeout < 1 {
		// A timeout of 0 means "wait forever" to the kernel.
		bt.timeout = 1
	}
	if len(p) > 0 {
		bt.data = uintptr(unsafe.Pointer(&p[0]))
	}
	n, err := b.ioctl(usbdevfsBulk, unsafe.Pointer(&bt))
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// Read reads up to len(p) bytes from the printer's bulk IN endpoint,
// honoring the deadline set by SetReadDeadline. USBDEVFS_BULK timeouts are
// sliced into ~100 ms chunks (like the Linux backend's poll slices), and a
// deadline expiry surfaces as os.ErrDeadlineExceeded.
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
		n, err := b.bulk(b.epIn, p, tv)
		switch err {
		case nil:
			return n, nil
		case syscall.EINTR:
			continue
		case syscall.ETIMEDOUT:
			if time.Now().Before(b.deadline) {
				continue
			}
			return 0, os.ErrDeadlineExceeded
		default:
			return 0, fmt.Errorf("bulk IN endpoint %#02x: %w", b.epIn.address, err)
		}
	}
}

// Write writes all of p to the printer's bulk OUT endpoint, chunked into
// maxBulkChunk-sized USBDEVFS_BULK ioctls. Each chunk is bounded by
// writeTimeout; on timeout the endpoint is left cleared for the next
// Reopen (which printer.go calls via resetUSB on write errors).
func (b *USBBackend) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > maxBulkChunk {
			chunk = chunk[:maxBulkChunk]
		}
		n, err := b.bulk(b.epOut, chunk, writeTimeout)
		total += n
		if err != nil {
			return total, fmt.Errorf("bulk OUT endpoint %#02x: %w", b.epOut.address, err)
		}
		if n == 0 {
			return total, errors.New("bulk OUT transfer wrote 0 bytes")
		}
		p = p[n:]
	}
	return total, nil
}

// Reopen resets the USB state on the same file descriptor: the interface
// is released, both endpoints are reset (clearing stalls and data toggles)
// and the interface is claimed again. The fd itself cannot be re-acquired
// without going through termux-usb again, so it is kept open.
func (b *USBBackend) Reopen() error {
	if err := b.releaseInterface(); err != nil {
		return fmt.Errorf("releasing interface %d: %w", b.iface, err)
	}
	if err := b.resetEP(b.epIn); err != nil {
		return fmt.Errorf("resetting bulk IN endpoint %#02x: %w", b.epIn.address, err)
	}
	if err := b.resetEP(b.epOut); err != nil {
		return fmt.Errorf("resetting bulk OUT endpoint %#02x: %w", b.epOut.address, err)
	}
	if err := b.claimInterface(); err != nil {
		return fmt.Errorf("re-claiming interface %d: %w", b.iface, err)
	}
	return nil
}

// Close releases the printer interface and closes the device file
// descriptor.
func (b *USBBackend) Close() error {
	if b.f == nil {
		return nil
	}
	_ = b.releaseInterface()
	err := b.f.Close()
	b.f = nil
	return err
}

// Path returns the device identifier.
func (b *USBBackend) Path() string { return b.path }

// String returns a human-readable description of the backend.
func (b *USBBackend) String() string { return b.path }

// SetReadDeadline bounds the next Read call in time.
func (b *USBBackend) SetReadDeadline(t time.Time) error {
	b.deadline = t
	return nil
}

// USBDevice mirrors the Linux backend's device description.
type USBDevice struct {
	Path      string `json:"path"`
	VendorID  string `json:"vendor_id"`
	ProductID string `json:"product_id"`
	Model     string `json:"model"`
	IsQL570   bool   `json:"is_ql570"`
}

// DiscoverUSB is not available on Android: without root, /dev/bus/usb
// cannot be scanned. Use `termux-usb -l` to list attached devices.
func DiscoverUSB() ([]USBDevice, error) {
	return nil, errors.New("device scanning is not available on Android; run `termux-usb -l` to list attached devices")
}
