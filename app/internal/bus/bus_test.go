package bus

import "testing"

func TestEncode(t *testing.T) {
	// Verified encodings (spec 01 §1.2 / ota gpmc_test.go): raw selector,
	// little-endian, never pre-shifted.
	if got := encode(1, 0x12, 0); got != [6]byte{1, 0, 0x12, 0, 0, 0} {
		t.Fatalf("version read encode = %v", got)
	}
	if got := encode(3, 0x0134, 0xBEEF); got != [6]byte{3, 0, 0x34, 0x01, 0xEF, 0xBE} {
		t.Fatalf("cs3 write encode = %v", got)
	}
}

func TestForbiddenWrites(t *testing.T) {
	forbidden := []struct {
		plane uint8
		sel   uint16
	}{
		{3, 0x07},            // nCONFIG/config-status — writing collapses the engine
		{1, 0x01}, {1, 0x0f}, // cal bank low
		{1, 0x27}, {1, 0x2a}, // gain-cal words
		{1, 0x5a}, {1, 0x7f}, // cal bank
	}
	for _, c := range forbidden {
		if !forbiddenWrite(c.plane, c.sel) {
			t.Errorf("cs%d sel %#04x not guarded", c.plane, c.sel)
		}
	}
	allowed := []struct {
		plane uint8
		sel   uint16
	}{
		{1, 0x00}, {1, 0x16}, {1, 0x19}, {1, 0x1a}, {1, 0x1b}, {1, 0x21},
		{1, 0x35}, {1, 0x36}, {1, 0x3c}, {1, 0x3d}, {1, 0x3e},
		{1, 0x44}, {1, 0x57}, {1, 0x58},
		{3, 0x14}, {3, 0x34}, {3, 0x15}, {3, 0x35},
	}
	for _, c := range allowed {
		if forbiddenWrite(c.plane, c.sel) {
			t.Errorf("cs%d sel %#04x wrongly guarded", c.plane, c.sel)
		}
	}
}
