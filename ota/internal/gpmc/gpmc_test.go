package gpmc

import "testing"

func TestEncodeAccess(t *testing.T) {
	// spec 01 §1.2: version read on CS1, selector 0x12 un-shifted.
	b := EncodeAccess(PlaneCS1, SelVersion, 0)
	want := [6]byte{1, 0, 0x12, 0x00, 0, 0}
	if b != want {
		t.Errorf("version read encode = %v, want %v", b, want)
	}

	// A CS3 write with a two-byte selector and value, little-endian.
	b = EncodeAccess(3, 0x0134, 0xBEEF)
	want = [6]byte{3, 0, 0x34, 0x01, 0xEF, 0xBE}
	if b != want {
		t.Errorf("cs3 write encode = %v, want %v", b, want)
	}
	// b[0] must be the plane, never a read/write flag; b[1] always 0.
	if b[1] != 0 {
		t.Errorf("b[1] must be 0, got %d", b[1])
	}
}

func TestDecodeValue(t *testing.T) {
	if got := DecodeValue([6]byte{1, 0, 0x12, 0, 0x52, 0x00}); got != VersionMagic {
		t.Errorf("decode = 0x%04x, want 0x%04x", got, VersionMagic)
	}
	if got := DecodeValue([6]byte{1, 0, 0, 0, 0xff, 0x07}); got != 0x07ff {
		t.Errorf("decode = 0x%04x, want 0x07ff", got)
	}
}

func TestReaderNoFDIsSafe(t *testing.T) {
	r := NewReader(-1)
	if r.OK() {
		t.Error("OK() should be false with no fd")
	}
	if _, err := r.Read(PlaneCS1, SelVersion); err == nil {
		t.Error("Read with no fd should error, not panic or syscall")
	}
	if _, ok := r.VerifyVersion(); ok {
		t.Error("VerifyVersion with no fd should be false")
	}
}

func TestInvalidPlaneRejected(t *testing.T) {
	// Use fd 0 (stdin) so we never reach a real device; plane 0 must be
	// rejected BEFORE any syscall (b[0]=0 would stall the bus for seconds).
	r := NewReader(0)
	if _, err := r.Read(0, 0x12); err == nil {
		t.Error("plane 0 must be rejected")
	}
	if _, err := r.Read(2, 0x12); err == nil {
		t.Error("plane 2 must be rejected")
	}
}
