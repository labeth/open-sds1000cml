// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"math/rand"
	"strings"
	"testing"
)

// TestStreamDecoderWindowBounded streams each gap-free protocol's decoder
// lines that are no bus of it - noise of several densities, a fast square,
// a slow one, a line stuck low - in pushes as the engine makes them: the
// window the decoder re-reads on every push stays within Max and a push,
// so neither memory nor the decode's cost grows with the session.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestStreamDecoderWindowBounded(t *testing.T) {
	for _, c := range append(append([]streamCase(nil), streamCases...), moreStreamCases...) {
		name := strings.TrimSuffix(c.name, "-carrier")
		feature, longest, quiet, ok := StreamTiming(name, c.baud)
		if !ok {
			continue
		}
		log, ok := StreamBucketLog(feature)
		if !ok {
			continue
		}
		ss := float64(uint64(1)<<log) * 2e-9 / 2
		closeN, maxN := int(quiet/ss), int((longest+quiet)/ss)
		push := max(2*closeN, int(0.2/ss)) // the engine's push
		dec := func(a, b []uint8) Result { return c.dec(a, b, ss) }
		rng := rand.New(rand.NewSource(int64(len(c.name))))
		lines := map[string]func(i int) uint8{
			"noise":  func(int) uint8 { return uint8(rng.Intn(2) * 255) },
			"sparse": func(int) uint8 { return uint8(255 * min(1, rng.Intn(40))) },
			"fast":   func(i int) uint8 { return uint8(255 * (i & 1)) },
			"slow":   func(i int) uint8 { return uint8(255 * (i / max(1, closeN/3) & 1)) },
			"low":    func(int) uint8 { return 0 },
		}
		for kind, gen := range lines {
			d := NewStreamDecoder(dec, closeN, maxN)
			d.SetLead(int(StreamLead(name, c.baud) / ss))
			total := 6 * (maxN + push)
			worst := 0
			for at := 0; at < total; at += push {
				a, b := make([]uint8, push), make([]uint8, push)
				for i := range a {
					a[i], b[i] = gen(at+i), gen(at+i+7)
				}
				if c.lines != 2 {
					b = nil
				}
				d.Push(a, b)
				worst = max(worst, d.Held())
			}
			if limit := 2*max(maxN, 4*max(closeN, 2)) + int(StreamLead(name, c.baud)/ss) + push; worst > limit {
				t.Errorf("%s on a %s line: the window grew to %d samples (Max %d, push %d)", c.name, kind, worst, maxN, push)
			}
		}
	}
}
