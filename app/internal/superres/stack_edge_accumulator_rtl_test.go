// ENGMODEL-OWNER-UNIT: FU-APP-SUPERRES
package superres

import (
	"fmt"
	"math"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// edgeHit is one reference crossing: position is pre-shifted, delta is Q24.
type edgeHit struct{ position, delta int64 }

// edgeQualifier is the reference for the FPGA template qualifier
// (ADR-STACKING-TEMPLATE-QUALIFIER): the sum of absolute differences between
// the raw align channel at crossing-pre+k*stride and the template must stay
// within threshold, with the whole window inside the record.
type edgeQualifier struct {
	count, stride, pre, threshold int64
	template                      []uint8
}

// TRLC-LINKS: REQ-SDS-141
func (q *edgeQualifier) accept(wave [][2]uint8, ch int, crossing, n int64) bool {
	if crossing < q.pre || q.count == 0 || q.stride == 0 {
		return false
	}
	idx, sad := crossing-q.pre, int64(0)
	for k := int64(0); k < q.count; k++ {
		if idx > n-2 {
			return false
		}
		d := int64(wave[idx][ch]) - int64(q.template[k])
		if d < 0 {
			d = -d
		}
		if sad += d; sad > q.threshold {
			return false
		}
		idx += q.stride
	}
	return true
}

// edgeScan is the reference for stack_edge_hits plus the optional qualifier.
// Rejected candidates do not restart the separation hold-off.
// TRLC-LINKS: REQ-SDS-141
func edgeScan(wave [][2]uint8, first, n, pre, separation int64, ch int, falling bool, level, hysteresis uint8, qual *edgeQualifier) (hits []edgeHit, crossings, rejected int) {
	code := func(i int64) int64 {
		v := wave[i][ch]
		if falling {
			v = ^v
		}
		return int64(v)
	}
	threshold := int64(level)
	if falling {
		threshold = int64(^level)
	}
	low := int64(0)
	if threshold > int64(hysteresis) {
		low = threshold - int64(hysteresis)
	}
	armed, haveLast, last := false, false, int64(0)
	for i := first; i <= n-2; i++ {
		a, b := code(i), code(i+1)
		armed = armed || a <= low
		if !(armed && a < threshold && b >= threshold) {
			continue
		}
		armed = false
		crossings++
		q := ((threshold - a) << 24) / (b - a)
		position, delta := i, q
		if q > 1<<23 {
			position, delta = i+1, q-(1<<24)
		}
		if position < pre || (haveLast && position-last < separation) {
			continue
		}
		if qual != nil && !qual.accept(wave, ch, position, n) {
			rejected++
			continue
		}
		hits = append(hits, edgeHit{position - pre, delta})
		last, haveLast = position, true
	}
	return hits, crossings, rejected
}

// TRLC-LINKS: REQ-SDS-141
func TestStackEdgeAccumulatorRTL(t *testing.T) {
	const n, bins = 3000, 16
	wave := make([][2]uint8, n)
	// C1: a random train of wide "A" pulses (4-sample rise to 200, 20 high) and
	// narrow "B" pulses (2-sample rise to 170, 6 high): repeating edges whose
	// surrounding waveform differs, the case the template qualifier is for.
	pulseTrain := make([]float64, n)
	for i, pick := 0, uint32(99); i < n; {
		for k := 0; k < 30 && i < n; k, i = k+1, i+1 {
			pulseTrain[i] = 40
		}
		pick = pick*1664525 + 1013904223
		rise, top, hold := 4, 200.0, 20
		if pick>>31 == 1 {
			rise, top, hold = 2, 170, 6
		}
		for k := 1; k <= rise && i < n; k, i = k+1, i+1 {
			pulseTrain[i] = 40 + (top-40)*float64(k)/float64(rise)
		}
		for k := 0; k < hold && i < n; k, i = k+1, i+1 {
			pulseTrain[i] = top
		}
		for k := 1; k <= rise && i < n; k, i = k+1, i+1 {
			pulseTrain[i] = top - (top-40)*float64(k)/float64(rise)
		}
	}
	seed := uint32(12345)
	for i := range wave {
		seed = seed*1103515245 + 12345
		noise := float64(int(seed>>16)%7 - 3)
		phase := 2 * math.Pi * float64(i) / 37.3
		c0 := pulseTrain[i] + noise
		c1 := math.Floor(128 + 60*math.Sin(phase+1) + 0.5)
		wave[i] = [2]uint8{uint8(math.Max(0, math.Min(255, c0))), uint8(math.Max(0, math.Min(255, c1)))}
	}
	type moment struct {
		s, q, a *big.Int
		n, na   uint32
	}
	var moments [bins][2]moment
	for b := range moments {
		for c := range moments[b] {
			moments[b][c] = moment{new(big.Int), new(big.Int), new(big.Int), 0, 0}
		}
	}
	accumulate := func(hits []edgeHit, initial int64, firstBin, factor int64, mask int) {
		for k, h := range hits {
			odd := (initial+int64(k))%2 == 0
			for b := int64(0); b < bins; b++ {
				pos := (h.position << 24) + h.delta + ((firstBin+b)<<24)/factor
				if pos < 0 || pos>>24 >= n-1 {
					continue
				}
				idx, frac := pos>>24, pos&(1<<24-1)
				for c := 0; c < 2; c++ {
					if mask&(1<<c) == 0 {
						continue
					}
					left, right := int64(wave[idx][c]), int64(wave[idx+1][c])
					v := big.NewInt((left << 24) + (right-left)*frac)
					m := &moments[b][c]
					m.s.Add(m.s, v)
					m.q.Add(m.q, new(big.Int).Mul(v, v))
					m.n++
					if odd {
						m.a.Add(m.a, v)
						m.na++
					}
				}
			}
		}
	}
	limbs := func(x *big.Int) [2]uint64 {
		return [2]uint64{x.Uint64(), new(big.Int).Rsh(new(big.Int).Set(x), 64).Uint64()}
	}
	// The register-level port bench runs the first scan, its tile and the peeks;
	// the engine bench runs every case.
	var input, portInput strings.Builder
	portToo := true
	type run struct {
		hits, crossings, rejected int
	}
	runs := []run{}
	expected := [][]FPGAStackMoments{}
	snapshot := func() {
		var tile []FPGAStackMoments
		for b := 0; b < bins; b++ {
			for c := 0; c < 2; c++ {
				m := moments[b][c]
				tile = append(tile, FPGAStackMoments{Sum: limbs(m.s), SumSquares: limbs(m.q), SumOdd: limbs(m.a), Count: m.n, CountOdd: m.na})
				fmt.Fprintf(&input, "r %d %d\n", b, c)
				if portToo {
					fmt.Fprintf(&portInput, "r %d %d\n", b, c)
				}
			}
		}
		expected = append(expected, tile)
	}
	scan := func(first, pre, separation int64, ch int, falling bool, level, hysteresis uint8, firstBin, factor, initial int64, mask int, qual *edgeQualifier) int {
		hits, crossings, rejected := edgeScan(wave, first, n, pre, separation, ch, falling, level, hysteresis, qual)
		f := 0
		if falling {
			f = 1
		}
		line := "q 0 0 1 0 0\n"
		if qual != nil {
			line = fmt.Sprintf("t %d", len(qual.template))
			for _, v := range qual.template {
				line += fmt.Sprintf(" %d", v)
			}
			line += fmt.Sprintf("\nq 1 %d %d %d %d\n", qual.count, qual.stride, qual.pre, qual.threshold)
		}
		line += fmt.Sprintf("s %d %d %d %d %d %d %d %d %d %d %d %d %d\n", first, n, pre, separation, ch, f, level, hysteresis, bins, firstBin, factor, initial, mask)
		input.WriteString(line)
		if portToo {
			portInput.WriteString(line)
		}
		accumulate(hits, initial, firstBin, factor, mask)
		runs = append(runs, run{int(initial) + len(hits), crossings, rejected})
		return len(hits)
	}
	// Template: the waveform around the first wide pulse's rising crossing.
	// A wide pulse is still high 12 samples after its crossing; a narrow one
	// has fallen back.
	wide := func(crossing int64) bool { return crossing+12 < n && wave[crossing+12][0] > 150 }
	all, _, _ := edgeScan(wave, 0, n, 0, 0, 0, false, 128, 10, nil)
	anchor := int64(-1)
	for _, h := range all {
		if wide(h.position) && h.position > 40 {
			anchor = h.position
			break
		}
	}
	if anchor < 0 {
		t.Fatal("no wide pulse in the test waveform")
	}
	templateAt := func(count, stride, pre int64) []uint8 {
		out := make([]uint8, count)
		for k := range out {
			out[k] = wave[anchor-pre+int64(k)*stride][0]
		}
		return out
	}
	shapeA := &edgeQualifier{count: 24, stride: 1, pre: 4, threshold: 400, template: templateAt(24, 1, 4)}
	qualified, _, rejectedB := edgeScan(wave, 0, n, 5, 20, 0, false, 128, 10, shapeA)
	if len(qualified) == 0 || rejectedB == 0 {
		t.Fatalf("qualifier fixture: %d accepted, %d rejected", len(qualified), rejectedB)
	}
	for _, h := range qualified {
		if !wide(h.position + 5) {
			t.Fatalf("reference accepted a narrow pulse at %d", h.position+5)
		}
	}
	t.Logf("template qualifier: %d wide-pulse hits accepted, %d narrow-pulse candidates rejected", len(qualified), rejectedB)
	// Only wide-pulse rising edges on CH1 (narrow pulses rejected by the
	// template), then all rising CH1 edges on top, then falling CH2 edges,
	// with continued odd/even numbering.
	scan(0, 5, 20, 0, false, 128, 10, 0, 4, 0, 3, shapeA)
	snapshot()
	scan(0, 5, 20, 0, false, 128, 10, 0, 4, int64(runs[0].hits), 3, nil)
	snapshot()
	portToo = false
	scan(7, 3, 30, 1, true, 140, 6, 2, 3, int64(runs[1].hits), 1, nil)
	snapshot()
	// A strided template window, and a window reaching before the first
	// crossings, which rejects them.
	scan(0, 5, 20, 0, false, 128, 10, 0, 4, int64(runs[2].hits), 3,
		&edgeQualifier{count: 10, stride: 3, pre: 6, threshold: 150, template: templateAt(10, 3, 6)})
	scan(0, 5, 20, 0, false, 128, 10, 0, 4, int64(runs[3].hits), 3,
		&edgeQualifier{count: 24, stride: 1, pre: 40, threshold: 400, template: templateAt(24, 1, 40)})
	snapshot()
	// Clean CH2 crossings are 37 or 38 samples apart: separation 38 sits on the boundary.
	scan(0, 0, 38, 1, false, 128, 20, 0, 2, int64(runs[4].hits), 2, nil)
	snapshot()
	// A level no sample reaches produces no crossings and leaves the tile unchanged.
	scan(0, 0, 0, 0, false, 255, 0, 0, 1, 0, 3, nil)
	peeks := []int{0, 1, 17, n - 2}
	for _, p := range peeks {
		fmt.Fprintf(&input, "p %d\n", p)
		fmt.Fprintf(&portInput, "p %d\n", p)
	}
	dir := t.TempDir()
	var hex strings.Builder
	for _, w := range wave {
		fmt.Fprintf(&hex, "%02x%02x\n", w[1], w[0])
	}
	wavePath, inputPath, portPath := filepath.Join(dir, "wave.hex"), filepath.Join(dir, "input.txt"), filepath.Join(dir, "port.txt")
	if err := os.WriteFile(portPath, []byte(portInput.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wavePath, []byte(hex.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, []byte(input.String()), 0600); err != nil {
		t.Fatal(err)
	}
	engine := []string{"stack_edge_accumulator.v", "stack_edge_hits.v", "stack_tiled_accumulator.v", "stack_tile_access.v", "stack_tile_transfer.v", "stack_state_tile.v", "stack_resample_store.v", "stack_resample.v", "stack_positions.v", "stack_interpolate.v", "stack_accumulate.v", "stack_bin_writer.v"}
	cache := []string{"stack_engine_port.v", "stack_record_cache.v", "transport.v", "sim/ddr_model.v"}
	check := func(bench string, out []byte, wantRuns, wantTiles int) {
		t.Helper()
		var tiles, done, peeked int
		var state []FPGAStackMoments
		for _, line := range strings.Split(string(out), "\n") {
			switch {
			case strings.HasPrefix(line, "D "):
				var hits, crossings, invalid, rejected int
				fmt.Sscanf(line, "D %d %d %d %d", &hits, &crossings, &invalid, &rejected)
				if done >= len(runs) || invalid != 0 || hits != runs[done].hits || crossings != runs[done].crossings || rejected != runs[done].rejected {
					t.Fatalf("%s run %d: got %q, want hits %+v", bench, done, line, runs)
				}
				done++
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
				got, err := DecodeFPGAMoments(words[:])
				if err != nil {
					t.Fatal(err)
				}
				state = append(state, got)
				if len(state) == 2*bins {
					for i := range state {
						if state[i] != expected[tiles][i] {
							t.Fatalf("tile %d bin %d ch %d: got %+v want %+v", tiles, i/2, i%2, state[i], expected[tiles][i])
						}
					}
					tiles++
					state = nil
				}
			case strings.HasPrefix(line, "P "):
				var data uint32
				var invalid int
				fmt.Sscanf(line, "P %x %d", &data, &invalid)
				p := peeks[peeked]
				want := uint32(wave[p][0]) | uint32(wave[p][1])<<8 | uint32(wave[p+1][0])<<16 | uint32(wave[p+1][1])<<24
				if invalid != 0 || data != want {
					t.Fatalf("peek %d: got %q want %08x", p, line, want)
				}
				peeked++
			}
		}
		if done != wantRuns || tiles != wantTiles || peeked != len(peeks) || !strings.Contains(string(out), "PASS ") {
			t.Fatalf("%s incomplete simulation: runs %d tiles %d peeks %d\n%s", bench, done, tiles, peeked, out)
		}
	}
	image := compileStackRTL(t, dir, "tb_stack_edge_accumulator", append(engine, "sim/tb_stack_edge_accumulator.v")...)
	out, err := exec.Command("vvp", image, "+wave="+wavePath, "+input="+inputPath).CombinedOutput()
	if err != nil {
		t.Fatalf("simulation %v\n%s", err, out)
	}
	check("engine", out, len(runs), len(expected))
	image = compileStackRTL(t, dir, "tb_stack_engine_port", append(append(engine, cache...), "sim/tb_stack_engine_port.v")...)
	out, err = exec.Command("vvp", image, "+wave="+wavePath, "+input="+portPath, fmt.Sprintf("+samples=%d", n)).CombinedOutput()
	if err != nil {
		t.Fatalf("port simulation %v\n%s", err, out)
	}
	check("port", out, 2, 2)
	if !strings.Contains(string(out), "PASS engine port") {
		t.Fatalf("port bench incomplete\n%s", out)
	}
	t.Logf("both benches: %d edge-locked hits over %d runs, %d exact tile downloads, 4 peeks, SRAM upload round trip and grant revocation", runs[len(runs)-2].hits, len(runs), len(expected))
}
