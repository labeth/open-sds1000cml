// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math/rand"
	"testing"
)

// boxDown averages a 2 ns record over box samples from a random phase, with
// a little noise, as a decimated record of the box image holds the line.
// TRLC-LINKS: REQ-SDS-018
func boxDown(w []uint8, box int, rng *rand.Rand) []uint8 {
	var d []uint8
	for i := rng.Intn(box); i+box <= len(w); i += box {
		s := 0
		for k := 0; k < box; k++ {
			s += int(w[i+k]) + rng.Intn(9) - 4
		}
		d = append(d, uint8(s/box))
	}
	return d
}

// FlexRay at 10 Mbit/s, MIL-STD-1553 and Manchester at 1 Mbit/s decode every
// message from records box-averaged to 3.1 samples a bit (500us/div and
// 10ms/div's own records), and refuse fewer than three: their floor.
// TRLC-LINKS: REQ-SDS-018
func TestLineCodesDecodeAtThreeSamplesABit(t *testing.T) {
	for seed := 0; seed < 50; seed++ {
		rng := rand.New(rand.NewSource(int64(seed)))
		var frames [][]int
		var all []int
		for f := 0; f < 3; f++ {
			b := brFlexFixCRC([]int{rng.Intn(256), rng.Intn(256), rng.Intn(256), rng.Intn(256), rng.Intn(256), rng.Intn(256), rng.Intn(256), rng.Intn(256)})
			frames, all = append(frames, b), append(all, b...)
		}
		fw := flexrayWave(frames, 50, 8) // 100 ns bits at 2 ns
		if r := DecodeFlexRay(boxDown(fw, 16, rng), 32e-9, FlexRayCfg{}); !r.OK || fmt.Sprint(r.Bytes) != fmt.Sprint(all) {
			t.Fatalf("seed %d: FlexRay at 3.1 samples a bit: %v %q", seed, r.Bytes, r.Error)
		}
		if r := DecodeFlexRay(boxDown(fw, 20, rng), 40e-9, FlexRayCfg{Bitrate: 10000000}); r.OK {
			t.Fatalf("seed %d: FlexRay at 2.5 samples a bit decoded", seed)
		}
		words := []int{rng.Intn(65536), rng.Intn(65536), rng.Intn(65536)}
		mw := mil1553Wave(words, []bool{true, false, false}, []int{mil1553OddParity(words[0]), mil1553OddParity(words[1]), mil1553OddParity(words[2])}, 500)
		r := DecodeMIL1553(boxDown(mw, 160, rng), 320e-9, MIL1553Cfg{Bitrate: 1000000})
		var got []int
		for _, s := range r.Spans {
			if s.Kind == "data" {
				got = append(got, s.Val)
			}
		}
		if !r.OK || fmt.Sprint(got) != fmt.Sprint(words) {
			t.Fatalf("seed %d: 1553 at 3.1 samples a bit: %v want %v %q", seed, got, words, r.Error)
		}
		bytes := []int{rng.Intn(256), rng.Intn(256), rng.Intn(256), rng.Intn(256)}
		cfg := ManchesterCfg{Bitrate: 1000000, IEEE: true, MSB: true, Bits: 8}
		if r := DecodeManchester(boxDown(manchesterWave(mBits(bytes, true, 8), true, 500), 160, rng), 320e-9, cfg); !r.OK || fmt.Sprint(r.Bytes) != fmt.Sprint(bytes) {
			t.Fatalf("seed %d: Manchester at 3.1 samples a bit: %v want %v %q", seed, r.Bytes, bytes, r.Error)
		}
	}
}
