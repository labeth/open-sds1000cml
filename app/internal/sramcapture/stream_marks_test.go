// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"context"
	"testing"
)

// markFake serves the mark registers; bumps, when set, advances the rising
// count on that many reads of register 56 (a mark arriving mid-read).
// TRLC-LINKS: REQ-SDS-272
type markFake struct {
	*streamFake
	regs  map[uint16]uint16
	bumps int
}

// TRLC-LINKS: REQ-SDS-272
func (m *markFake) Read(p uint8, r uint16) (uint16, error) {
	if r >= 54 && r <= 60 {
		if r == 56 && m.bumps > 0 {
			m.bumps--
			m.regs[55]++
			m.regs[56] += 10
		}
		return m.regs[r], nil
	}
	return m.streamFake.Read(p, r)
}

// The marks read as the image holds them, a count that changes during its
// word's read is read again, and an image without the capability has none.
// TRLC-LINKS: REQ-SDS-272
func TestStreamMarksRead(t *testing.T) {
	f := &markFake{streamFake: streamBus(), regs: map[uint16]uint16{54: streamMarkCapability, 55: 7, 56: 0x1234, 57: 0x0002, 58: 3, 59: 0xffff, 60: 0x7fff}}
	c, err := New(f)
	if err != nil {
		t.Fatal(err)
	}
	if !c.StreamMarksSupported() {
		t.Fatal("capability not seen")
	}
	m, err := c.StreamMarks()
	if err != nil || m.RiseCount != 7 || m.RiseWord != 0x21234 || m.FallCount != 3 || m.FallWord != 0x7fffffff {
		t.Fatalf("marks %+v %v", m, err)
	}
	f.bumps = 1
	m, err = c.StreamMarks()
	if err != nil || m.RiseCount != 8 || m.RiseWord != 0x2123e {
		t.Fatalf("re-read %+v %v", m, err)
	}
	f.bumps = 5
	if _, err := c.StreamMarks(); err == nil {
		t.Fatal("marks that never hold still read")
	}
	f.regs[54] = 0
	if c.StreamMarksSupported() {
		t.Fatal("an image without marks reads as having them")
	}
}

// The marked stream starts continuous with the directions asked, and a live
// change writes the channel, level and directions.
// TRLC-LINKS: REQ-SDS-272, REQ-SDS-265
func TestStreamMarkedStart(t *testing.T) {
	f := &markFake{streamFake: streamBus(), regs: map[uint16]uint16{54: streamMarkCapability}}
	c, err := New(f)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Source: ADC, DecimationLog2: 13, PreWords: Words - 17, PostWords: 17, Normal: true}
	_ = c.StartStreamMarked(context.Background(), cfg, StreamMarkMode{Rise: true, Fall: true})
	if f.staged[19] != 1|2|8|16 || f.staged[18] != 13 {
		t.Fatalf("mode %d decimation %d", f.staged[19], f.staged[18])
	}
	if err := c.SetStreamMarks(1, 140, 3, StreamMarkMode{Rise: true}); err != nil {
		t.Fatal(err)
	}
	if f.staged[6] != 1|16|64 || f.staged[7] != 140|3<<8 || f.staged[19] != 1|2|8 {
		t.Fatalf("live change: 6=%x 7=%x 19=%x", f.staged[6], f.staged[7], f.staged[19])
	}
	if err := c.StartStreamMarked(context.Background(), Config{DecimationLog2: 4}, StreamMarkMode{}); err == nil {
		t.Fatal("a decimation the stream cannot carry was taken")
	}
}
