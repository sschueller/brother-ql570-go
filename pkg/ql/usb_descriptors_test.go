package ql

import (
	"encoding/binary"
	"strings"
	"testing"
)

// The descriptor parsing helpers (parseDeviceDescriptor,
// parseConfigDescriptor, selectPrinterInterface) live in
// usb_descriptors.go without build constraints so these tests compile and
// run on every platform - they need no device and no ioctls, only byte
// fixtures.

// ql570DeviceDescriptor is a realistic QL-570 device descriptor
// (04f9:2028, one configuration).
var ql570DeviceDescriptor = []byte{
	0x12, 0x01, 0x00, 0x02, 0x00, 0x00, 0x00, 0x40,
	0xf9, 0x04, 0x28, 0x20, 0x00, 0x01, 0x01, 0x02,
	0x00, 0x01,
}

// testEndpoint is a compact endpoint fixture.
type testEndpoint struct {
	addr      uint8
	attrs     uint8
	maxPacket uint16
}

// testInterface is a compact interface fixture.
type testInterface struct {
	number uint8
	class  uint8
	eps    []testEndpoint
}

// buildConfigBlob assembles a configuration descriptor blob (config +
// interface + endpoint descriptors) from interface fixtures.
func buildConfigBlob(ifaces ...testInterface) []byte {
	blob := make([]byte, 9)
	for _, iface := range ifaces {
		blob = append(blob, 9, usbDescInterface, iface.number, 0, uint8(len(iface.eps)), iface.class, 0, 0, 0)
		for _, ep := range iface.eps {
			blob = append(blob, 7, usbDescEndpoint, ep.addr, ep.attrs, 0, 0, 0)
			binary.LittleEndian.PutUint16(blob[len(blob)-3:len(blob)-1], ep.maxPacket)
		}
	}
	blob[0] = 9
	blob[1] = usbDescConfig
	binary.LittleEndian.PutUint16(blob[2:4], uint16(len(blob)))
	blob[4] = uint8(len(ifaces))
	blob[5] = 1
	blob[7] = 0x80
	blob[8] = 50
	return blob
}

// ql570ConfigBlob is a realistic QL-570 configuration: one printer-class
// (7) interface with bulk OUT (0x01) and bulk IN (0x82) endpoints.
func ql570ConfigBlob() []byte {
	return buildConfigBlob(testInterface{
		number: 0,
		class:  7,
		eps: []testEndpoint{
			{addr: 0x01, attrs: 0x02, maxPacket: 64},
			{addr: 0x82, attrs: 0x02, maxPacket: 64},
		},
	})
}

func TestParseDeviceDescriptor(t *testing.T) {
	vid, pid, err := parseDeviceDescriptor(ql570DeviceDescriptor)
	if err != nil {
		t.Fatalf("parseDeviceDescriptor: %v", err)
	}
	if vid != 0x04f9 || pid != 0x2028 {
		t.Errorf("got %04x:%04x, want 04f9:2028", vid, pid)
	}
}

func TestParseDeviceDescriptorShort(t *testing.T) {
	if _, _, err := parseDeviceDescriptor(ql570DeviceDescriptor[:17]); err == nil {
		t.Error("expected error for truncated device descriptor")
	}
}

func TestParseDeviceDescriptorWrongType(t *testing.T) {
	b := append([]byte(nil), ql570DeviceDescriptor...)
	b[1] = usbDescConfig
	if _, _, err := parseDeviceDescriptor(b); err == nil {
		t.Error("expected error for wrong descriptor type")
	}
}

func TestParseConfigDescriptor(t *testing.T) {
	ifaces, err := parseConfigDescriptor(ql570ConfigBlob())
	if err != nil {
		t.Fatalf("parseConfigDescriptor: %v", err)
	}
	if len(ifaces) != 1 {
		t.Fatalf("got %d interfaces, want 1", len(ifaces))
	}
	iface := ifaces[0]
	if iface.number != 0 || iface.class != 7 {
		t.Errorf("got interface number=%d class=%d, want 0/7", iface.number, iface.class)
	}
	if len(iface.endpoints) != 2 {
		t.Fatalf("got %d endpoints, want 2", len(iface.endpoints))
	}
	out, in := iface.endpoints[0], iface.endpoints[1]
	if out.address != 0x01 || out.in() || !out.bulk() || out.maxPacket != 64 {
		t.Errorf("bulk OUT endpoint parsed as %+v (addr=%#02x in=%v bulk=%v)", out, out.address, out.in(), out.bulk())
	}
	if in.address != 0x82 || !in.in() || !in.bulk() || in.maxPacket != 64 {
		t.Errorf("bulk IN endpoint parsed as %+v (addr=%#02x in=%v bulk=%v)", in, in.address, in.in(), in.bulk())
	}
}

func TestParseConfigDescriptorErrors(t *testing.T) {
	cases := map[string]func(b []byte) []byte{
		"truncated header": func(b []byte) []byte { return b[:8] },
		"wTotalLength too big": func(b []byte) []byte {
			b = append([]byte(nil), b...)
			binary.LittleEndian.PutUint16(b[2:4], uint16(len(b)+10))
			return b
		},
		"malformed bLength": func(b []byte) []byte {
			b = append([]byte(nil), b...)
			b[9] = 1 // interface descriptor with bLength 1
			return b
		},
		"endpoint before interface": func(b []byte) []byte {
			b = append([]byte(nil), b...)
			// Drop the interface descriptor, leaving a stray endpoint.
			b = append(b[:9], b[18:]...)
			binary.LittleEndian.PutUint16(b[2:4], uint16(len(b)))
			return b
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseConfigDescriptor(mutate(ql570ConfigBlob())); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestSelectPrinterInterfacePrefersPrinterClass(t *testing.T) {
	blob := buildConfigBlob(
		testInterface{number: 0, class: 3, eps: []testEndpoint{
			{addr: 0x01, attrs: 0x02, maxPacket: 64},
			{addr: 0x81, attrs: 0x02, maxPacket: 64},
		}},
		testInterface{number: 1, class: 7, eps: []testEndpoint{
			{addr: 0x02, attrs: 0x02, maxPacket: 64},
			{addr: 0x83, attrs: 0x02, maxPacket: 64},
		}},
	)
	ifaces, err := parseConfigDescriptor(blob)
	if err != nil {
		t.Fatalf("parseConfigDescriptor: %v", err)
	}
	eps, err := selectPrinterInterface(ifaces)
	if err != nil {
		t.Fatalf("selectPrinterInterface: %v", err)
	}
	if eps.iface.number != 1 || eps.iface.class != 7 {
		t.Errorf("picked interface %d class %d, want the printer-class interface 1", eps.iface.number, eps.iface.class)
	}
	if eps.bulkOut.address != 0x02 || eps.bulkIn.address != 0x83 {
		t.Errorf("picked endpoints OUT %#02x IN %#02x, want 0x02/0x83", eps.bulkOut.address, eps.bulkIn.address)
	}
}

func TestSelectPrinterInterfaceFallsBackToBulkPair(t *testing.T) {
	blob := buildConfigBlob(testInterface{number: 0, class: 3, eps: []testEndpoint{
		{addr: 0x01, attrs: 0x02, maxPacket: 64},
		{addr: 0x81, attrs: 0x02, maxPacket: 64},
	}})
	ifaces, err := parseConfigDescriptor(blob)
	if err != nil {
		t.Fatalf("parseConfigDescriptor: %v", err)
	}
	eps, err := selectPrinterInterface(ifaces)
	if err != nil {
		t.Fatalf("selectPrinterInterface: %v", err)
	}
	if eps.iface.number != 0 {
		t.Errorf("picked interface %d, want fallback interface 0", eps.iface.number)
	}
}

func TestSelectPrinterInterfaceErrors(t *testing.T) {
	if _, err := selectPrinterInterface(nil); err == nil {
		t.Error("expected error for no interfaces")
	}
	blob := buildConfigBlob(testInterface{number: 0, class: 3, eps: []testEndpoint{
		{addr: 0x81, attrs: 0x03, maxPacket: 8}, // interrupt IN only
	}})
	ifaces, err := parseConfigDescriptor(blob)
	if err != nil {
		t.Fatalf("parseConfigDescriptor: %v", err)
	}
	if _, err := selectPrinterInterface(ifaces); err == nil {
		t.Error("expected error for interface without a bulk IN+OUT pair")
	} else if !strings.Contains(err.Error(), "bulk IN") {
		t.Errorf("unexpected error: %v", err)
	}
}
