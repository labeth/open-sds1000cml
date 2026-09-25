// ENGMODEL-OWNER-UNIT: FU-APP-SUPERRES
package superres

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type peakFixture struct {
	name       string
	scores     []int64
	reject     map[int]bool
	undefined  map[int]bool
	first      uint32
	separation uint32
	floor      int64
	abort      int
	bad        int
	invalid    bool
}
type peakCandidate struct {
	Position                  uint32
	Score, Left, Right        int64
	HasLeft, HasRight, Accept int
}

// TRLC-LINKS: REQ-SDS-141
func TestStackPeaksRTL(t *testing.T) {
	const unit = int64(1) << 48
	dir := t.TempDir()
	rtl := filepath.Join("..", "..", "..", "fpga", "acq_sram")
	image := filepath.Join(dir, "peaks.vvp")
	out, err := exec.Command("iverilog", "-g2012", "-s", "tb_stack_peaks", "-o", image, filepath.Join(rtl, "stack_peaks.v"), filepath.Join(rtl, "sim", "tb_stack_peaks.v")).CombinedOutput()
	if err != nil {
		t.Fatalf("compile %v: %s", err, out)
	}
	fixtures := []peakFixture{
		{name: "single", scores: []int64{unit}, first: 17},
		{name: "plateau", scores: []int64{unit, unit, unit}},
		{name: "both-boundaries", scores: []int64{unit, 0, unit}, first: 92},
		{name: "threshold-equality", scores: []int64{unit / 2, 0, unit / 2}, floor: unit / 2},
		{name: "reject-does-not-suppress", scores: []int64{unit, 0, unit, 0, unit}, separation: 4, reject: map[int]bool{0: true}},
		{name: "accepted-separation", scores: []int64{unit, 0, unit, 0, unit}, separation: 4},
		{name: "undefined-is-zero", scores: []int64{unit, unit, unit}, undefined: map[int]bool{1: true}},
		{name: "signed", scores: []int64{-unit, -unit / 2, -unit, -unit / 2}, floor: -unit / 2},
		{name: "reset-pending", scores: []int64{unit, 0, unit}, abort: 1},
		{name: "restart-pending", scores: []int64{unit, 0, unit}, abort: 2},
		{name: "separation-past-position-range", scores: []int64{unit, 0, unit}, first: ^uint32(0) - 2, separation: 4},
		{name: "maximum-separation", scores: []int64{unit, 0, unit}, separation: ^uint32(0)},
		{name: "last-position", scores: []int64{unit}, first: ^uint32(0)},
		{name: "position-overflow", scores: []int64{0, unit}, first: ^uint32(0), invalid: true},
		{name: "invalid-score", scores: []int64{unit}, bad: 1, invalid: true},
		{name: "score-range", scores: []int64{unit + 1}, invalid: true},
		{name: "threshold-range", scores: []int64{unit}, floor: unit + 1, invalid: true},
	}
	rng := rand.New(rand.NewSource(141025))
	for n := 0; n < 80; n++ {
		f := peakFixture{name: fmt.Sprintf("random-%d", n), first: uint32(n * 97), separation: uint32(rng.Intn(30)), floor: int64(rng.Intn(17)-8) * unit / 8, reject: map[int]bool{}, undefined: map[int]bool{}}
		for k := 0; k < 1+rng.Intn(200); k++ {
			f.scores = append(f.scores, int64(rng.Intn(33)-16)*unit/16)
			f.reject[k] = rng.Intn(3) == 0
			f.undefined[k] = rng.Intn(9) == 0
		}
		fixtures = append(fixtures, f)
	}
	// Use the existing full gateFind implementation as a second oracle on actual
	// waveforms. Gates shorter than 48 samples have no multi-segment veto.
	for n := 0; n < 12; n++ {
		ref := make([]float32, 16)
		sig := make([]uint8, 160)
		for i := range ref {
			ref[i] = float32(60 + rng.Intn(130))
		}
		for i := range sig {
			sig[i] = uint8(100 + rng.Intn(20))
		}
		for _, loc := range []int{0, 45, 90, 144} {
			for j := range ref {
				sig[loc+j] = uint8(ref[j])
			}
		}
		f := peakFixture{name: fmt.Sprintf("gateFind-waveform-%d", n), floor: unit * 4 / 5, separation: 8}
		for loc := 0; loc <= len(sig)-len(ref); loc++ {
			m := windowMoments{N: uint64(len(ref))}
			for j, x := range ref {
				a, b := uint64(x), uint64(sig[loc+j])
				m.X += a
				m.Y += b
				m.XX += a * a
				m.YY += b * b
				m.XY += a * b
			}
			f.scores = append(f.scores, exactCorrelation(m).Score)
		}
		st := Stack{N: len(sig), gtpl: gateTemplate(ref, 0, len(ref)), MinMatch: .8}
		hits := st.gateFind(sig, 0, 0)
		var want []int
		for _, h := range hits {
			want = append(want, h.loc)
		}
		var got []int
		last := -1000000
		for k, sc := range f.scores {
			if sc < f.floor || k > 0 && f.scores[k-1] >= sc || k+1 < len(f.scores) && f.scores[k+1] > sc || k-last < int(f.separation) {
				continue
			}
			got = append(got, k)
			last = k
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("waveform policy differs: got %v want %v", got, want)
		}
		fixtures = append(fixtures, f)
	}
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			var input strings.Builder
			fmt.Fprintf(&input, "%d %d %d %d %d\n", f.first, f.separation, f.floor, len(f.scores), f.abort)
			values := append([]int64(nil), f.scores...)
			for k, sc := range f.scores {
				defined, bad, accept := 1, 0, 1
				if f.undefined[k] {
					defined = 0
					values[k] = 0
				}
				if f.bad == k+1 {
					bad = 1
				}
				if f.reject[k] {
					accept = 0
				}
				fmt.Fprintf(&input, "%d %d %d %d %d\n", sc, defined, bad, accept, k%7)
			}
			file := filepath.Join(dir, "input.txt")
			if err := os.WriteFile(file, []byte(input.String()), 0600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command("vvp", image, "+input="+file).CombinedOutput()
			if err != nil {
				t.Fatalf("simulate %v: %s", err, out)
			}
			var got, want []peakCandidate
			completed := false
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if strings.HasPrefix(line, "D ") {
					expected := 0
					if f.invalid {
						expected = 1
					}
					if line != fmt.Sprintf("D %d", expected) {
						t.Fatalf("completion %q", line)
					}
					completed = true
					continue
				}
				var p peakCandidate
				if _, err := fmt.Sscanf(line, "H %d %d %d %d %d %d %d", &p.Position, &p.Score, &p.Left, &p.Right, &p.HasLeft, &p.HasRight, &p.Accept); err != nil {
					t.Fatalf("output %q", line)
				}
				got = append(got, p)
			}
			if !completed {
				t.Fatal("missing completion")
			}
			if !f.invalid {
				// Float policy is deliberately separate from the RTL's signed fixed-point
				// comparisons; binary fractions in these fixtures are exactly representable.
				last := -1
				sep := int(f.separation)
				if sep < 1 {
					sep = 1
				}
				for k, sc := range values {
					v := float64(sc) / float64(unit)
					if v < float64(f.floor)/float64(unit) || k > 0 && float64(values[k-1])/float64(unit) >= v || k+1 < len(values) && float64(values[k+1])/float64(unit) > v || last >= 0 && k-last < sep {
						continue
					}
					p := peakCandidate{Position: f.first + uint32(k), Score: sc, Accept: 1}
					if k > 0 {
						p.Left = values[k-1]
						p.HasLeft = 1
					}
					if k+1 < len(values) {
						p.Right = values[k+1]
						p.HasRight = 1
					}
					if f.reject[k] {
						p.Accept = 0
					} else {
						last = k
					}
					want = append(want, p)
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("candidates got=%+v want=%+v", got, want)
			}
		})
	}
	t.Logf("%d score-stream fixtures, including 12 actual-waveform gateFind comparisons", len(fixtures))
}
