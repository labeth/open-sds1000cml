// Package panel is the front-panel controller (spec 08): SIGIO-driven key
// matrix + hardware-quadrature knobs + LED latch. The panel worker NEVER
// touches the GPMC bus — all matrix reads and LED/offset/level writes go
// through the engine's command surface and are applied by the bus owner at
// the frame boundary. The analog V/div front end (SPI) is off-bus and is
// driven directly.
package panel

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
)

// Engine is the command surface the controller drives (spec 08 §4).
type Engine interface {
	ReadMatrix() ([5]uint16, bool)
	SetLEDs(word uint16)
	SetOffsetDAC(ch int, code uint16)
	SetTrigLevelCode(code uint16) uint16
	SetTdiv(tdivS float64) (engine.Band, bool)
	SetNorm(on bool)
	SetRunning(on bool)
	SetSingle()
	SetTrigSlope(rising bool)
	SetTrigSource(ch int)
	SetTrigType(typ int)
	SetAcqMode(m int)
	SetAvgCount(n int)
	SetEresLen(l int)
	SetETS(on bool)
	SetTrigPosFrac(frac float64)
	SetHoldoff(sec float64) float64
	SetMemDepth(samples int) int
	SetPulseParams(lvlFrac, wMinNs, wMaxNs float64, cond int)
	SetSlopeParams(loFrac, hiFrac, tMinNs, tMaxNs float64, cond int)
	SetVideoParams(std, line int, neg bool)
	Snapshot() engine.Stats // authoritative state to resync knob shadows
}

// Analog is the off-bus V/div front end; nil → V/div knobs claim-and-ignore.
type Analog interface {
	SetVdiv(ch, idx int) error
	Snapshot() ([2]int, bool)
	SetOffset(ch int, volts float64) uint16
	OffsetReqV(ch int) float64
	SetCoupling(ch, mode int) error
	Coupling(ch int) int
	SetProbe(ch int, x float64)
	ProbeFactor(ch int) float64
}

// LED bits (spec 08 §5 — only the corroborated bits are wired; the low-byte
// CURSORS/MATH/REF candidates conflict and stay unwired).
const (
	ledTrigd   = 0x0004
	ledCH2     = 0x0010
	ledCH1     = 0x0020
	ledMeasure = 0x0100
	ledAcquire = 0x0200
	ledDisplay = 0x0400
	ledRun     = 0x2000
	ledStop    = 0x4000
	ledSingle  = 0x8000
)

const knobPhaseMask = 0xC0C0 // encoder phase bits in every selector word

// Button codes are sel-index<<8 | bit.
func bcode(selIdx, bit int) int { return selIdx<<8 | bit }

// Wired buttons (spec 08 §2 map; selIdx: 0=0x64, 1=0x65, 2=0x66, 3=0x67).
var (
	btnRunStop = bcode(1, 2)
	btnSingle  = bcode(1, 10)
	btnAuto    = bcode(3, 10)
)

// knobDef is one row of the FIXED FPGA priority order (spec 08 §3): exactly
// one knob is serviced per interrupt — the first whose selector has a low
// phase bit. Direction comes from WHICH bit rests low (never from a phase
// change — that misses sustained rotation).
type knobDef struct {
	name    string
	selIdx  int
	bitLo   uint // low → CW (+1)
	bitHi   uint // low → CCW (−1)
	stepped bool // stepped: 1/detent; continuous: accel-clamped 0x69
}

var knobs = []knobDef{
	{"horizpos", 3, 14, 15, false},
	{"ch2pos", 3, 6, 7, false},
	{"tdiv", 2, 14, 15, true},
	{"ch2vdiv", 2, 6, 7, true},
	{"ch1vdiv", 1, 14, 15, true},
	{"adjust", 1, 6, 7, false},
	{"triglevel", 0, 14, 15, false},
	{"ch1pos", 0, 6, 7, false},
}

// accel maps the raw 0x69 magnitude to steps (continuous knobs): runaway
// guard at 200 FIRST (must be ≥100 or the ≥20→100 row is unreachable).
func accel(raw uint16) int {
	if raw > 200 {
		raw = 200
	}
	switch {
	case raw >= 20:
		return 100
	case raw >= 10:
		return 50
	default:
		return int(raw)
	}
}

type Controller struct {
	eng   Engine
	fe    Analog
	keyFD int
	logf  func(string, ...any)

	tdivs    []float64
	tdivIdx  int
	vIdx     [2]int
	offReqV  [2]float64
	trigCode uint16
	running  bool
	norm     bool

	prev     [5]uint16
	havePrev bool

	// Menu state (spec 08 §6): written by the panel goroutine, read by the LCD
	// renderer, guarded by mu. inject runs API-driven panel events on the panel
	// goroutine so button/knob dispatch stays single-threaded.
	mu       sync.Mutex
	menuPage int
	menuSel  int
	chDisp   [2]bool
	showMeas bool
	viewMode int // 0 = Y-T, 1 = X-Y, 2 = FFT (DISPLAY menu "View")
	mathMode int // 0 = off, 1 = C1+C2, 2 = C1-C2, 3 = C1×C2 (DISPLAY menu "Math")
	// On-screen cursors (spec 08 §6): two X (time) and two Y (volts) cursors,
	// positions as screen fractions; ADJUST moves the selected one.
	curOn   bool
	curType int // 0 = X (time), 1 = Y (volts)
	curSel  int // 0 = A, 1 = B
	curX    [2]float64
	curY    [2]float64
	inject  chan func()

	// frameFn reads the latest published frame (fo.WithFrame) for autoset; nil
	// until SetFrameSource is called (AUTO then falls back to plain AUTO/run).
	frameFn func(func(*engine.Frame))
	// autoset runs as a background sweep; autosetStop cancels it (second AUTO).
	autosetBusy bool
	autosetStop chan struct{}
	autosetMsg  string     // banner text while busy ("AUTOSET…" / a result note)
	refs        [2]refWave // saved reference waveforms (REF A/B)

	// Trigger-qualifier shadows (the engine has no getters for these), edited on
	// the TRIGGER-qualifier sub-page. ns for widths/times; fractions 0..1.
	pulseLvl, pulseMin, pulseMax         float64
	pulseCond                            int
	slopeLo, slopeHi, slopeMin, slopeMax float64
	slopeCond                            int
	videoStd, videoLine                  int
	videoNeg                             bool
}

// New builds the controller. The timebase ladder is injected (the controller
// does not own it); startTdiv seeds the knob index. All shadows are seeded
// WITHOUT driving anything — the inherited analog state stays untouched
// until the user turns a knob (spec 08 §7).
func New(eng Engine, fe Analog, keyFD int, tdivs []float64, startTdiv float64, logf func(string, ...any)) *Controller {
	c := &Controller{
		eng: eng, fe: fe, keyFD: keyFD, logf: logf,
		tdivs:    tdivs,
		vIdx:     [2]int{analog.BootDetent, analog.BootDetent},
		trigCode: 31434, // 0 V threshold
		chDisp:   [2]bool{true, true},
		curX:     [2]float64{0.35, 0.65},
		curY:     [2]float64{0.35, 0.65},
		inject:   make(chan func(), 32),
		running:  true,
		pulseLvl: 0.5, pulseMin: 100, pulseMax: 1000,
		slopeLo: 0.1, slopeHi: 0.9, slopeMin: 100, slopeMax: 1000,
		videoLine: 1,
	}
	for i, t := range tdivs {
		if t >= startTdiv*(1-1e-6) {
			c.tdivIdx = i
			break
		}
	}
	return c
}

// Run is the panel event loop: SIGIO (knobs + buttons, rate-capped ~150 Hz)
// plus the MANDATORY 40 ms re-sync tick (buttons only — a timer-driven read
// lands mid-detent and misreads quadrature). Blocks; run as a goroutine.
func (c *Controller) Run(stop <-chan struct{}) {
	sigio := make(chan os.Signal, 8)
	haveSIGIO := false
	if c.keyFD >= 0 {
		signal.Notify(sigio, syscall.SIGIO)
		if err := armSIGIO(c.keyFD); err != nil {
			c.logf("panel: SIGIO arming failed (%v) — poll fallback", err)
		} else {
			haveSIGIO = true
		}
	} else {
		c.logf("panel: no inherited /dev/fpga_key fd — poll fallback")
	}

	// Seed the decoder baseline (prevents fabricated first presses).
	if m, ok := c.eng.ReadMatrix(); ok {
		c.prev, c.havePrev = m, true
	}
	c.pushLEDs()

	tick := time.NewTicker(40 * time.Millisecond)
	defer tick.Stop()
	// trailing fires shortly after a rate-limited interrupt so the LAST
	// detent of a fast knob spin (whose interrupt lands inside the 6 ms cap)
	// is still read with knob decode enabled — a plain drop would lose it.
	trailing := time.NewTimer(time.Hour)
	trailing.Stop()
	defer trailing.Stop()
	var lastSig time.Time
	var pendingTrail bool
	for {
		select {
		case <-stop:
			return
		case fn := <-c.inject:
			fn() // API-injected button/knob, run on the panel goroutine
		case <-sigio:
			if time.Since(lastSig) < 6*time.Millisecond {
				if !pendingTrail {
					pendingTrail = true
					trailing.Reset(8 * time.Millisecond)
				}
				continue
			}
			lastSig, pendingTrail = time.Now(), false
			if m, ok := c.eng.ReadMatrix(); ok {
				c.decode(m, true)
			}
		case <-trailing.C:
			pendingTrail = false
			lastSig = time.Now()
			if m, ok := c.eng.ReadMatrix(); ok {
				c.decode(m, true) // knob decode: catches the burst's last detent
			}
		case <-tick.C:
			if m, ok := c.eng.ReadMatrix(); ok {
				// Fallback mode (no SIGIO) decodes knobs on the tick too —
				// the deliberate exception that accepts mid-detent reads.
				c.decode(m, !haveSIGIO)
			}
		}
	}
}

func armSIGIO(fd int) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETOWN, uintptr(syscall.Getpid())); errno != 0 {
		return errno
	}
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFL, 0)
	if errno != 0 {
		return errno
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFL, flags|syscall.O_ASYNC); errno != 0 {
		return errno
	}
	return nil
}

// decode processes one matrix snapshot: button 1→0 edges always; knob
// quadrature only on interrupt-aligned reads (spec 08 §1/§3).
func (c *Controller) decode(m [5]uint16, knobsOn bool) {
	if !c.havePrev {
		c.prev, c.havePrev = m, true
		return
	}
	for i := 0; i < 4; i++ {
		pressed := (c.prev[i] &^ m[i]) &^ uint16(knobPhaseMask)
		for bit := 0; bit < 16; bit++ {
			if pressed&(1<<uint(bit)) != 0 {
				c.button(bcode(i, bit))
			}
		}
	}
	if knobsOn {
		c.knob(m)
	}
	c.prev = m
}

func (c *Controller) button(code int) {
	// While an autoset sweep runs, ignore everything except AUTO (which cancels)
	// — this both gives a clean "busy" UX and avoids racing its scale changes.
	c.mu.Lock()
	busy := c.autosetBusy
	c.mu.Unlock()
	if busy && code != btnAuto {
		return
	}
	switch code {
	case btnRunStop:
		c.running = !c.running
		c.eng.SetRunning(c.running)
	case btnSingle:
		c.norm, c.running = true, true
		c.eng.SetSingle() // true single-shot: capture one triggered frame, stop
	case btnAuto:
		c.autoset()
		return
	default:
		// Menu / softkey / channel buttons (spec 08 §6). Anything else is
		// claimed-and-ignored so it can't cross-drive another control.
		c.menuButton(code)
		return
	}
	c.pushLEDs()
}

// resync refreshes the knob shadows from authoritative state before a step,
// so a step lands relative to whatever the web UI / SCPI last set — not a
// stale panel-local value (which would snap the setting on the first click).
func (c *Controller) resync() {
	// Autoset owns the shadows while it sweeps (it writes them under mu on its own
	// goroutine). Skip here so the injected-knob path can't race it either.
	c.mu.Lock()
	busy := c.autosetBusy
	c.mu.Unlock()
	if busy {
		return
	}
	st := c.eng.Snapshot()
	if st.TrigCode != 0 {
		c.trigCode = st.TrigCode
	}
	c.running, c.norm = st.Running, st.Norm
	for i, t := range c.tdivs {
		if st.TdivS > 0 && absf(t-st.TdivS) <= t*1e-6 {
			c.tdivIdx = i
			break
		}
	}
	if c.fe != nil {
		idx, _ := c.fe.Snapshot()
		c.vIdx = idx
	}
}

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// knob services AT MOST ONE knob per event, walking the fixed priority order
// (the cross-coupling fix). Gate: 0x69 == 0 means a plain button interrupt.
func (c *Controller) knob(m [5]uint16) {
	raw := m[4]
	if raw == 0 {
		return
	}
	// Autoset owns the shadows while it sweeps; resync() below writes them
	// unguarded, so the busy check MUST be here (before resync), not just in
	// dispatch() — otherwise a knob turn mid-sweep races the autoset goroutine.
	c.mu.Lock()
	busy := c.autosetBusy
	c.mu.Unlock()
	if busy {
		return
	}
	c.resync()
	for _, k := range knobs {
		w := m[k.selIdx]
		loDown := w&(1<<k.bitLo) == 0
		hiDown := w&(1<<k.bitHi) == 0
		if !loDown && !hiDown {
			continue
		}
		dir := 1
		if hiDown {
			dir = -1
		}
		steps := 1
		if !k.stepped {
			steps = accel(raw)
		}
		c.dispatch(k.name, dir, steps)
		return // exactly one knob per interrupt
	}
}

func (c *Controller) dispatch(name string, dir, steps int) {
	c.mu.Lock()
	busy := c.autosetBusy
	c.mu.Unlock()
	if busy { // knobs are inert while autoset sweeps (avoids racing its writes)
		return
	}
	switch name {
	case "tdiv":
		// CW (+1) → slower timebase; ladder is ascending.
		c.tdivIdx = clampInt(c.tdivIdx+dir, 0, len(c.tdivs)-1)
		c.eng.SetTdiv(c.tdivs[c.tdivIdx])
	case "ch1vdiv", "ch2vdiv":
		ch := 0
		if name == "ch2vdiv" {
			ch = 1
		}
		if c.fe == nil {
			return // claim-and-ignore without an analog front end
		}
		c.vIdx[ch] = clampInt(c.vIdx[ch]+dir, 0, len(analog.Detents)-1)
		if err := c.fe.SetVdiv(ch, c.vIdx[ch]); err != nil {
			c.logf("panel: SetVdiv: %v", err)
		}
	case "ch1pos", "ch2pos":
		ch := 0
		if name == "ch2pos" {
			ch = 1
		}
		if c.fe == nil {
			return // no analog front end: offset knob claim-and-ignore
		}
		// Offset step is 20 DAC codes/accel-step; K=262 codes/V is fixed, so
		// step the input-referred volts by 20/262 and let the front end
		// re-derive the code (keeps the offset consistent across detents).
		v := c.fe.OffsetReqV(ch) + float64(dir*steps)*20.0/262.0
		c.fe.SetOffset(ch, v)
	case "triglevel":
		// Sign trap: CW RAISES the level, which LOWERS the code
		// (−938 codes/V); step 40 codes per accel step.
		nc := int(c.trigCode) - dir*40*steps
		nc = clampInt(nc, engine.TrigCodeMin, engine.TrigCodeMax)
		c.trigCode = uint16(nc)
		c.eng.SetTrigLevelCode(c.trigCode)
	case "adjust":
		// ADJUST drives the highlighted menu item (spec 08 §6.3); no-op if the
		// menu is closed.
		c.menuAdjust(dir)
	default:
		// horizpos: claimed-and-ignored for now.
	}
}

func (c *Controller) pushLEDs() {
	c.mu.Lock()
	c1, c2, pg := c.chDisp[0], c.chDisp[1], c.menuPage
	running, norm := c.running, c.norm // read under the lock — both goroutines write these
	c.mu.Unlock()
	var word uint16
	if c1 {
		word |= ledCH1
	}
	if c2 {
		word |= ledCH2
	}
	if running {
		word |= ledRun
	} else {
		word |= ledStop
	}
	if norm {
		word |= ledSingle
	}
	switch pg { // light the active menu lamp (spec 08 §6.4/§8.2)
	case pgAcq, pgRef: // REF is ACQUIRE's second page
		word |= ledAcquire
	case pgDisp, pgChan: // CHANNEL is DISPLAY's second page
		word |= ledDisplay
	}
	c.eng.SetLEDs(word)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
