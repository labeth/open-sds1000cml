// ENGMODEL-OWNER-UNIT: FU-APP-SUPERRES
package superres

import (
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Compare actual waveform search, alignment, qualified verdicts, interpolation,
// tile updates and host codec readback against independent exact arithmetic.
// TRLC-LINKS: REQ-SDS-141
func TestStackSearchAccumulatorRTL(t *testing.T) {
	ref := []uint8{30, 80, 210, 50, 170, 240, 90, 20}
	samples := make([]uint8, 48)
	for i := range samples {
		samples[i] = uint8(100 + (i*7)%25)
	}
	for _, p := range []int{2, 18, 34} {
		copy(samples[p:], ref)
	}
	type moment struct {
		sum, square, odd *big.Int
		count, countOdd  uint32
	}
	limbs := func(v *big.Int) [2]uint64 {
		return [2]uint64{v.Uint64(), new(big.Int).Rsh(new(big.Int).Set(v), 64).Uint64()}
	}
	encode := func(m moment) FPGAStackMoments {
		return FPGAStackMoments{Sum: limbs(m.sum), SumSquares: limbs(m.square), SumOdd: limbs(m.odd), Count: m.count, CountOdd: m.countOdd}
	}
	initial := func() []moment {
		result := make([]moment, 16)
		for i := range result {
			v := big.NewInt(int64(10+i) << 24)
			result[i] = moment{new(big.Int).Mul(v, big.NewInt(2)), new(big.Int).Mul(new(big.Int).Mul(v, v), big.NewInt(2)), new(big.Int).Set(v), 2, 1}
		}
		return result
	}
	var fixture strings.Builder
	for _, v := range ref {
		fmt.Fprintln(&fixture, v)
	}
	for _, v := range samples {
		fmt.Fprintln(&fixture, v)
	}
	for _, m := range initial() {
		words, err := EncodeFPGAMoments(encode(m))
		if err != nil {
			t.Fatal(err)
		}
		packed := new(big.Int)
		for i := 19; i >= 0; i-- {
			packed.Lsh(packed, 16)
			packed.Or(packed, new(big.Int).SetUint64(uint64(words[i])))
		}
		fmt.Fprintf(&fixture, "%080x\n", packed)
	}
	scores := make([]int64, len(samples)-len(ref)+1)
	for p := range scores {
		m := windowMoments{N: uint64(len(ref))}
		for i, a := range ref {
			x, y := uint64(a), uint64(samples[p+i])
			m.X += x
			m.Y += y
			m.XX += x * x
			m.YY += y * y
			m.XY += x * y
		}
		score := exactCorrelation(m)
		if score.Invalid != 0 {
			t.Fatal("invalid oracle window")
		}
		scores[p] = score.Score
	}
	type hit struct {
		position, delta int64
		accept, mask    int
	}
	expected := func(reject, rejectAll bool) ([]hit, []FPGAStackMoments, uint32) {
		state := initial()
		var hits []hit
		last := -1000000
		count := uint32(2)
		separation := 4
		if reject {
			separation = 20
		}
		for p, score := range scores {
			if score < 1<<48 || p > 0 && scores[p-1] >= score || p+1 < len(scores) && scores[p+1] > score || p-last < separation {
				continue
			}
			var left, right int64
			if p > 0 {
				left = scores[p-1]
			}
			if p+1 < len(scores) {
				right = scores[p+1]
			}
			delta, bad := exactAlignment(left, score, right, p > 0, p+1 < len(scores))
			if bad {
				t.Fatal("invalid alignment oracle")
			}
			h := hit{int64(p), delta, 1, 3}
			if p == 18 {
				h.mask = 2
			}
			if p == 34 {
				h.mask = 1
			}
			if rejectAll || reject && p == 2 {
				h.accept = 0
			}
			hits = append(hits, h)
			if h.accept == 0 {
				continue
			}
			last = p
			count++
			for b := 0; b < 8; b++ {
				pos := (int64(p) << 24) + delta + (int64(b+3)<<24)/3
				idx, frac := pos>>24, pos&((1<<24)-1)
				if idx < 0 || idx+1 >= int64(len(samples)) {
					continue
				}
				for ch := 0; ch < 2; ch++ {
					if h.mask&(1<<ch) == 0 {
						continue
					}
					left, right := int64(samples[idx])<<24, int64(samples[idx+1])<<24
					if ch == 1 {
						left = (255 << 24) - left
						right = (255 << 24) - right
					}
					// Weighted unsigned endpoints avoid signed rounding assumptions.
					value := big.NewInt(left*((1<<24)-frac) + right*frac)
					value.Rsh(value, 24)
					m := &state[b*2+ch]
					m.sum.Add(m.sum, value)
					m.square.Add(m.square, new(big.Int).Mul(value, value))
					m.count++
					if count&1 != 0 {
						m.odd.Add(m.odd, value)
						m.countOdd++
					}
				}
			}
		}
		result := make([]FPGAStackMoments, len(state))
		for i, m := range state {
			result[i] = encode(m)
		}
		return hits, result, count
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(input, []byte(fixture.String()), 0600); err != nil {
		t.Fatal(err)
	}
	files := []string{}
	for _, name := range []string{"search_accumulator", "memory_search", "window_reader", "search", "moments", "correlation", "match_score", "peaks", "alignment", "tiled_accumulator", "tile_access", "tile_transfer", "state_tile", "resample_store", "resample", "positions", "interpolate", "accumulate", "bin_writer"} {
		files = append(files, "stack_"+name+".v")
	}
	files = append(files, "sim/tb_stack_search_accumulator.v")
	image := compileStackRTL(t, dir, "tb_stack_search_accumulator", files...)
	for _, scenario := range []struct {
		name         string
		mode, reject int
	}{{"all-hits", 0, 0}, {"rejected-hit-and-separation", 0, 1}, {"normalized-read-fault", 1, 0}, {"search-fault-during-update", 2, 0}, {"hit-count-overflow", 3, 0}, {"reset-during-update", 4, 0}, {"oversized-reference", 5, 0}, {"moment-overflow", 6, 0}, {"oversized-tile", 7, 0}, {"all-candidates-rejected", 8, 0}} {
		t.Run(scenario.name, func(t *testing.T) {
			out, err := exec.Command("vvp", image, "+input="+input, fmt.Sprintf("+mode=%d", scenario.mode), fmt.Sprintf("+reject=%d", scenario.reject)).CombinedOutput()
			if err != nil {
				t.Fatalf("simulation %v\n%s", err, out)
			}
			if !strings.Contains(string(out), "PASS search to tile") {
				t.Fatalf("incomplete simulation\n%s", out)
			}
			if scenario.mode != 0 && scenario.mode != 4 && scenario.mode != 8 {
				t.Log("fault blocked readback and further work")
				return
			}
			var hits []hit
			var states []FPGAStackMoments
			var count uint32
			for _, line := range strings.Split(string(out), "\n") {
				switch {
				case line == "R":
					hits = nil
				case strings.HasPrefix(line, "H "):
					var h hit
					if _, err := fmt.Sscanf(line, "H %d %d %d %d", &h.position, &h.delta, &h.accept, &h.mask); err != nil {
						t.Fatal(err)
					}
					hits = append(hits, h)
				case strings.HasPrefix(line, "N "):
					if _, err := fmt.Sscanf(line, "N %d", &count); err != nil {
						t.Fatal(err)
					}
				case strings.HasPrefix(line, "S "):
					packed, ok := new(big.Int).SetString(strings.TrimPrefix(line, "S "), 16)
					if !ok {
						t.Fatal(line)
					}
					var words [20]uint16
					for i := range words {
						words[i] = uint16(packed.Uint64())
						packed.Rsh(packed, 16)
					}
					m, err := DecodeFPGAMoments(words[:])
					if err != nil {
						t.Fatal(err)
					}
					states = append(states, m)
				}
			}
			wantHits, wantState, wantCount := expected(scenario.reject != 0, scenario.mode == 8)
			if !reflect.DeepEqual(hits, wantHits) || count != wantCount {
				t.Fatalf("hits=%+v count=%d want=%+v count=%d", hits, count, wantHits, wantCount)
			}
			if !reflect.DeepEqual(states, wantState) {
				t.Fatalf("state got=%+v want=%+v", states, wantState)
			}
			t.Logf("%d qualified candidates, hit count %d, 16 exact host-decoded channel/bin states", len(hits), count)
		})
	}
}
