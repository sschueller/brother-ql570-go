package ql

// Backend is the transport used to talk to a printer. Implementations:
//
//   - USBBackend (Linux and Android): a Brother QL printer exposed through
//     the kernel usblp driver as /dev/usb/lpN on Linux, or through the
//     usbdevfs file descriptor handed over by `termux-usb` on Android
//     (Termux). This is the only backend the QL-570 needs on these
//     platforms.
//   - The interface also allows other transports (raw USB via gousb, a
//     network print server, an HTTP daemon client, ...) to be added later
//     without changing the printing code.
//
// Printing on macOS: there is no direct-USB backend in this package
// (libusb on macOS requires cgo). Instead, run `ql570 serve` on the Linux
// host attached to the printer and submit jobs over HTTP, or import
// pkg/ql and provide your own Backend.
type Backend interface {
	// Read reads from the printer (status responses). It should return
	// promptly when no data is available.
	Read(p []byte) (int, error)
	// Write writes command bytes to the printer.
	Write(p []byte) (int, error)
	// Close closes the transport.
	Close() error
}

// BackendError is a non-fatal backend error (e.g. nothing to read).
type BackendError struct {
	Err error
}

func (e *BackendError) Error() string { return e.Err.Error() }
func (e *BackendError) Unwrap() error { return e.Err }

// VendorID and ProductID identify the Brother QL-570 on USB.
const (
	VendorID  = 0x04f9
	ProductID = 0x2028
)
