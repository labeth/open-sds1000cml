package superres

import (
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// synthetic burst/slow frames, generated ONCE in Go and fed to BOTH engines
// (the same values, so noise is baked in identically). Mirrors the web fixture.
func genFrames() (N, K, align int, frames []jframe) {
	N, K, align = 2048, 32, 0
	const edge = 200
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
	base := func(i int) float64 {
		if i < edge {
			return 128
		}
		if i < edge+5 {
			return 128 + 62*float64(i-edge)/5
		}
		return 190
	}
	burst := func(packetShift int, na float64) []int16 {
		a := make([]int16, N)
		for i := 0; i < N; i++ {
			v := base(i)
			p0 := 260 + packetShift
			if i >= p0 && i < p0+320 {
				v = 190 + 48*math.Sin(float64(i-p0)*2*math.Pi/9)
			}
			a[i] = clamp(v + na*rnd())
		}
		return a
	}
	slow := func(na float64) []int16 {
		a := make([]int16, N)
		for i := 0; i < N; i++ {
			v := base(i)
			if i >= 260 && i < 900 {
				v = 190 - 40*float64(i-260)/640
			}
			a[i] = clamp(v + na*rnd())
		}
		return a
	}
	mk := func(sig []int16) jframe { return jframe{C1: sig, C2: sig, EdgeX: edge} }
	frames = append(frames, mk(burst(0, 0))) // reference
	for _, s := range []int{0, 6, 30, -18, 12, -6} {
		frames = append(frames, mk(burst(s, 6)))
	}
	for i := 0; i < 5; i++ {
		frames = append(frames, mk(slow(6)))
	}
	return
}

type jframe struct {
	C1    []int16 `json:"c1"`
	C2    []int16 `json:"c2"`
	EdgeX float64 `json:"edgeX"`
}

type jsResult struct {
	SeedOk      bool      `json:"seedOk"`
	Disp        []string  `json:"disp"`
	Shifts      []float64 `json:"shifts"` // JSON null → 0; fine, we key off Disp
	Frames      int       `json:"frames"`
	Rejected    int       `json:"rejected"`
	BitsGained  float64   `json:"bitsGained"`
	SigmaSingle float64   `json:"sigmaSingle"`
	SigmaStack  float64   `json:"sigmaStack"`
	MeanSum     float64   `json:"meanSum"`
	MeanCount   int       `json:"meanCount"`
}

// TestParityJS asserts the Go reference-locked stacker converges to the SAME
// stack as superres.js on identical frames: same accept/reject set, same integer
// shifts, same frame/reject counts, the same mean array (sum + count), and
// bitsGained within a small log2 tolerance. Skips if node is absent.
func TestParityJS(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	N, K, align, frames := genFrames()

	// --- JS engine via the driver ---
	payload, _ := json.Marshal(map[string]any{"N": N, "K": K, "align": align, "frames": frames})
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
	if !st.SeedRef(u8(frames[0].C1), u8(frames[0].C2), frames[0].EdgeX) {
		t.Fatal("Go SeedRef failed")
	}
	if !js.SeedOk {
		t.Fatal("JS seed failed")
	}
	var goDisp []string
	var goShift []float64
	for i := 1; i < len(frames); i++ {
		before := st.Frames
		d := st.Feed(u8(frames[i].C1), u8(frames[i].C2), frames[i].EdgeX)
		goDisp = append(goDisp, d)
		if st.Frames > before {
			goShift = append(goShift, st.Shifts[len(st.Shifts)-1])
		} else {
			goShift = append(goShift, math.NaN())
		}
	}
	res := st.Result(false, 1)

	// --- compare ---
	acc := func(d string) bool { return d == "stacked" }
	if len(goDisp) != len(js.Disp) {
		t.Fatalf("disp length %d vs %d", len(goDisp), len(js.Disp))
	}
	for i := range goDisp {
		if acc(goDisp[i]) != acc(js.Disp[i]) {
			t.Errorf("frame %d accept mismatch: go=%q js=%q", i+1, goDisp[i], js.Disp[i])
		}
		if acc(goDisp[i]) && acc(js.Disp[i]) {
			if math.Abs(goShift[i]-js.Shifts[i]) > 1e-6 {
				t.Errorf("frame %d shift mismatch: go=%v js=%v", i+1, goShift[i], js.Shifts[i])
			}
		}
	}
	if st.Frames != js.Frames || st.Rejected != js.Rejected {
		t.Errorf("counts mismatch: go frames=%d rej=%d, js frames=%d rej=%d", st.Frames, st.Rejected, js.Frames, js.Rejected)
	}
	if res.MeanCountNonGap() != js.MeanCount {
		t.Errorf("mean count mismatch: go=%d js=%d", res.MeanCountNonGap(), js.MeanCount)
	}
	if d := math.Abs(res.MeanSumNonGap() - js.MeanSum); d > 1e-3 {
		t.Errorf("mean sum mismatch: go=%.6f js=%.6f (Δ%.2e)", res.MeanSumNonGap(), js.MeanSum, d)
	}
	if d := math.Abs(res.BitsGained - js.BitsGained); d > 0.02 {
		t.Errorf("bitsGained mismatch: go=%.4f js=%.4f (Δ%.4f)", res.BitsGained, js.BitsGained, d)
	}
	t.Logf("parity: frames go=%d js=%d, bits go=%.3f js=%.3f, meanSum go=%.4f js=%.4f",
		st.Frames, js.Frames, res.BitsGained, js.BitsGained, res.MeanSumNonGap(), js.MeanSum)
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
