// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import "testing"

// peakEnvelope is a peak-detect envelope of src: each bucket's extremes, in
// time order, as the live readout holds them.
// TRLC-LINKS: REQ-SDS-018
func peakEnvelope(src []uint8, bucket int) []uint8 {
	n := len(src) / bucket
	out := make([]uint8, 0, 2*n)
	last := src[0]
	for b := 0; b < n; b++ {
		lo, hi := uint8(255), uint8(0)
		for _, v := range src[b*bucket : (b+1)*bucket] {
			lo, hi = min(lo, v), max(hi, v)
		}
		if max(last, hi)-min(last, hi) < max(last, lo)-min(last, lo) {
			out = append(out, hi, lo)
			last = lo
		} else {
			out = append(out, lo, hi)
			last = hi
		}
	}
	return out
}

// A Wiegand pulse, a twentieth of its interval, survives an envelope bucket
// wider than itself: a frame decodes from five points an interval, so the
// envelope's feature is the interval (AUTO then shows a whole frame).
// TRLC-LINKS: REQ-SDS-018
func TestWiegandFromEnvelope(t *testing.T) {
	for _, interval := range []float64{1e-3, 2e-3} {
		l := newTwoLine(1e6)
		l.add(1, 1, 3.37e-3)
		wiegandFrame(l, wiegandBits(26, 12, 4660), interval)
		code := func(b []byte) []uint8 {
			o := make([]uint8, len(b))
			for i, v := range b {
				o[i] = 40 + 170*v
			}
			return o
		}
		bucket := int(interval * 1e6 / 2.5)
		r := DecodeWiegand(peakEnvelope(code(l.a.bits), bucket), peakEnvelope(code(l.b.bits), bucket), float64(bucket)*1e-6/2, ClockedCfg{Format: "hex"})
		if !r.OK || r.Text != "26b F12 C4660" {
			t.Fatalf("interval %v: %q %q", interval, r.Text, r.Error)
		}
		if fs := EnvelopeFeatureSamples(r); fs < 4 || fs > 6 {
			t.Fatalf("interval %v: envelope feature %.1f points, want the interval's 5", interval, fs)
		}
		if fs := FeatureSamples(r); fs > 1 {
			t.Fatalf("interval %v: sampled feature %.2f, want the pulse's", interval, fs)
		}
	}
}
