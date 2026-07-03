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
	decimWin   = 2048 // decimated display window (10-division span cap)
	decimDrain = 6144 // decimated drain depth = window + centring margin
	latchAt     = 0x200 // fill-counter gate before capture-halt
	fillMask    = 0x07ff
	screenDivsH = 10 // horizontal graticule divisions
	screenDivsV = 8  // vertical graticule divisions

	// Slow-envelope constants (spec 04): the per-sample interval is chosen
	// for PHASE SCATTER (~0.23 of the 1 kHz cal period), never for density —
	// dense sampling yields a thin wandering band.
	envIntervalS   = 2.3e-4
	envDisplayCols = 800
	envMinWin      = 200
	envMaxWin      = 2048
	envRingN       = 24
	envFillCap     = 0x600 // fill target cap (the 11-bit counter saturates)

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

// EnvFillTarget is the fill-counter gate for an envelope frame.
func (b Band) EnvFillTarget() uint16 {
	w, _ := b.EnvPlan()
	if w > envFillCap {
		return envFillCap
	}
	return uint16(w)
}

// Prog is what bringUp actually programs (spec 04 §5).
func (b Band) Prog() (class, lo, hi uint16) {
	switch b.Kind() {
	case KindEnvelope:
		_, d := b.EnvPlan()
		return 0x80, uint16(d & 0xffff), uint16(d >> 16)
	case KindRoll:
		return 0x80, rollDivisor & 0xffff, 0
	default:
		return b.Class, b.Lo, b.Hi
	}
}

// Divisor is the 32-bit NOMINAL decimation divisor of the table row.
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
		w, _ := b.EnvPlan()
		return w
	case KindRoll:
		return rollWin
	}
	return decimDrain
}

// WinCols is the sample count spanning the 10-division screen.
func (b Band) WinCols() int {
	switch b.Kind() {
	case KindEnvelope:
		w, _ := b.EnvPlan()
		return w
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

// DisplayedSdivS is the on-screen seconds/div actually delivered. On
// decimated bands the WinCols clamp makes it differ from the nominal label;
// envelope preserves the label by construction; roll reports the label.
func (b Band) DisplayedSdivS() float64 {
	switch b.Kind() {
	case KindEnvelope, KindRoll:
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
