// ENGMODEL-OWNER-UNIT: FU-APP-PANEL

// Package panel is the SDS1000CML+'s front panel as the core UI model sees
// it (the board's Input): the key matrix read through the engine's owner
// on SIGIO from /dev/fpga_key, the knobs' quadrature and acceleration, and
// the lamp latch. It turns the matrix into key and knob events under the
// UI model's names and the lit lamps into the latch word; the workspaces,
// sheets and every key's job are the core's (open-dso-core/uimodel/panel).
// It never touches the GPMC bus: the matrix reads and the latch writes go
// through the engine's command surface, applied by the bus owner at the
// frame boundary.
package panel

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/labeth/open-dso-core/hal"
	ui "github.com/labeth/open-dso-core/uimodel/panel"
)

// Matrix is the engine's command surface for the panel: the key matrix it
// reads on request and the lamp word it latches (spec 08 §4).
// TRLC-LINKS: REQ-SDS-135
type Matrix interface {
	ReadMatrix() ([5]uint16, bool)
	SetLEDs(word uint16)
}

// Input is the front panel: Run reads the matrix, Events carries what it
// decoded, SetLamps latches the lamps.
// TRLC-LINKS: REQ-SDS-135
type Input struct {
	m      Matrix
	keyFD  int
	logf   func(string, ...any)
	events chan hal.InputEvent
	dec    decoder

	mu   sync.Mutex
	word uint16 // the latched lamp word
	set  bool
}

var _ ui.Input = (*Input)(nil)

// NewInput is the front panel over the engine's matrix and the inherited
// key device keyFD (-1 without one: the matrix is polled).
// TRLC-LINKS: REQ-SDS-135
func NewInput(m Matrix, keyFD int, logf func(string, ...any)) *Input {
	return &Input{m: m, keyFD: keyFD, logf: logf, events: make(chan hal.InputEvent, 64)}
}

// Events is the decoded keys and knobs.
// TRLC-LINKS: REQ-SDS-135
func (in *Input) Events() <-chan hal.InputEvent { return in.events }

// SetLamps latches the lamp word of the named lamps when it changed: the
// latch's bits are the UI model's lamp word (spec 02 §7.5, the corroborated
// PCB wiring; there is no TRIG'd lamp, and INTENSITY stays dark).
// TRLC-LINKS: REQ-SDS-135
func (in *Input) SetLamps(on []string) error {
	w := ui.LampWord(on)
	in.mu.Lock()
	changed := !in.set || w != in.word
	in.word, in.set = w, true
	in.mu.Unlock()
	if changed {
		in.m.SetLEDs(w)
	}
	return nil
}

// Run reads the matrix: on SIGIO (knobs and keys, rate-capped at ~150 Hz)
// plus the MANDATORY 40 ms re-sync tick (keys only — a timer-driven read
// lands mid-detent and misreads quadrature). Blocks; run as a goroutine.
// TRLC-LINKS: REQ-SDS-135
func (in *Input) Run(stop <-chan struct{}) {
	sigio := make(chan os.Signal, 8)
	haveSIGIO := false
	if in.keyFD >= 0 {
		signal.Notify(sigio, syscall.SIGIO)
		if err := armSIGIO(in.keyFD); err != nil {
			in.logf("panel: SIGIO arming failed (%v) — poll fallback", err)
		} else {
			haveSIGIO = true
		}
	} else {
		in.logf("panel: no inherited /dev/fpga_key fd — poll fallback")
	}

	// Seed the decoder baseline (prevents fabricated first presses).
	if m, ok := in.m.ReadMatrix(); ok {
		in.dec.prev, in.dec.havePrev = m, true
	}
	// ADR-PANEL-SERIAL-SCAN: a matrix built from knob counts may be decoded
	// on every read.
	synthesized := false
	if s, ok := in.m.(interface{ MatrixSynthesized() bool }); ok {
		synthesized = s.MatrixSynthesized()
	}
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
	read := func(knobsOn bool) {
		if m, ok := in.m.ReadMatrix(); ok {
			for _, ev := range in.dec.decode(m, knobsOn) {
				select {
				case in.events <- ev:
				case <-stop:
					return
				}
			}
		}
	}
	for {
		select {
		case <-stop:
			return
		case <-sigio:
			if time.Since(lastSig) < 6*time.Millisecond {
				if !pendingTrail {
					pendingTrail = true
					trailing.Reset(8 * time.Millisecond)
				}
				continue
			}
			lastSig, pendingTrail = time.Now(), false
			read(true)
		case <-trailing.C:
			pendingTrail = false
			lastSig = time.Now()
			read(true) // knob decode: catches the burst's last detent
		case <-tick.C:
			// Fallback mode (no SIGIO) decodes knobs on the tick too — the
			// deliberate exception that accepts mid-detent reads. A matrix
			// synthesized from knob counts has no mid-detent phase.
			read(!haveSIGIO || synthesized)
		}
	}
}

// armSIGIO makes the key device fd signal this process (SIGIO) when it has input.
// TRLC-LINKS: REQ-SDS-135
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

// decoder turns matrix snapshots into events: key 1→0 edges always; knob
// quadrature only on interrupt-aligned reads (spec 08 §1/§3).
type decoder struct {
	prev     [5]uint16
	havePrev bool
}

// decode is one matrix snapshot's events: a key press for every 1→0 edge
// (a key this board does not wire has no name: it only wakes the screen),
// then at most one knob, the first in the FPGA's priority order whose
// selector has a phase bit low, when knobsOn and the magnitude word is not
// zero (zero is a plain key interrupt).
// TRLC-LINKS: REQ-SDS-135
func (d *decoder) decode(m [5]uint16, knobsOn bool) []hal.InputEvent {
	if !d.havePrev {
		d.prev, d.havePrev = m, true
		return nil
	}
	var out []hal.InputEvent
	for i := range 4 {
		pressed := (d.prev[i] &^ m[i]) &^ uint16(knobPhaseMask)
		for bit := range 16 {
			if pressed&(1<<uint(bit)) != 0 {
				out = append(out, hal.InputEvent{Source: hal.FromPanel, Kind: hal.KeyEvent, Key: keyCodes[bcode(i, bit)], Down: true})
			}
		}
	}
	if knobsOn {
		if ev, ok := knobOf(m); ok {
			out = append(out, ev)
		}
	}
	d.prev = m
	return out
}

// knobOf is the one knob a snapshot moved (the cross-coupling fix): its
// signed steps, one a detent on a stepped knob, accelerated on the others.
// TRLC-LINKS: REQ-SDS-135
func knobOf(m [5]uint16) (hal.InputEvent, bool) {
	raw := m[4]
	if raw == 0 {
		return hal.InputEvent{}, false
	}
	for _, k := range knobs {
		w := m[k.selIdx]
		loDown := w&(1<<k.bitLo) == 0
		hiDown := w&(1<<k.bitHi) == 0
		if !loDown && !hiDown {
			continue
		}
		steps := 1
		if !k.stepped {
			steps = accel(raw)
		}
		if hiDown {
			steps = -steps
		}
		return hal.InputEvent{Source: hal.FromPanel, Kind: hal.KnobEvent, Knob: k.name, Steps: steps}, true
	}
	return hal.InputEvent{}, false
}
