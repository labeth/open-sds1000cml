// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"testing"

	"open-sds/app/internal/decode"
)

// The symbol trigger against the protocols' software decoders on the same
// synthetic buses: per request, the frames the decoder opens (its "sof"
// spans) and matches with the sequence semantics are as many as the frames
// the hardware matches.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestSymbolTriggerAgreesWithDecoders(t *testing.T) {
	for _, tc := range symbolCases() {
		t.Run(tc.name, func(t *testing.T) {
			w := tc.wave()
			d, ok := serialDecoder(tc.p.Proto)
			if !ok {
				t.Fatal("no software decoder registered")
			}
			code := func(v []bool) []uint8 {
				out := make([]uint8, len(v))
				for i, x := range v {
					out[i] = 50
					if x {
						out[i] = 200
					}
				}
				return out
			}
			p := tc.p
			res := d.Decode(code(w.a), code(w.b), 8e-9, p)
			var frames [][]decode.Span
			for _, s := range res.Spans {
				if s.Kind == "sof" {
					frames = append(frames, nil)
				}
				if len(frames) > 0 {
					frames[len(frames)-1] = append(frames[len(frames)-1], s)
				}
			}
			for _, r := range tc.reqs {
				p.Pattern = r.pattern
				soft := 0
				for _, f := range frames {
					if ok, _ := matchSequence(f, p); ok {
						soft++
					}
				}
				hard := len(symMatches(t, p, w))
				t.Logf("%v: software %d of %d frames, hardware %d", r.pattern, soft, len(frames), hard)
				if soft != hard {
					t.Errorf("pattern %v: software matches %d frames, hardware %d", r.pattern, soft, hard)
				}
			}
		})
	}
}
