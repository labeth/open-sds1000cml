package engine

import "math"

// Band is one row of the timebase ladder (spec 04 §2): the divisor triple of
// the row plus everything the FSM derives from it. Rows ≥5 ms carry the
// FAITHFUL-NOMINAL divisor for reporting only — what bring-up programs comes
// from Prog() (the envelope phase-scatter formula / the fixed roll divisor),
// never these values.
type Band struct {
	TdivS float64 // nominal seconds/div label
	Class uint16  // divisor class (0x19), nominal
	Lo    uint16  // divisor low (0x1a), nominal
	Hi    uint16  // divisor high (0x1b), nominal
}

const (
	deepRecord = 20480 // physical deep-record depth in samples
	// Decimated bands: the DISPLAY window (10-division span) is decimWin, but
	// the FSM drains decimDrain samples so software centring has margin on BOTH
	// sides of the record centre. With drain == window (the old decimCols) the
	// display window could not shift at all — the clamp pinned it to [0,win] and
	// the trigger sat wherever the HW comparator fired (±800-sample jitter). The
	// drain stays well inside the record actually captured by halt time
	// (arm-to-latch clocks ~14k samples at 500 µs/div), verified per-frame by
	// ValidDepth telemetry.
	decimWin    = 2048  // decimated display window (10-division span cap)
	decimDrain  = 6144  // decimated drain depth = window + centring margin
	latchAt     = 0x200 // fill-counter gate before capture-halt
	fillMask    = 0x07ff
	screenDivsH = 10 // horizontal graticule divisions
	screenDivsV = 8  // vertical graticule divisions

	// Slow-envelope constants (spec 04): the per-sample interval is chosen
	// for PHASE SCATTER (~0.23 of the 1 kHz cal period), never for density —
	// dense sampling yields a thin wandering band.
	envIntervalS   = 2.3e-4
	envDisplayCols = 800
	// envFabricCols is how many envelope columns the FABRIC folds and buffers per
	// frame. The fabric's envelope FIFO is 2048 words at 6 words/column (two
	// channels × a 3-word record), so it holds floor(2048/6) = 341 columns; asking
	// for more overflows the FIFO and silently drops the tail. We program a value
	// safely under that ceiling and stretch the columns across the wider display
	// (envConsumeChannel), so every display column is real — never blanked.
	envFabricCols = 336
	envMinWin     = 200
	envMaxWin      = 2048
	envRingN       = 24
	envFillCap     = 0x600 // fill target cap (the 11-bit counter saturates)
	// Triggered-envelope centring margin (samples each side) captured beyond the
	// display window, so a triggered envelope frame re-centres the anchor without
	// repeat-extending the screen edges. Deadline-gated: only added where the
	// extra capture clears the 250 ms fill deadline with headroom.
	envMargin          = 128
	envFillFloorMs     = 250  // responsiveness floor for the envelope fill deadline
	envFillSlack       = 1.30 // grow the deadline to 1.3× the expected capture time

	// Roll constants (spec 04): a FIXED phase-scatter divisor for every roll
	// tdiv — the table divisor would pace reads at exactly one signal period
	// (thin band). rollClockNs is a read-pacing heuristic only; never use it
	// to size records or windows (the deep interval stays divisor × 10 ns).
	rollDivisor  = 37000 // FIFO steps ~0.37 signal-period/sample (deep rate ×10ns), so each
	rollClockNs  = 50
	rollWin      = 4096
	rollBatch    = 1600
	rollBudgetMs = 220
)

// Kind routes the FSM: which capture path a band runs.
type Kind int

const (
	KindNativeFast Kind = iota
	KindDecimated
	KindEnvelope // 5–50 ms/div: min/max ring over phase-scattered frames
	KindRoll     // ≥100 ms/div: free-running FIFO, arm-once, never halt
)

// bands is the full 33-detent ladder. The fast decade uses 25 ns (not 20 ns).
var bands = []Band{
	{1e-9, 0x20, 0x0000, 0},
	{2e-9, 0x20, 0x0000, 0},
	{5e-9, 0x20, 0x0000, 0},
	{10e-9, 0x20, 0x0000, 0},
	{25e-9, 0x20, 0x0000, 0},
	{50e-9, 0x20, 0x0000, 0},
	{100e-9, 0x20, 0x0000, 0},
	{200e-9, 0x20, 0x0000, 0},
	{500e-9, 0x01, 0x0000, 0},
	{1e-6, 0x01, 0x0000, 0},
	{2e-6, 0x80, 0x0001, 0},
	{5e-6, 0x80, 0x0001, 0},
	{10e-6, 0x80, 0x0001, 0},
	{20e-6, 0x80, 0x0004, 0},
	{50e-6, 0x80, 0x0008, 0},
	{100e-6, 0x80, 0x0014, 0},
	{200e-6, 0x80, 0x0028, 0},
	{500e-6, 0x80, 0x0050, 0},
	{1e-3, 0x80, 0x00c8, 0},
	{2e-3, 0x80, 0x0190, 0},
	{5e-3, 0x80, 0x0320, 0},
	{10e-3, 0x80, 0x07d0, 0},
	{20e-3, 0x80, 0x0fa0, 0},
	{50e-3, 0x80, 0x1f40, 0},
	{100e-3, 0x80, 0x4e20, 0},
	{200e-3, 0x80, 0x9c40, 0},
	{500e-3, 0x80, 0x3880, 0x001},
	{1, 0x80, 0x0640, 0x003},
	{2, 0x80, 0x1a80, 0x006},
	{5, 0x80, 0x3500, 0x00c},
	{10, 0x80, 0x8480, 0x01e},
	{20, 0x80, 0x0900, 0x03d},
	{50, 0x80, 0x1200, 0x07a},
}

// PlanTdiv resolves a requested seconds/div to a ladder row with 1e-6
// relative tolerance (float round-trip safety). ok=false → not a detent;
// the caller rejects the request.
func PlanTdiv(tdivS float64) (Band, bool) {
	for _, b := range bands {
		if math.Abs(b.TdivS-tdivS) <= b.TdivS*1e-6 {
			return b, true
		}
	}
	return Band{}, false
}

// SupportedTdivs lists the ladder's tdiv column, ascending (UI source of truth).
func SupportedTdivs() []float64 {
	out := make([]float64, len(bands))
	for i, b := range bands {
		out[i] = b.TdivS
	}
	return out
}

// Kind classifies the band (spec 04 routing predicates, resolved in order).
func (b Band) Kind() Kind {
	switch {
	case b.TdivS >= 100e-3:
		return KindRoll
	case b.TdivS >= 5e-3:
		return KindEnvelope
	case b.NativeFast():
		return KindNativeFast
	default:
		return KindDecimated
	}
}

// EnvPlan computes the slow-envelope window and the REAL programmed divisor
// (spec 04): winCols = round(10·tdiv/envInterval) clamped BEFORE the divisor
// calc; divisor = round(span/winCols/10 ns) — preserving the labelled span
// exactly, so displayed s/div equals the label.
func (b Band) EnvPlan() (winCols int, divisor uint32) {
	span := screenDivsH * b.TdivS
	w := int(math.Round(span / envIntervalS))
	if w < envMinWin {
		w = envMinWin
	}
	if w > envMaxWin {
		w = envMaxWin
	}
	d := uint32(math.Round(span / float64(w) / 10e-9))
	if d < 1 {
		d = 1
	}
	return w, d
}

// EnvCaptureCols is how many samples a triggered envelope frame FILLS and
// DRAINS: the display window (EnvPlan) plus centring margin on each side, so
// envFrame can re-centre the trigger anchor within the record instead of repeat-
// extending the screen edges (which shimmered as the anchor wandered on the
// few-periods bands). The margin is DEADLINE-GATED: capturing w samples already
// takes 10·TdivS (the screen time) and the fill deadline is 250 ms, so margin is
// added only where the extra capture clears the deadline with headroom — the
// fast envelope bands (5–10 ms/div), which is exactly where the few-periods
// anchor wander makes the shimmer worst. At 20–50 ms/div the deadline binds (and
// the wander is already sub-5 %), so the capture stays the display span.
func (b Band) EnvCaptureCols() int {
	w, _ := b.EnvPlan()
	want := w + 2*envMargin
	if want > envFillCap {
		want = envFillCap // the fill counter can't gate higher — the slowest band
	} //                     (50 ms/div, w>envFillCap) then displays fewer real
	if want > envMaxWin { //  samples via WinCols, instead of draining dead-tail.
		want = envMaxWin
	}
	if want < 2*envMargin+envMinWin {
		want = 2*envMargin + envMinWin
	}
	return want
}

// EnvFillTarget is the fill-counter gate for an envelope frame: the capture
// width (display + deadline-gated centring margin), capped at the counter's cap.
func (b Band) EnvFillTarget() uint16 {
	c := b.EnvCaptureCols()
	if c > envFillCap {
		return envFillCap
	}
	return uint16(c)
}

// baseTickNs is the owned streaming spine's base sample interval — the interval
// at DECIM = 1 (native-fast, the fastest bands). Every band's on-wire decimation
// factor is CaptureIntervalNs / baseTickNs (spec 03 §4.2, §5.1).
const baseTickNs = 2.0

// Decim is the stream decimation factor bringUp programs into DECIM_LO/HI: the
// number of base samples per captured sample (fpga doc §4.2). It is derived
// from the band's real per-sample interval, so the display timing math and the
// programmed decimation stay in lock-step. Envelope/roll fold their scatter
// divisor in exactly the same way.
func (b Band) Decim() uint32 {
	d := uint32(math.Round(b.CaptureIntervalNs() / baseTickNs))
	if d < 1 {
		d = 1
	}
	return d
}

// capWindow is the record window (pre+post samples) the FSM programs, clamped to
// the C2 capture depth: pre+post <= REC_DEPTH − MARGIN (the exact-window
// invariant, fpga doc §4.4). The band drains at most this many samples.
func (b Band) capWindow() int {
	n := b.DrainCols()
	if n > deepRecord-2 {
		n = deepRecord - 2
	}
	return n
}

// PreTrig / PostTrig are the programmable pre/post-trigger depths (PRETRIG_*,
// POSTTRIG_*): the record is split around the trigger mark. Software trigger
// positioning (SetTrigPosFrac) then re-windows within the captured record.
func (b Band) PreTrig() uint32  { return uint32(b.capWindow() / 2) }
func (b Band) PostTrig() uint32 { return uint32(b.capWindow() - b.capWindow()/2) }

// Divisor is the 32-bit NOMINAL decimation divisor of the table row (reporting).
func (b Band) Divisor() uint32 { return uint32(b.Lo) | uint32(b.Hi)<<16 }

// NativeFast: class 0x20/0x01 always, class 0x80 with divisor ≤ 4. The
// ladder has no divisor 5–7, so >4 → decimated is gap-free.
func (b Band) NativeFast() bool {
	if b.TdivS >= 5e-3 {
		return false
	}
	if b.Class == 0x20 || b.Class == 0x01 {
		return true
	}
	return b.Class == 0x80 && b.Divisor() <= 4
}

// CaptureIntervalNs is the real per-sample interval of the captured record.
func (b Band) CaptureIntervalNs() float64 {
	switch b.Kind() {
	case KindEnvelope:
		w, d := b.EnvPlan()
		_ = w
		return float64(d) * 10
	case KindRoll:
		return rollDivisor * 10
	}
	switch b.Class {
	case 0x20:
		return 2
	case 0x01:
		return 4
	default:
		return float64(b.Divisor()) * 10
	}
}

// displayIntervalNs sizes the display window. Class 0x20 uses the 1 ns
// nominal (spec 04: sizing at the real 2 ns would render everything ≤200 ns
// 2× zoomed; the nominal makes 10 divisions match the labelled tdiv).
func (b Band) displayIntervalNs() float64 {
	if b.Class == 0x20 && b.Kind() == KindNativeFast {
		return 1
	}
	return b.CaptureIntervalNs()
}

// DrainCols is how many samples the FSM drains per frame: native-fast always
// drains the full deep record (the edge lands mid-record); decimated drains
// the display record; envelope drains its window; roll fills its raw ring.
func (b Band) DrainCols() int {
	switch b.Kind() {
	case KindNativeFast:
		return deepRecord
	case KindEnvelope:
		return b.EnvCaptureCols() // display span + deadline-gated centring margin
	case KindRoll:
		return rollWin
	}
	return decimDrain
}

// WinCols is the sample count spanning the 10-division screen.
func (b Band) WinCols() int {
	switch b.Kind() {
	case KindEnvelope:
		// Display span = capture minus the centring margin. Equals the EnvPlan
		// span on every band except the counter-limited slowest one (50 ms/div),
		// where it shrinks so the window holds only real captured samples (no
		// dead-tail) — DisplayedSdivS reports the honest s/div for that band.
		if w := b.EnvCaptureCols() - 2*envMargin; w >= envMinWin {
			return w
		}
		return envMinWin
	case KindRoll:
		return rollWin
	}
	w := int(math.Round(screenDivsH * b.TdivS / (b.displayIntervalNs() * 1e-9)))
	if w < 1 {
		w = 1
	}
	// The display window caps at decimWin on decimated bands — NOT at DrainCols,
	// which now carries extra centring margin. Native-fast still caps at its
	// (full-record) drain, where the clamp never binds anyway.
	cap := b.DrainCols()
	if b.Kind() == KindDecimated {
		cap = decimWin
	}
	if w > cap {
		w = cap
	}
	return w
}

// DisplayedSdivS is the on-screen seconds/div actually delivered. On decimated
// bands the WinCols clamp makes it differ from the nominal label; envelope
// preserves the label on every band except the counter-limited slowest one,
// where the reduced display span honestly reports fewer s/div; roll reports the
// label.
func (b Band) DisplayedSdivS() float64 {
	switch b.Kind() {
	case KindRoll:
		return b.TdivS
	}
	return float64(b.WinCols()) * b.displayIntervalNs() * 1e-9 / screenDivsH
}

// WaitBudget is the bounded wait-for-capture budget for real-time bands:
// clamp(3 · captureInterval · LatchAt, 40 ms, 80 ms), in nanoseconds.
func (b Band) WaitBudgetNs() int64 {
	n := int64(3 * b.CaptureIntervalNs() * latchAt)
	if n < 40e6 {
		n = 40e6
	}
	if n > 80e6 {
		n = 80e6
	}
	return n
}

// RollPaceNs is the sleep between roll FIFO pops. It matches the FIFO's own
// production rate (rollDivisor × 10 ns, the deep sample clock) so each read
// lands on a FRESH sample: pacing 5× slower (× 50 ns) misses 4-of-5 fresh
// samples and fills the ring glacially; pacing faster just re-reads dwells
// (skipped by rollUpdate) and risks wedging the port. Clamped to [50 µs, 40 ms].
func RollPaceNs() int64 {
	n := int64(rollDivisor * 10)
	if n < 50e3 {
		n = 50e3
	}
	if n > 40e6 {
		n = 40e6
	}
	return n
}
