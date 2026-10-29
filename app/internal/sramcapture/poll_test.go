// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import "testing"

// Poll reads the status bits and the data fault in one batch of three
// registers and agrees with Status on every one of them.
// TRLC-LINKS: REQ-SDS-040, REQ-SDS-083
func TestPollAgreesWithStatus(t *testing.T) {
	for i, flags := range []uint16{0, 4, 4 | 16, 4 | 16 | 32 | 64, 8 | 1024, 4 | 16 | 64, 4 | 16 | 64} {
		f := frozenBus()
		f.flags = flags
		if i >= 5 { // an interleaved image: the fault and lock flags count
			f.revision, f.staged[15], f.staged[19] = 6, 500, uint16(2+i-5)
		}
		c, err := New(f)
		if err != nil {
			t.Fatal(err)
		}
		full, err := c.Status()
		if err != nil {
			t.Fatal(err)
		}
		f.selReads = map[uint16]int{}
		p, err := c.Poll()
		if err != nil {
			t.Fatal(err)
		}
		if p.Ready != full.Ready || p.Frozen != full.Frozen || p.Triggered != full.Triggered || p.Running != full.Running ||
			p.Locked != full.Locked || p.Prefetched != full.Prefetched || p.DataFault != full.DataFault {
			t.Fatalf("flags %#x: poll %+v, status %+v", flags, p, full)
		}
		if i == 6 && !p.DataFault {
			t.Fatal("the data fault was not read")
		}
		if len(f.selReads) != 3 || f.selReads[1] != 1 || f.selReads[13] != 1 || f.selReads[19] != 1 {
			t.Fatalf("flags %#x: poll read %v, want registers 1, 13 and 19 once", flags, f.selReads)
		}
	}
}
