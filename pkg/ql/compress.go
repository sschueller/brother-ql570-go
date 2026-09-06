package ql

import (
	"errors"
	"fmt"
)

// TIFF PackBits compression for raster lines, per the official Command
// Reference, section 5 "Compression mode selection":
//
//   - A run of 2..128 identical bytes is encoded as a negative count
//     (-(n-1)) followed by the byte.
//   - A run of 1..128 literal bytes is encoded as a positive count (n-1)
//     followed by the literal bytes.
//   - If the compressed result is not smaller than the input, the caller
//     must transmit the raw line instead.
//
// Note: the QL-570 does not support TIFF compression at all (only QL-580N/
// 650TD/1050/1060N do). The encoder is kept for library completeness and
// for golden tests against the reference example.
//
// Reference example (from the official document):
//
//	raw:  00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00
//	      22 22 23 BA BF A2 22 2B
//	enc:  ED 00 FF 22 05 23 BA BF A2 22 2B

// packBitsMaxRun is the maximum run length (128 bytes).
const packBitsMaxRun = 128

// PackBitsEncode encodes src with TIFF PackBits. It returns the encoded
// data and whether the result is smaller than src.
func PackBitsEncode(src []byte) ([]byte, bool) {
	if len(src) == 0 {
		return nil, false
	}
	out := make([]byte, 0, len(src))
	for i := 0; i < len(src); {
		// Count the run of identical bytes.
		run := 1
		for i+run < len(src) && src[i+run] == src[i] && run < packBitsMaxRun {
			run++
		}
		if run >= 2 {
			out = append(out, byte(-(run - 1)), src[i])
			i += run
			continue
		}
		// Count the run of literal (differing) bytes.
		lit := 1
		for i+lit < len(src) && lit < packBitsMaxRun {
			if i+lit+1 < len(src) && src[i+lit] == src[i+lit+1] {
				break
			}
			lit++
		}
		out = append(out, byte(lit-1))
		out = append(out, src[i:i+lit]...)
		i += lit
	}
	return out, len(out) < len(src)
}

// PackBitsDecode decodes TIFF PackBits data.
func PackBitsDecode(src []byte) ([]byte, error) {
	out := make([]byte, 0, len(src)*2)
	for i := 0; i < len(src); {
		n := int(int8(src[i]))
		i++
		if n >= 0 {
			n++
			if i+n > len(src) {
				return nil, errors.New("packbits: truncated literal run")
			}
			out = append(out, src[i:i+n]...)
			i += n
		} else {
			n = -n + 1
			if i >= len(src) {
				return nil, errors.New("packbits: truncated repeat run")
			}
			for j := 0; j < n; j++ {
				out = append(out, src[i])
			}
			i++
		}
	}
	return out, nil
}

// packBitsExample is the reference example from the official document.
var packBitsExampleRaw = []byte{
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0x22, 0x22, 0x23, 0xBA, 0xBF, 0xA2, 0x22, 0x2B,
}

var packBitsExampleEnc = []byte{
	0xED, 0x00, 0xFF, 0x22, 0x05, 0x23, 0xBA, 0xBF, 0xA2, 0x22, 0x2B,
}

// ensurePackBitsExampleIsCorrect is referenced from the test file; it
// documents the expected encoding in one place.
func ensurePackBitsExampleIsCorrect(got []byte) error {
	if len(got) != len(packBitsExampleEnc) {
		return fmt.Errorf("length mismatch: got %d want %d", len(got), len(packBitsExampleEnc))
	}
	for i := range got {
		if got[i] != packBitsExampleEnc[i] {
			return fmt.Errorf("byte %d: got %02X want %02X", i, got[i], packBitsExampleEnc[i])
		}
	}
	return nil
}
