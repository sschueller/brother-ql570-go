package ql

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// USB descriptor types (USB 2.0 spec, table 9-5).
const (
	usbDescDevice    = 0x01
	usbDescConfig    = 0x02
	usbDescInterface = 0x04
	usbDescEndpoint  = 0x05
)

// usbEndpoint is a parsed USB endpoint descriptor. address is the full
// bEndpointAddress (direction bit included), as the usbdevfs ioctls
// require.
type usbEndpoint struct {
	address   uint8
	attrs     uint8
	maxPacket uint16
}

// in reports whether the endpoint is device-to-host (IN).
func (e usbEndpoint) in() bool { return e.address&0x80 != 0 }

// number is the endpoint number without the direction bit.
func (e usbEndpoint) number() uint8 { return e.address & 0x0f }

// bulk reports whether the endpoint is a bulk endpoint.
func (e usbEndpoint) bulk() bool { return e.attrs&0x03 == 0x02 }

// usbInterface is a parsed USB interface descriptor with its endpoints.
type usbInterface struct {
	number    uint8
	class     uint8
	endpoints []usbEndpoint
}

// bulkIn returns the interface's bulk IN endpoint, if any.
func (i usbInterface) bulkIn() (usbEndpoint, bool) {
	for _, ep := range i.endpoints {
		if ep.bulk() && ep.in() {
			return ep, true
		}
	}
	return usbEndpoint{}, false
}

// bulkOut returns the interface's bulk OUT endpoint, if any.
func (i usbInterface) bulkOut() (usbEndpoint, bool) {
	for _, ep := range i.endpoints {
		if ep.bulk() && !ep.in() {
			return ep, true
		}
	}
	return usbEndpoint{}, false
}

// printerEndpoints identifies the interface and the bulk endpoint pair
// used to talk to a printer.
type printerEndpoints struct {
	iface   usbInterface
	bulkIn  usbEndpoint
	bulkOut usbEndpoint
}

// parseDeviceDescriptor validates a USB device descriptor and extracts the
// vendor and product ID.
func parseDeviceDescriptor(b []byte) (vid, pid uint16, err error) {
	if len(b) < 18 {
		return 0, 0, fmt.Errorf("device descriptor too short: %d bytes", len(b))
	}
	if b[0] < 18 {
		return 0, 0, fmt.Errorf("device descriptor bLength %d < 18", b[0])
	}
	if b[1] != usbDescDevice {
		return 0, 0, fmt.Errorf("not a device descriptor (bDescriptorType 0x%02x)", b[1])
	}
	return binary.LittleEndian.Uint16(b[8:10]), binary.LittleEndian.Uint16(b[10:12]), nil
}

// parseConfigDescriptor walks a configuration descriptor blob (the
// configuration descriptor followed by its interface and endpoint
// descriptors) and returns the interfaces it contains.
func parseConfigDescriptor(b []byte) ([]usbInterface, error) {
	if len(b) < 9 {
		return nil, fmt.Errorf("config descriptor too short: %d bytes", len(b))
	}
	if b[0] < 9 {
		return nil, fmt.Errorf("config descriptor bLength %d < 9", b[0])
	}
	if b[1] != usbDescConfig {
		return nil, fmt.Errorf("not a config descriptor (bDescriptorType 0x%02x)", b[1])
	}
	total := int(binary.LittleEndian.Uint16(b[2:4]))
	if total < 9 {
		return nil, fmt.Errorf("config descriptor wTotalLength %d < 9", total)
	}
	if total > len(b) {
		return nil, fmt.Errorf("config descriptor wTotalLength %d exceeds the %d bytes received", total, len(b))
	}
	b = b[:total]

	var (
		ifaces  []usbInterface
		current *usbInterface
	)
	for off := 9; off < len(b); {
		length := int(b[off])
		if length < 2 || off+length > len(b) {
			return nil, fmt.Errorf("malformed descriptor at offset %d (bLength %d)", off, length)
		}
		switch b[off+1] {
		case usbDescInterface:
			if length < 9 {
				return nil, fmt.Errorf("interface descriptor at offset %d too short: bLength %d", off, length)
			}
			ifaces = append(ifaces, usbInterface{
				number: b[off+2],
				class:  b[off+5],
			})
			current = &ifaces[len(ifaces)-1]
		case usbDescEndpoint:
			if length < 7 {
				return nil, fmt.Errorf("endpoint descriptor at offset %d too short: bLength %d", off, length)
			}
			if current == nil {
				return nil, fmt.Errorf("endpoint descriptor at offset %d without a preceding interface", off)
			}
			current.endpoints = append(current.endpoints, usbEndpoint{
				address:   b[off+2],
				attrs:     b[off+3],
				maxPacket: binary.LittleEndian.Uint16(b[off+4 : off+6]),
			})
		}
		off += length
	}
	return ifaces, nil
}

// selectPrinterInterface picks the interface to print on: preferably the
// printer-class (7) interface with a bulk IN and a bulk OUT endpoint,
// falling back to the first interface that has both.
func selectPrinterInterface(ifaces []usbInterface) (printerEndpoints, error) {
	if len(ifaces) == 0 {
		return printerEndpoints{}, errors.New("no USB interfaces found")
	}
	var fallback *printerEndpoints
	for _, iface := range ifaces {
		in, inOK := iface.bulkIn()
		out, outOK := iface.bulkOut()
		if !inOK || !outOK {
			continue
		}
		if iface.class == 7 {
			return printerEndpoints{iface: iface, bulkIn: in, bulkOut: out}, nil
		}
		if fallback == nil {
			fallback = &printerEndpoints{iface: iface, bulkIn: in, bulkOut: out}
		}
	}
	if fallback != nil {
		return *fallback, nil
	}
	return printerEndpoints{}, errors.New("no USB interface with both a bulk IN and a bulk OUT endpoint")
}
