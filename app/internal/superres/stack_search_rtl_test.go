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

type searchFixture struct {
	name                         string
	reference, samples           []uint8
	reject                       map[int]bool
	first, separation            uint32
	floor                        int64
	delay, abortCycle, abortKind int
	empty, invalid               bool
}

// Test the connected sample-pair -> moments -> normalization -> peak pipeline,
// not separately supplied scores. A stalled downstream verdict must preserve
// both the candidate descriptor and the next completed score.
// TRLC-LINKS: REQ-SDS-141
func TestStackSearchRTL(t *testing.T) { runStackSearchRTL(t, false) }

// TRLC-LINKS: REQ-SDS-141
func TestStackMemorySearchRTL(t *testing.T) { runStackSearchRTL(t, true) }

// TRLC-LINKS: REQ-SDS-141
func runStackSearchRTL(t *testing.T, memory bool) {
	const unit = int64(1) << 48
	dir := t.TempDir()
	rtl := filepath.Join("..", "..", "..", "fpga", "acq_sram")
	image := filepath.Join(dir, "search.vvp")
	bench := "tb_stack_search"
	if memory {
		bench = "tb_stack_memory_search"
	}
	args := []string{"-g2012", "-s", bench, "-o", image}
	for _, f := range []string{"stack_moments.v", "stack_correlation.v", "stack_match_score.v", "stack_peaks.v", "stack_search.v", "stack_window_reader.v", "stack_alignment.v", "stack_memory_search.v", "sim/" + bench + ".v"} {
		args = append(args, filepath.Join(rtl, f))
	}
	if out, err := exec.Command("iverilog", args...).CombinedOutput(); err != nil {
		t.Fatalf("compile %v: %s", err, out)
	}
	rng := rand.New(rand.NewSource(141026))
	var fixtures []searchFixture
	for _, length := range []int{4, 16, 47, 64, 193, 512} {
		ref := make([]uint8, length)
		sig := make([]uint8, length*3)
		for k := range ref {
			ref[k] = uint8(40 + rng.Intn(170))
		}
		for k := range sig {
			sig[k] = uint8(80 + rng.Intn(70))
		}
		copy(sig, ref)
		copy(sig[len(sig)-length:], ref)
		fixtures = append(fixtures, searchFixture{name: fmt.Sprintf("waveform-%d", length), reference: ref, samples: sig, separation: uint32(length / 2), floor: unit * 4 / 5})
	}
	small := fixtures[1]
	for _, name := range []string{"reject-first", "long-verdict", "reset-feed", "restart-score", "reset-held-score", "restart-held-score", "maximum-position"} {
		f := small
		f.name = name
		switch name {
		case "reject-first":
			f.reject = map[int]bool{0: true}
			f.separation = uint32(len(f.samples))
		case "long-verdict":
			f.delay = 6000
		case "reset-feed":
			f.abortCycle = 8
			f.abortKind = 1
		case "restart-score":
			f.abortCycle = 400
			f.abortKind = 2
		case "reset-held-score":
			f.abortCycle = -1
			f.abortKind = 1
			f.delay = 6000
		case "restart-held-score":
			f.abortCycle = -1
			f.abortKind = 2
			f.delay = 6000
		case "maximum-position":
			f.first = ^uint32(0) - uint32(len(f.samples)-len(f.reference))
		}
		fixtures = append(fixtures, f)
	}
	fixtures = append(fixtures,
		searchFixture{name: "flat-reference", reference: []uint8{100, 100, 100, 100}, samples: []uint8{100, 100, 100, 100, 101, 101}, floor: unit / 2},
		searchFixture{name: "flat-candidate", reference: []uint8{0, 50, 100, 255}, samples: []uint8{100, 100, 100, 100}, floor: unit / 2},
		searchFixture{name: "single-window", reference: []uint8{0, 50, 100, 255}, samples: []uint8{0, 50, 100, 255}, floor: unit},
	)
	for _, name := range []string{"empty", "short-gate", "bad-threshold", "position-overflow"} {
		f := small
		f.name = name
		f.invalid = true
		switch name {
		case "empty":
			f.empty = true
		case "short-gate":
			f.reference = f.reference[:3]
		case "bad-threshold":
			f.floor = unit + 1
		case "position-overflow":
			f.first = ^uint32(0)
		}
		fixtures = append(fixtures, f)
	}
	if memory {
		offset := small
		offset.name = "record-offset"
		offset.first = 29
		fixtures = append(fixtures, offset)
		long := fixtures[5]
		long.name = "long-gate-single-window"
		long.samples = append([]uint8(nil), long.reference...)
		fixtures = append(fixtures, long)
		for _, name := range []string{"restart-memory-read", "restart-on-response", "memory-read-error", "short-reference-memory", "short-record-memory"} {
			f := small
			f.name = name
			if name == "restart-memory-read" {
				f.abortKind = 2
				f.abortCycle = -2
			} else if name == "restart-on-response" {
				f.abortKind = 2
				f.abortCycle = -3
			} else {
				f.invalid = true
			}
			fixtures = append(fixtures, f)
		}
	}
	executed := 0
	for _, f := range fixtures {
		if memory && (f.name == "maximum-position" || f.name == "waveform-512") {
			continue
		}
		executed++
		t.Run(f.name, func(t *testing.T) {
			count := len(f.samples) - len(f.reference) + 1
			if f.empty {
				count = 0
			}
			var input strings.Builder
			fmt.Fprintf(&input, "%d %d %d %d %d %d %d %d %d\n", f.first, count, len(f.reference), f.separation, f.floor, len(f.samples), f.delay, f.abortCycle, f.abortKind)
			for _, v := range f.reference {
				fmt.Fprintf(&input, "%d\n", v)
			}
			for _, v := range f.samples {
				fmt.Fprintf(&input, "%d\n", v)
			}
			for k := 0; k < count; k++ {
				accept := 1
				if f.reject[k] {
					accept = 0
				}
				fmt.Fprintf(&input, "%d\n", accept)
			}
			file := filepath.Join(dir, "input.txt")
			if err := os.WriteFile(file, []byte(input.String()), 0600); err != nil {
				t.Fatal(err)
			}
			vargs := []string{image, "+input=" + file}
			if f.name == "memory-read-error" {
				vargs = append(vargs, "+fault=1")
			}
			if f.name == "short-reference-memory" {
				vargs = append(vargs, "+short_reference=1")
			}
			if f.name == "short-record-memory" {
				vargs = append(vargs, "+short_record=1")
			}
			out, err := exec.Command("vvp", vargs...).CombinedOutput()
			if err != nil {
				t.Fatalf("simulate %v: %s", err, out)
			}
			var got, want []peakCandidate
			var alignedPosition uint32
			var alignedDelta int64
			aligned := false
			completed := false
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if line == "R" {
					got = nil
					aligned = false
					continue
				}
				if strings.HasPrefix(line, "A ") {
					if !memory || aligned {
						t.Fatal("unexpected alignment result")
					}
					if _, err := fmt.Sscanf(line, "A %d %d", &alignedPosition, &alignedDelta); err != nil {
						t.Fatal(err)
					}
					aligned = true
					continue
				}
				if strings.HasPrefix(line, "D ") {
					var bad, cycles int
					if _, err := fmt.Sscanf(line, "D %d %d", &bad, &cycles); err != nil {
						t.Fatal(err)
					}
					if (bad != 0) != f.invalid {
						t.Fatalf("invalid=%d expected %v", bad, f.invalid)
					}
					t.Logf("%d windows, %d clocks", count, cycles)
					completed = true
					continue
				}
				var p peakCandidate
				if _, err := fmt.Sscanf(line, "H %d %d %d %d %d %d %d", &p.Position, &p.Score, &p.Left, &p.Right, &p.HasLeft, &p.HasRight, &p.Accept); err != nil {
					t.Fatalf("output %q", line)
				}
				got = append(got, p)
				if memory {
					wantDelta, bad := exactAlignment(p.Left, p.Score, p.Right, p.HasLeft != 0, p.HasRight != 0)
					if !aligned || bad || alignedPosition != p.Position || alignedDelta != wantDelta {
						t.Fatalf("alignment at %d got=%d want=%d, present=%v", p.Position, alignedDelta, wantDelta, aligned)
					}
					aligned = false
				}
			}
			if !completed {
				t.Fatal("missing completion")
			}
			if aligned {
				t.Fatal("alignment without candidate")
			}
			if !f.invalid {
				scores := make([]int64, count)
				for k := range scores {
					m := windowMoments{N: uint64(len(f.reference))}
					for j, x := range f.reference {
						a, b := uint64(x), uint64(f.samples[k+j])
						m.X += a
						m.Y += b
						m.XX += a * a
						m.YY += b * b
						m.XY += a * b
					}
					r := exactCorrelation(m)
					if r.Invalid != 0 {
						t.Fatal("oracle invalid")
					}
					scores[k] = r.Score
				}
				last := -1
				sep := int(f.separation)
				if sep < 1 {
					sep = 1
				}
				for k, sc := range scores {
					if sc < f.floor || k > 0 && scores[k-1] >= sc || k+1 < count && scores[k+1] > sc || last >= 0 && k-last < sep {
						continue
					}
					p := peakCandidate{Position: f.first + uint32(k), Score: sc, Accept: 1}
					if k > 0 {
						p.Left = scores[k-1]
						p.HasLeft = 1
					}
					if k+1 < count {
						p.Right = scores[k+1]
						p.HasRight = 1
					}
					if f.reject[k] {
						p.Accept = 0
					} else {
						last = k
					}
					want = append(want, p)
				}
				if len(f.reference) < 48 && f.reject == nil {
					ref := make([]float32, len(f.reference))
					for k, v := range f.reference {
						ref[k] = float32(v)
					}
					st := Stack{N: len(f.samples), gtpl: gateTemplate(ref, 0, len(ref)), MinMatch: float64(f.floor) / float64(unit)}
					if st.gtpl != nil && f.separation == uint32(len(ref)/2) {
						hits := st.gateFind(f.samples, 0, 0)
						if len(hits) != len(want) {
							t.Fatalf("gateFind count %d vs %d", len(hits), len(want))
						}
						for k, h := range hits {
							if uint32(h.loc)+f.first != want[k].Position {
								t.Fatalf("gateFind hit %v vs %+v", h, want[k])
							}
						}
					}
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("candidates got=%+v want=%+v", got, want)
			}
		})
	}
	t.Logf("%d connected search fixtures; memory=%v", executed, memory)
}
