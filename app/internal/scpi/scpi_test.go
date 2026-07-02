package scpi

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
)

type fakeScope struct {
	stats engine.Stats
	frame *engine.Frame
	calls []string
}

func (f *fakeScope) Snapshot() engine.Stats           { return f.stats }
func (f *fakeScope) WithFrame(fn func(*engine.Frame)) { fn(f.frame) }
func (f *fakeScope) SetRunning(on bool)               { f.calls = append(f.calls, "run") }
func (f *fakeScope) SetNorm(on bool)                  { f.calls = append(f.calls, "norm") }
func (f *fakeScope) SetSingle()                       { f.calls = append(f.calls, "single") }
func (f *fakeScope) SetTdiv(t float64) (engine.Band, bool) {
	f.calls = append(f.calls, "tdiv")
	return engine.PlanTdiv(t)
}
func (f *fakeScope) SetTrigLevelCode(c uint16) uint16 {
	f.calls = append(f.calls, "trlv")
	return c
}
func (f *fakeScope) SetTrigSlope(r bool)           { f.calls = append(f.calls, "slope") }
func (f *fakeScope) SetTrigSource(ch int)          { f.calls = append(f.calls, "src") }
func (f *fakeScope) SetOffsetDAC(ch int, c uint16) { f.calls = append(f.calls, "ofst") }
func (f *fakeScope) SetAcqMode(m int)              { f.calls = append(f.calls, "acq") }
func (f *fakeScope) SetAvgCount(n int)             { f.calls = append(f.calls, "avg") }

func newH(t *testing.T) (*Handler, *fakeScope) {
	t.Helper()
	f := &engine.Frame{
		C1: make([]uint8, 2048), C2: make([]uint8, 2048),
		Valid: 2048, WinCols: 2048, Seq: 3, SampleS: 800e-9,
	}
	for i := range f.C1 {
		f.C1[i] = uint8(i % 200)
	}
	fs := &fakeScope{
		stats: engine.Stats{Running: true, TdivS: 500e-6, AvgCount: 16, TrigRising: true},
		frame: f,
	}
	return New(fs, nil, nil, t.Logf), fs
}

func do(t *testing.T, h *Handler, cmd string) string {
	t.Helper()
	return string(h.HandleLine([]byte(cmd + "\n")))
}

func TestIDN(t *testing.T) {
	h, _ := newH(t)
	got := do(t, h, "*IDN?")
	parts := strings.Split(strings.TrimSpace(got), ",")
	if len(parts) != 4 || parts[0] != "Siglent" || parts[1] != "SDS1102CML+" {
		t.Fatalf("*IDN? = %q", got)
	}
}

func TestReplyFormats(t *testing.T) {
	h, _ := newH(t)
	// TDIV: %.2E + LOWER-case s, header echoed.
	if got := do(t, h, "TDIV?"); got != "TDIV 5.00E-04s\n" {
		t.Fatalf("TDIV? = %q", got)
	}
	// SARA: SI-prefix exception. 1/800ns = 1.25 MSa.
	if got := do(t, h, "SARA?"); got != "SARA 1.25MSa\n" {
		t.Fatalf("SARA? = %q", got)
	}
	if got := do(t, h, "SANU? C1"); got != "SANU 2048\n" {
		t.Fatalf("SANU? = %q", got)
	}
	// CHDR OFF strips the header.
	do(t, h, "CHDR OFF")
	if got := do(t, h, "TDIV?"); got != "5.00E-04s\n" {
		t.Fatalf("TDIV? with CHDR OFF = %q", got)
	}
}

func TestErrorTokens(t *testing.T) {
	h, _ := newH(t)
	if got := do(t, h, "BOGUS?"); got != "Undefined header\n" {
		t.Fatalf("unknown = %q", got)
	}
	if got := do(t, h, "C9:VDIV?"); got != "Header suffix out of range\n" {
		t.Fatalf("C9 = %q", got)
	}
	if got := do(t, h, "TDIV 3.3E-3"); got != "Data out of range\n" {
		t.Fatalf("off-ladder tdiv = %q", got)
	}
}

func TestSettersSilent(t *testing.T) {
	h, fs := newH(t)
	if got := do(t, h, "TDIV 1E-3"); got != "" {
		t.Fatalf("setter replied %q", got)
	}
	if got := do(t, h, "TRMD NORM"); got != "" {
		t.Fatalf("TRMD replied %q", got)
	}
	if got := do(t, h, "TRLV 1.0"); got != "" {
		t.Fatalf("TRLV replied %q", got)
	}
	want := []string{"tdiv", "norm", "run", "trlv"}
	if strings.Join(fs.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v", fs.calls)
	}
}

func TestCompoundLine(t *testing.T) {
	h, _ := newH(t)
	got := do(t, h, "CHDR?;TDIV?")
	if got != "CHDR SHORT\nTDIV 5.00E-04s\n" {
		t.Fatalf("compound = %q", got)
	}
}

func TestWFSUAndDAT2(t *testing.T) {
	h, _ := newH(t)
	do(t, h, "WFSU SP,4,NP,100,FP,8")
	if got := do(t, h, "WFSU?"); got != "WFSU SP,4,NP,100,FP,8,SN,0\n" {
		t.Fatalf("WFSU? = %q", got)
	}
	out := h.HandleLine([]byte("C1:WF? DAT2\n"))
	head := "C1:WF ALL,#9000000100"
	if !bytes.HasPrefix(out, []byte(head)) {
		t.Fatalf("DAT2 header = %q", out[:24])
	}
	payload := out[len(head) : len(head)+100]
	// Sample i comes from FP + i·SP = 8, 12, 16... of (i%200).
	for i := 0; i < 100; i++ {
		if payload[i] != uint8((8+i*4)%200) {
			t.Fatalf("payload[%d] = %d", i, payload[i])
		}
	}
	if out[len(out)-1] != '\n' {
		t.Fatal("missing trailing newline")
	}

	// FP beyond the record: reject, never crash (factory bug).
	do(t, h, "WFSU SP,1,NP,0,FP,3000")
	if got := do(t, h, "C1:WF? DAT2"); got != "Data out of range\n" {
		t.Fatalf("FP beyond record = %q", got)
	}
}

func TestWavedesc(t *testing.T) {
	h, _ := newH(t)
	out := h.HandleLine([]byte("C1:WF? DESC\n"))
	head := "C1:WF ALL,#9000000346"
	if !bytes.HasPrefix(out, []byte(head)) {
		t.Fatalf("DESC header = %q", out[:24])
	}
	d := out[len(head) : len(head)+346]
	if string(d[0:8]) != "WAVEDESC" || string(d[16:19]) != "DSO" {
		t.Fatal("names")
	}
	if binary.LittleEndian.Uint16(d[34:]) != 1 {
		t.Fatal("COMM_ORDER")
	}
	if int32(binary.LittleEndian.Uint32(d[116:])) != 2048 {
		t.Fatal("WAVE_ARRAY_COUNT")
	}
	gain := math.Float32frombits(binary.LittleEndian.Uint32(d[156:]))
	if math.Abs(float64(gain)-1.0/50) > 1e-9 { // 1 V/div default / 50
		t.Fatalf("VERTICAL_GAIN = %v", gain)
	}
	hi := math.Float32frombits(binary.LittleEndian.Uint32(d[176:]))
	if math.Abs(float64(hi)-800e-9) > 1e-12 { // float32 precision
		t.Fatalf("HORIZ_INTERVAL = %v", hi)
	}
	ho := math.Float64frombits(binary.LittleEndian.Uint64(d[180:]))
	if math.Abs(ho-(-2048*800e-9/2)) > 1e-12 {
		t.Fatalf("HORIZ_OFFSET = %v", ho)
	}
}

func TestRollRescale(t *testing.T) {
	h, fs := newH(t)
	fs.frame.RollCodes = true
	// Roll codes are HALF-scale: a +25 roll deviation is +50 deep codes.
	for i := range fs.frame.C1 {
		fs.frame.C1[i] = 153 // +25 from centre → deep +50 → 178
	}
	do(t, h, "WFSU SP,1,NP,4,FP,0")
	out := h.HandleLine([]byte("C1:WF? DAT2\n"))
	head := "C1:WF ALL,#9000000004"
	payload := out[len(head) : len(head)+4]
	if payload[0] != 178 {
		t.Fatalf("roll rescale: %d, want 178 (deviation doubled)", payload[0])
	}
	// Clamp: a +100 roll deviation would be +200 deep → clamp to 255.
	for i := range fs.frame.C1 {
		fs.frame.C1[i] = 228
	}
	out = h.HandleLine([]byte("C1:WF? DAT2\n"))
	if out[len(head)] != 255 {
		t.Fatalf("roll rescale clamp: %d, want 255", out[len(head)])
	}
}
