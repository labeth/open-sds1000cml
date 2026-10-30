// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import "testing"

// TestDALIFrameCutByTheRecordIsNoFrame: a DALI frame needs the line quiet
// after it past half a bit, where a next bit would move it: a 16-bit forward
// frame followed by the bus's idle decodes; the same frame ending as the
// record ends, or 300 us before, does not
// (the record's end cuts a longer burst at any bit: an Oregon sensor's burst
// cut at 16 bits read as a DALI frame).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-235
func TestDALIFrameCutByTheRecordIsNoFrame(t *testing.T) {
	const sr = 100e3
	for _, tc := range []struct {
		idle float64
		want bool
	}{{3e-3, true}, {0.6e-3, true}, {0, false}, {0.3e-3, false}} {
		w := newTimeline(sr)
		w.idle(2e-3)
		daliFrame(w, 16, 0xA501, tc.idle)
		r := DecodeDALI(w.codes(), 1/sr, DALICfg{Format: "hex"})
		if r.OK != tc.want {
			t.Errorf("idle %g s after the frame: OK %v (%q %q), want %v", tc.idle, r.OK, r.Text, r.Error, tc.want)
		}
	}
}
