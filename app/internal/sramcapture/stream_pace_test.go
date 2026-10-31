// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"testing"
)

// paceFake is a stream image that takes the pace when capable: register 65
// its capability, 66 to 68 the pace written (29, 30 and 46) as it holds it.
// TRLC-LINKS: REQ-SDS-035
type paceFake struct {
	*streamFake
	capable bool
}

// TRLC-LINKS: REQ-SDS-035
func (p *paceFake) Read(plane uint8, r uint16) (uint16, error) {
	if p.capable {
		switch r {
		case 65:
			return streamPaceCapability, nil
		case 66:
			return p.staged[29], nil
		case 67:
			return p.staged[30] & 15, nil
		case 68:
			return p.staged[46], nil
		}
	}
	return p.streamFake.Read(plane, r)
}

// A pace is written to the image's registers and read back; the
// decimation's pace (zero) is written too, so no earlier stream's pace
// stays, and needs no capability; a pace on an image without it, or out of
// range, is refused.
// TRLC-LINKS: REQ-SDS-035
func TestStreamPaceWrittenAndChecked(t *testing.T) {
	f := &paceFake{streamFake: streamBus(), capable: true}
	c, err := New(f)
	if err != nil {
		t.Fatal(err)
	}
	set := func(p StreamPace) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.setStreamPace(p)
	}
	for _, p := range []StreamPace{{Words: 7}, {Words: 90}, {Words: 1 << 19}, {Words: 70000, RampClocks: 100}, {RampClocks: 2}, {}} {
		if err := set(p); err != nil {
			t.Fatalf("pace %+v: %v", p, err)
		}
		if got := uint32(f.staged[29]) | uint32(f.staged[30])<<16; got != p.Words || f.staged[46] != p.RampClocks {
			t.Fatalf("pace %+v: registers %d, %d", p, got, f.staged[46])
		}
	}
	for _, p := range []StreamPace{{Words: 1}, {Words: 1<<19 + 1}, {RampClocks: 1}} {
		if err := set(p); err == nil {
			t.Fatalf("pace %+v taken", p)
		}
	}
	f.capable = false
	if err := set(StreamPace{Words: 90}); err == nil {
		t.Fatal("a pace taken by an image without it")
	}
	f.staged[29] = 5
	if err := set(StreamPace{}); err != nil || f.staged[29] != 0 {
		t.Fatalf("the decimation's pace: %v, register 29 %d", err, f.staged[29])
	}
}
