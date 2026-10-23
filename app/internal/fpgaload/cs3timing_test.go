// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

import "testing"

// The short write cycle changes only the write fields and the gap of the
// vendor's CS3 timing (read off the scope), and leaves any other timing alone.
// TRLC-LINKS: REQ-SDS-005
func TestFastWriteChangesOnlyTheWriteFields(t *testing.T) {
	vendor := cs3Timing{c2: 0x00141400, c4: 0x10041004, c5: 0x010d141f, c6: 0x00000f80}
	got, ok := fastWrite(vendor)
	if want := (cs3Timing{c2: 0x00081400, c4: 0x06011004, c5: 0x010d081f, c6: 0x00000280}); !ok || got != want {
		t.Fatalf("got %#08x (%v), want %#08x", got, ok, want)
	}
	for _, other := range []cs3Timing{
		{c2: 0x00141400, c4: 0x10041004, c5: 0x010d101f, c6: 0x00000f80},
		{c2: 0x00141400, c4: 0x10041004, c5: 0x010d141f, c6: 0x00000280},
	} {
		if got, ok := fastWrite(other); ok || got != other {
			t.Fatalf("%#08x, not the vendor's timing, was changed", other)
		}
	}
}
