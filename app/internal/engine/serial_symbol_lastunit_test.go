// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"testing"

	"open-sds/app/internal/decode"
)

// Every symbol-trigger protocol's frames matched on their last unit, whole:
// the request is the frame start, a wildcard for each unit before and the
// last unit's value with every bit of its width asked. The hardware's
// matches (the block's Go model) are as many as the frames the software
// decoder reads with that value there, and at least one: a frame's end (a
// Miller message's last bits, then the idle) must not keep the last unit
// out of reach.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-257
func TestSymbolTriggerMatchesLastUnit(t *testing.T) {
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
			spec, ok := symbolSpec(p)
			if !ok {
				t.Skip("no symbol spec")
			}
			tried := 0
			for _, f := range frames {
				var units []decode.Span
				clean := true
				for _, s := range f {
					switch s.Kind {
					case "data":
						units = append(units, s)
					case "frame-error", "parity-error":
						clean = false
					}
				}
				if len(units) == 0 || !clean {
					continue
				}
				last := len(units) - 1
				wd := spec.width(last)
				if wd <= 0 {
					continue
				}
				mask := 1<<min(wd, 31) - 1 // a pattern unit's 31 bits (an SWD data word's low ones)
				pat := []PatternElem{symStart}
				for i := 0; i < last; i++ {
					pat = append(pat, symAny)
				}
				pat = append(pat, symData(units[last].Val&mask, mask))
				p.Pattern = pat
				if !planHardwareSymbol(&p).Enabled {
					continue // past the block's window (a long frame on a protocol without a leader)
				}
				tried++
				// the frames whose unit at that place holds the value, its
				// check passed or not (the block checks no CRC or checksum)
				soft := 0
				for _, g := range frames {
					k := 0
					for _, s := range g {
						if s.Kind != "data" && s.Kind != "frame-error" && s.Kind != "parity-error" || s.Kind != "data" && s.Text == "!" {
							continue
						}
						if k == last && s.Val&mask == units[last].Val&mask {
							soft++
						}
						k++
					}
				}
				hard := len(symMatches(t, p, w))
				t.Logf("last unit %d of %d = %#x: software %d, hardware %d", last+1, len(units), units[last].Val&mask, soft, hard)
				if hard != soft || hard == 0 {
					t.Errorf("last unit %d of %d = %#x (mask %#x): software matches %d frames, hardware %d",
						last+1, len(units), units[last].Val&mask, mask, soft, hard)
				}
				break
			}
			if tried == 0 {
				t.Logf("no frame whose last unit the block's window reaches")
			}
		})
	}
}
