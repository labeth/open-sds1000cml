// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"strings"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/wsview"
)

// The settings model (REQ-SDS-212): every setting is a row of a sheet tab,
// declared once with its options or range, getter and setter. The LCD's
// full-screen sheets, the panel's F-keys and knobs, the phone remote's
// /api/sheets and the typed entry all use these rows, so a setting has one
// definition and one setter path whichever surface changes it.

// opt is one option of an enum row: its raw value and label.
type opt struct {
	v int
	t string
}

// preset is a labelled value a number row offers on an F-key.
type preset struct {
	t   string
	v   float64
	run func(c *Controller) error // nil: set v
}

// act is one action of an action row.
type act struct {
	id, t string
	dim   bool
	run   func(c *Controller) error
}

// row is one setting.
type row struct {
	id    string // stable id, e.g. "trig.level"
	label string
	help  string // one line: what the setting does
	knob  string // the knob or key that also changes it
	why   string // what makes it relevant, for a dim row
	hdr   string // a section header drawn above the row
	kind  wsview.RowKind
	rel   func(s *sv) bool

	// Enum
	opts  func(s *sv) []opt
	get   func(s *sv) int
	set   func(c *Controller, v int) error
	quick []int                             // the values on F1..F5 (default: the first five)
	long  func(s *sv) string                // the full value text for the side panel
	busy  func(s *sv) string                // the row's text while an action it started runs ("" when none)
	parse func(s *sv, t string) (int, bool) // typed values beyond the labels (a custom baud)

	// Number: val in unit; setN validates against lim and quantises like the
	// knob; stepN is one knob detent (or F-key Down/Up).
	unit    string
	val     func(s *sv) float64
	setN    func(c *Controller, v float64) error
	stepN   func(c *Controller, n int) error
	fmtN    func(v float64) string
	fmtS    func(s *sv, v float64) string // fmtN, for a row whose unit follows the state
	unitOf  func(s *sv) string            // unit, for a row whose unit follows the state
	text    func(s *sv) string            // the value as written, when more than its number
	lim     func(s *sv) (lo, hi float64)
	step    func(s *sv) float64   // one knob step in unit (0 = a ladder)
	ladder  func(s *sv) []float64 // the discrete values, when a ladder
	presets func(s *sv) []preset

	// Action (and Slot)
	acts   func(s *sv) []act
	status func(s *sv) string

	// Position push (Zero) on the row: back to the factory value.
	def func(c *Controller) error

	meas int // MEAS parameter rows: index+1 into measParams (live values beside)
	slot int // FILE rows: the setup slot number
}

// tab is one sheet tab.
type tab struct {
	id, name, title string
	key             string // the printed key that opens it
	rows            []*row
	home            int // the row the first opening lands on
}

// relevant reports whether r is in use now.
// TRLC-LINKS: REQ-SDS-212
func (r *row) relevant(s *sv) bool { return r.rel == nil || r.rel(s) }

// options returns an enum row's options.
// TRLC-LINKS: REQ-SDS-212
func (r *row) options(s *sv) []opt {
	if r.opts == nil {
		return nil
	}
	return r.opts(s)
}

// optIndex is the index of the current option, -1 when none matches.
// TRLC-LINKS: REQ-SDS-212
func (r *row) optIndex(s *sv) int {
	cur := r.get(s)
	for i, o := range r.options(s) {
		if o.v == cur {
			return i
		}
	}
	return -1
}

// valueText is the row's current value as written in the sheet.
// TRLC-LINKS: REQ-SDS-212
func (r *row) valueText(s *sv) string {
	if r.busy != nil {
		if t := r.busy(s); t != "" {
			return t
		}
	}
	switch r.kind {
	case wsview.Enum:
		if i := r.optIndex(s); i >= 0 {
			return r.options(s)[i].t
		}
		return fmt.Sprint(r.get(s))
	case wsview.Number:
		if r.text != nil {
			return r.text(s)
		}
		return r.format(s, r.val(s))
	case wsview.Bytes:
		return matchText(s)
	case wsview.Action, wsview.Slot:
		if r.status != nil {
			return r.status(s)
		}
	}
	return ""
}

// longText is the side panel's large value.
// TRLC-LINKS: REQ-SDS-212
func (r *row) longText(s *sv) string {
	if r.long != nil {
		return r.long(s)
	}
	return r.valueText(s)
}

// quickOpts are the options on F1..F5.
// TRLC-LINKS: REQ-SDS-212
func (r *row) quickOpts(s *sv) []opt {
	all := r.options(s)
	if r.quick == nil {
		return all[:min(5, len(all))]
	}
	var out []opt
	for _, q := range r.quick {
		for _, o := range all {
			if o.v == q {
				out = append(out, o)
			}
		}
	}
	return out
}

// gauge places a number row's value in its range, 0..1.
// TRLC-LINKS: REQ-SDS-212
func (r *row) gauge(s *sv) float64 {
	v := r.val(s)
	if r.ladder != nil {
		l := r.ladder(s)
		best := 0
		for i := range l {
			if math.Abs(math.Log(l[i]/v)) < math.Abs(math.Log(l[best]/v)) {
				best = i
			}
		}
		if len(l) > 1 {
			return float64(best) / float64(len(l)-1)
		}
		return 0
	}
	lo, hi := r.lim(s)
	if hi > lo {
		return min(max((v-lo)/(hi-lo), 0), 1)
	}
	return 0.5
}

// rangeText is a number row's range for the side panel and errors.
// TRLC-LINKS: REQ-SDS-212
func (r *row) rangeText(s *sv) string {
	lo, hi := r.lim(s)
	return r.format(s, lo) + " .. " + r.format(s, hi)
}

// format writes a number row's value v.
// TRLC-LINKS: REQ-SDS-212
func (r *row) format(s *sv, v float64) string {
	if r.fmtS != nil {
		return r.fmtS(s, v)
	}
	return r.fmtN(v)
}

// unitFor is a number row's unit now.
// TRLC-LINKS: REQ-SDS-212
func (r *row) unitFor(s *sv) string {
	if r.unitOf != nil {
		return r.unitOf(s)
	}
	return r.unit
}

// setEnum applies option v, refusing a value the row does not offer.
// TRLC-LINKS: REQ-SDS-212
func (r *row) setEnum(c *Controller, s *sv, v int) error {
	for _, o := range r.options(s) {
		if o.v == v {
			return r.set(c, v)
		}
	}
	if r.parse != nil {
		return r.set(c, v)
	}
	var names []string
	for _, o := range r.options(s) {
		names = append(names, o.t)
	}
	return fmt.Errorf("%s has no value %d (%s)", r.label, v, strings.Join(names, ", "))
}

// setNumber validates v against the row's range and applies it.
// TRLC-LINKS: REQ-SDS-212
func (r *row) setNumber(c *Controller, s *sv, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("%s: not a number", r.label)
	}
	lo, hi := r.lim(s)
	// a rounding allowance at each bound, scaled to that bound: one scaled to
	// the larger lets 0 through a 10 ns .. 10 s row
	if v < lo-1e-9*math.Abs(lo) || v > hi+1e-9*math.Abs(hi) {
		return fmt.Errorf("%s %s is outside %s", strings.ToLower(r.label), r.format(s, v), r.rangeText(s))
	}
	return r.setN(c, v)
}

// parseEnum finds the option a typed text names: its label (any case), its
// raw value, or a row-specific typed value.
// TRLC-LINKS: REQ-SDS-212
func (r *row) parseEnum(s *sv, text string) (int, error) {
	t := strings.TrimSpace(text)
	for _, o := range r.options(s) {
		if strings.EqualFold(o.t, t) {
			return o.v, nil
		}
	}
	if r.parse != nil {
		if v, ok := r.parse(s, t); ok {
			return v, nil
		}
	}
	for _, o := range r.options(s) {
		if fmt.Sprint(o.v) == t {
			return o.v, nil
		}
	}
	if err := ambiguousComma(t); err != nil { // "57,600" baud: say why, as a number row does
		return 0, fmt.Errorf("%s: %v", r.label, err)
	}
	var names []string
	for _, o := range r.options(s) {
		names = append(names, o.t)
	}
	return 0, fmt.Errorf("%s: %q is not one of %s", r.label, t, strings.Join(names, ", "))
}

// nearestLadder snaps v to the closest ladder value (log distance).
// TRLC-LINKS: REQ-SDS-212
func nearestLadder(l []float64, v float64) int {
	best := 0
	for i := range l {
		if math.Abs(math.Log(l[i]/v)) < math.Abs(math.Log(l[best]/v)) {
			best = i
		}
	}
	return best
}

// ---- the state snapshot rows read ----

// measItem is one chosen measurement: a channel and a parameter.
type measItem struct {
	ch    int // 0 = C1
	param int // index into measParams
}

// maxMeasItems is how many readouts fit beside the trace.
const maxMeasItems = 12

// sv is one consistent read of everything the rows and panes show. It is
// taken with c.mu held only to copy the controller's fields; engine and
// front-end reads use their own thread-safe getters. Rows read sv alone, so
// building a view never re-enters c.mu.
type sv struct {
	now time.Time
	st  engine.Stats

	fe       bool
	vidx     [2]int
	probe    [2]float64
	cpl      [2]int
	offV     [2]float64 // requested input-referred offset volts
	hasBWL   bool
	bwl      [2]bool
	hasInv   bool
	inv      [2]bool
	trigV    float64 // the trigger level at the probe tip
	trigLo   float64 // the level's range (the code window), tip volts
	trigHi   float64
	trigStep float64 // one Level knob step, tip volts

	hasSerial bool
	serial    engine.SerialParams

	chDisp            [2]bool
	showMeas, persist bool
	viewMode          int
	mathMode          int
	zoom              int
	zoomOff           float64
	tdivs             []float64
	memDepth          int // the depth asked for: on the SRAM images the one in use (Stats.MemDepth) follows the timebase

	decProto, decBaud, decChA, decChB int
	decCPOL, decCPHA, decLSB          bool
	decParity                         string
	decInv                            bool
	decBits                           int
	decDelayNs                        float64 // SPI's data delay set by hand (ns)
	decDelayAuto, decFine             bool
	spiEye                            decode.SPIEye
	decAuto                           bool // the settings are what Auto filled in
	decFormat                         int
	decMode, decTrig, decValue        int
	streamList                        bool
	hasStream                         bool
	streamStopped                     bool

	curOn          bool
	curType        int
	curSel         int
	curX, curY     [2]float64 // the cursors' screen fractions now (beyond 0..1 off the screen)
	curT, curV     [2]float64 // their times from the trigger (s) and volts at curVCh's tip
	curVCh         int
	records        float64 // the record's length in screens (0: no frame yet)
	refHas         [2]bool
	refShow        [2]bool
	refC2          [2]bool    // the reference holds C2 too
	refTdiv        [2]float64 // the timebase it was stored at
	srActive       bool
	srSeeding      bool
	srFocus        int
	srCh, srK      int
	srStopMode     int
	srStopVal      float64
	srStatus       string
	srManLo        int
	srManHi        int
	srGateLo       int
	srGateHi       int
	srN            int
	srFPGABusy     bool
	maskN, maskTol int
	maskBusy       bool
	maskMsg        string
	meas           []measItem
	fftSrc         int
	stackN         int
	stacking       bool
	slotUsed       [setupSlots + 1]bool
	slotSum        [setupSlots + 1]string
	remoteNotices  bool
	saverMin       int    // the Screen saver row (minutes, 0 = off)
	offMin         int    // the Screen off row
	screen         Screen // what the LCD shows now
	hasHardcopy    bool
	pulse          [4]float64 // level frac, min ns, max ns, cond
	slope          [5]float64 // lo, hi, min ns, max ns, cond
	video          [3]int     // std, line, neg
	ui             uiState
}

// srcVdiv is a channel's displayed V/div at the probe tip.
// TRLC-LINKS: REQ-SDS-212
func (s *sv) vdivTip(ch int) float64 {
	return analog.Detents[s.vidx[ch&1]].VdivV * s.probe[ch&1]
}

// matchTokens is the serial trigger's match as editor tokens.
// TRLC-LINKS: REQ-SDS-214
func matchTokens(s *sv) []string {
	if !s.hasSerial {
		return nil
	}
	return tokensOf(s.serial)
}

// matchText is the match as written: "55 AA", or "any byte".
// TRLC-LINKS: REQ-SDS-214
func matchText(s *sv) string {
	t := strings.Join(matchTokens(s), " ")
	if t == "" {
		return "any byte"
	}
	return t
}
