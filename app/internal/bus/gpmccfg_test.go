package bus

import "testing"

// The region survey must decode CONFIG7 the way the AM335x TRM defines it and
// must never write. fakeRegs records writes.
type recRegs struct {
	v      map[uint32]uint32
	writes int
}

func (r *recRegs) R(off uint32) uint32 { return r.v[off] }
func (r *recRegs) W(off, val uint32)   { r.writes++ }

func TestSurveyRegions(t *testing.T) {
	r := &recRegs{v: map[uint32]uint32{}}
	// CS1: valid, base 0x01000000, mask 0xF (16 MB); CS3: valid, base 0x07000000, 16 MB
	r.v[gpmcCSConfig(1, 7)] = 0x00000F41
	r.v[gpmcCSConfig(3, 7)] = 0x00000F47
	r.v[gpmcCSConfig(2, 7)] = 0x00000C42 // valid, 64 MB, base 0x02000000
	got := surveyRegions(r)
	if len(got) != 7 || r.writes != 0 {
		t.Fatalf("survey wrote %d times, %d regions", r.writes, len(got))
	}
	if !got[1].Valid || got[1].Base != 0x01000000 || got[1].SizeMB != 16 {
		t.Fatalf("CS1: %+v", got[1])
	}
	if !got[2].Valid || got[2].Base != 0x02000000 || got[2].SizeMB != 64 {
		t.Fatalf("CS2: %+v", got[2])
	}
	if !got[3].Valid || got[3].Base != 0x07000000 {
		t.Fatalf("CS3: %+v", got[3])
	}
	if got[0].Valid || got[4].Valid {
		t.Fatalf("unset regions must not read valid: %+v %+v", got[0], got[4])
	}
}
