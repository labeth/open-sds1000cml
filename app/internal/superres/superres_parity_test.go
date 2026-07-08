package superres

import (
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// synthetic REPETITIVE frames (triangle) plus one non-matching frame, generated
// ONCE in Go and fed to BOTH engines (same values, noise baked in identically).
// Mirrors the web multi-hit fixture: the auto-gate narrows to one period, so each
// frame yields many hits; the last frame carries a different waveform → reject.
func genFrames() (N, K, align int, frames []jframe) {
	N, K, align = 2048, 16, 0
	const edge, P = 40, 40
	var seed int64 = 12345
	rnd := func() float64 { // deterministic LCG in [-0.5,0.5]
		seed = (seed*1103515245 + 12345) & 0x7fffffff
		return float64(seed)/float64(0x7fffffff) - 0.5
	}
	clamp := func(v float64) int16 {
		r := int(math.Round(v))
		if r < 12 {
			r = 12
		}
		if r > 243 {
			r = 243
		}
		return int16(r)
	}
	tval := func(t float64) float64 {
		ph := math.Mod(math.Mod(t, P)+P, P)
		if ph < P/2 {
			return 128 + (-40 + 160*ph/P)
		}
		return 128 + (120 - 160*ph/P)
	}
	tri := func(frac, na float64) []int16 {
		a := make([]int16, N)
		for i := 0; i < N; i++ {
			a[i] = clamp(tval(float64(i)-frac) + na*rnd())
		}
		return a
	}
	other := func(na float64) []int16 { // a DIFFERENT waveform (slow ramp) → no hits
		a := make([]int16, N)
		for i := 0; i < N; i++ {
			a[i] = clamp(90 + 70*float64(i)/float64(N) + na*rnd())
		}
		return a
	}
	// decoy: a square at the triangle's period — same scale/period, different
	// shape. Exercises the segment-consistency + level checks cross-engine.
	decoy := func(na float64) []int16 {
		a := make([]int16, N)
		for i := 0; i < N; i++ {
			ph := math.Mod(math.Mod(float64(i), P)+P, P)
			v := 88.0
			if ph >= P/2 {
				v = 208
			}
			a[i] = clamp(v + na*rnd())
		}
		return a
	}
	mk := func(sig []int16) jframe { return jframe{C1: sig, C2: sig, EdgeX: edge} }
	frames = append(frames, mk(tri(0, 0))) // reference
	for _, fr := range []float64{0, 0.5, 0, 0.5} {
		frames = append(frames, mk(tri(fr, 6)))
	}
	frames = append(frames, mk(other(6))) // non-matching ramp → rejected
	frames = append(frames, mk(decoy(6))) // same-period lookalike → rejected
	return
}

type jframe struct {
	C1    []int16 `json:"c1"`
	C2    []int16 `json:"c2"`
	EdgeX float64 `json:"edgeX"`
}

type jsResult struct {
	SeedOk      bool     `json:"seedOk"`
	Disp        []string `json:"disp"`
	HitsAfter   []int    `json:"hitsAfter"`
	GridL       int      `json:"gridL"`
	GateLo      int      `json:"gateLo"`
	GateHi      int      `json:"gateHi"`
	Frames      int      `json:"frames"`
	Hits        int      `json:"hits"`
	Rejected    int      `json:"rejected"`
	BitsGained  float64  `json:"bitsGained"`
	SigmaSingle float64  `json:"sigmaSingle"`
	SigmaStack  float64  `json:"sigmaStack"`
	MeanSum     float64  `json:"meanSum"`
	MeanCount   int      `json:"meanCount"`
	Mean2Sum    float64  `json:"mean2Sum"`
	Mean2Count  int      `json:"mean2Count"`
}

// TestParityJS asserts the Go reference-locked stacker converges to the SAME
// stack as superres.js on identical frames: same accept/reject set, same integer
// shifts, same frame/reject counts, the same mean array (sum + count), and
// bitsGained within a small log2 tolerance. Skips if node is absent.
func TestParityJS(t *testing.T) { runParity(t, -1, -1) }

// TestParityManualGate pins the MANUAL-gate path cross-engine with a 3-period
// gate — wide enough to have segments, so the segment/level consistency checks
// and the decoy REJECTION run identically in both engines.
func TestParityManualGate(t *testing.T) { runParity(t, 40, 160) }

func runParity(t *testing.T, gateLo, gateHi int) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	N, K, align, frames := genFrames()

	// --- JS engine via the driver ---
	payload, _ := json.Marshal(map[string]any{"N": N, "K": K, "align": align, "frames": frames})
	if gateHi > gateLo && gateLo >= 0 {
		payload, _ = json.Marshal(map[string]any{"N": N, "K": K, "align": align, "frames": frames,
			"gate": map[string]int{"lo": gateLo, "hi": gateHi}})
	}
	tmp := filepath.Join(t.TempDir(), "frames.json")
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("node", "parity_driver.cjs", tmp).CombinedOutput()
	if err != nil {
		t.Fatalf("node driver failed: %v\n%s", err, out)
	}
	var js jsResult
	if err := json.Unmarshal(out, &js); err != nil {
		t.Fatalf("bad driver output: %v\n%s", err, out)
	}

	// --- Go engine on the same frames ---
	st := New(N, K)
	st.Align = align
	u8 := func(a []int16) []uint8 {
		b := make([]uint8, len(a))
		for i, v := range a {
			b[i] = uint8(v)
		}
		return b
	}
	if !st.SeedRefGate(u8(frames[0].C1), u8(frames[0].C2), frames[0].EdgeX, gateLo, gateHi) {
		t.Fatal("Go SeedRef failed")
	}
	if !js.SeedOk {
		t.Fatal("JS seed failed")
	}
	var goDisp []string
	var goHits []int
	for i := 1; i < len(frames); i++ {
		d := st.Feed(u8(frames[i].C1), u8(frames[i].C2), frames[i].EdgeX)
		goDisp = append(goDisp, d)
		goHits = append(goHits, st.Hits)
	}
	res := st.Result(false, 1)

	// --- compare (bit-identical: same gate, same per-frame disposition, same
	// running hit count, same mean checksum; bits within a small log2 tol) ---
	if st.GateLo != js.GateLo || st.GateHi != js.GateHi || st.GridL != js.GridL {
		t.Errorf("gate mismatch: go [%d,%d) L=%d, js [%d,%d) L=%d", st.GateLo, st.GateHi, st.GridL, js.GateLo, js.GateHi, js.GridL)
	}
	if len(goDisp) != len(js.Disp) {
		t.Fatalf("disp length %d vs %d", len(goDisp), len(js.Disp))
	}
	for i := range goDisp {
		if goDisp[i] != js.Disp[i] {
			t.Errorf("frame %d disp mismatch: go=%q js=%q", i+1, goDisp[i], js.Disp[i])
		}
		if goHits[i] != js.HitsAfter[i] {
			t.Errorf("frame %d running-hits mismatch: go=%d js=%d", i+1, goHits[i], js.HitsAfter[i])
		}
	}
	if st.Frames != js.Frames || st.Hits != js.Hits || st.Rejected != js.Rejected {
		t.Errorf("counts mismatch: go frames=%d hits=%d rej=%d, js frames=%d hits=%d rej=%d",
			st.Frames, st.Hits, st.Rejected, js.Frames, js.Hits, js.Rejected)
	}
	if res.MeanCountNonGap() != js.MeanCount {
		t.Errorf("mean count mismatch: go=%d js=%d", res.MeanCountNonGap(), js.MeanCount)
	}
	if d := math.Abs(res.MeanSumNonGap() - js.MeanSum); d > 1e-3 {
		t.Errorf("mean sum mismatch: go=%.6f js=%.6f (Δ%.2e)", res.MeanSumNonGap(), js.MeanSum, d)
	}
	// Second (non-align) channel too — the stacked X-Y / dual FFT read it, so it
	// must be bit-parity with the web just like the align channel.
	if res.Mean2CountNonGap() != js.Mean2Count {
		t.Errorf("mean2 count mismatch: go=%d js=%d", res.Mean2CountNonGap(), js.Mean2Count)
	}
	if d := math.Abs(res.Mean2SumNonGap() - js.Mean2Sum); d > 1e-3 {
		t.Errorf("mean2 sum mismatch: go=%.6f js=%.6f (Δ%.2e)", res.Mean2SumNonGap(), js.Mean2Sum, d)
	}
	if d := math.Abs(res.BitsGained - js.BitsGained); d > 0.02 {
		t.Errorf("bitsGained mismatch: go=%.4f js=%.4f (Δ%.4f)", res.BitsGained, js.BitsGained, d)
	}
	if st.Hits < 20 {
		t.Errorf("expected multi-hit (>=20 hits from repetitive frames), got %d", st.Hits)
	}
	t.Logf("parity: gate [%d,%d) L=%d, frames go=%d/js=%d, hits go=%d/js=%d, bits go=%.3f/js=%.3f, meanSum go=%.4f/js=%.4f",
		st.GateLo, st.GateHi, st.GridL, st.Frames, js.Frames, st.Hits, js.Hits, res.BitsGained, js.BitsGained, res.MeanSumNonGap(), js.MeanSum)
}

// helpers on Result for the mean checksum (sum/count of non-gap bins).
func (r Result) MeanSumNonGap() float64 {
	s := 0.0
	for _, v := range r.Mean {
		if v != -1 {
			s += float64(v)
		}
	}
	return s
}
func (r Result) MeanCountNonGap() int {
	c := 0
	for _, v := range r.Mean {
		if v != -1 {
			c++
		}
	}
	return c
}
func (r Result) Mean2SumNonGap() float64 {
	s := 0.0
	for _, v := range r.Mean2 {
		if v != -1 {
			s += float64(v)
		}
	}
	return s
}
func (r Result) Mean2CountNonGap() int {
	c := 0
	for _, v := range r.Mean2 {
		if v != -1 {
			c++
		}
	}
	return c
}
