// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"fmt"
	"time"

	"open-sds/app/internal/sramcapture"
)

// Panel matrix synthesis (ADR-PANEL-SERIAL-SCAN). The scan's 64 bits arrive
// as eight bytes, row 1 first; within a row, frame bit k is key position k+1.
// The panel decoder speaks the factory's words: 0x67 rows 1 (high byte) and 2,
// 0x66 rows 3 and 4, 0x65 rows 5 and 6, 0x64 rows 7 and 8, positions 1..8 at
// bits 6, 7, 5, 4, 3, 2, 1, 0 of each byte, active low. Positions 1 and 2 are
// the row's knob phases; the factory image turned them into a direction (bit
// 6 low clockwise, bit 7 low counter-clockwise) and a shared magnitude word.
// The fabric counts each knob's quadrature steps, and ReadMatrix rebuilds the
// factory words from the counts, one knob per read in the decoder's order.
var panelPositionBit = [8]uint{6, 7, 5, 4, 3, 2, 1, 0}

// panelSteppedRow marks the rows whose knobs step once per read (T/div,
// CH2 V/div, CH1 V/div); the others take their whole pending count.
var panelSteppedRow = [8]bool{false, false, true, true, true, false, false, false}

const (
	// panelCountsPerDetent is quadrature transitions per knob detent: bench
	// 2026-10-06, five T/div clicks counted 20, resting at phase 11.
	panelCountsPerDetent = 4
	// panelKnobSign maps counting up to clockwise.
	panelKnobSign = 1
	// panelMagnitudeMax matches the panel decoder's runaway guard.
	panelMagnitudeMax = 200
)

// panelKnobs is the ReadMatrix state: the last counts read and the steps not
// yet reported, per row.
type panelKnobs struct {
	primed  bool
	last    [8]uint8
	pending [8]int
}

// SetPanelScan stores the front-panel scan settings and applies them; image
// loads reset the fabric, so applyPanelScan re-applies them after each load.
// TRLC-LINKS: REQ-SDS-022
func (e *Engine) SetPanelScan(p sramcapture.PanelConfig) error {
	e.mu.Lock()
	e.panelScan, e.panelScanSet = p, true
	e.mu.Unlock()
	return e.applyPanelScan()
}

// applyPanelScan writes the scan settings when the loaded image has the
// block; images without it are left alone.
// TRLC-LINKS: REQ-SDS-022
func (e *Engine) applyPanelScan() error {
	e.mu.Lock()
	p, set := e.panelScan, e.panelScanSet
	e.mu.Unlock()
	if !set {
		p = sramcapture.PanelDefault
	}
	if e.sram == nil {
		return nil
	}
	if ok, err := e.sram.SupportsPanel(); err != nil || !ok {
		return err
	}
	e.panelMu.Lock()
	e.panelState.primed = false
	e.panelMu.Unlock()
	return e.sram.ConfigurePanel(p)
}

// PanelScan reads the last panel frame and its counters.
// TRLC-LINKS: REQ-SDS-022
func (e *Engine) PanelScan() (sramcapture.PanelFrame, error) {
	if e.sram == nil {
		return sramcapture.PanelFrame{}, fmt.Errorf("engine: no SRAM fabric")
	}
	if ok, err := e.sram.SupportsPanel(); err != nil || !ok {
		return sramcapture.PanelFrame{}, fmt.Errorf("engine: fabric lacks the panel scan: %v", err)
	}
	return e.sram.ReadPanel()
}

// MatrixSynthesized reports that ReadMatrix builds knob state from counts, so
// every read may decode knobs (no mid-detent phase to misread).
// TRLC-LINKS: REQ-SDS-022
func (e *Engine) MatrixSynthesized() bool { return e.sram != nil }

// ReadMatrix is the panel's key-matrix request: the factory's five words
// [0x64, 0x65, 0x66, 0x67, 0x69], rebuilt from the panel scan. ok is false
// when the loaded image has no scan block.
// TRLC-LINKS: REQ-SDS-022
func (e *Engine) ReadMatrix() ([5]uint16, bool) {
	if e.sram == nil {
		return [5]uint16{}, false
	}
	if ok, err := e.sram.SupportsPanel(); err != nil || !ok {
		return [5]uint16{}, false
	}
	f, err := e.sram.ReadPanel()
	if err != nil {
		return [5]uint16{}, false
	}
	e.panelMu.Lock()
	defer e.panelMu.Unlock()
	m, ok := panelMatrix(f, &e.panelState)
	if !ok {
		// Rare and diagnostic: log the raw read, at most one a second.
		e.panelRejects++
		if now := e.clk.Now(); now.Sub(e.panelRejectLog) >= time.Second {
			e.panelRejectLog = now
			e.logf("panel: implausible scan read dropped (%d so far): words %04x knobs %04x frames %d", e.panelRejects, f.Words, f.Knobs, f.Frames)
		}
	}
	return m, ok
}

// Plausibility limits for one scan read. A hand holds at most a few keys and
// turns a knob a few detents between reads (40 ms or less apart). A read
// while the fabric loses power or restarts is garbage: zero words press all
// 48 keys, and counters reading 0 or 0xff jump every knob by up to 31 detents.
// After a power cycle the scope came up with the trigger level 39 steps up,
// both positions 12 down and the trigger source and slope pushed (bench
// 2026-10-08).
const (
	panelMaxKeysDown   = 3
	panelMaxCountsRead = 6 * panelCountsPerDetent
)

// panelMatrix converts one scan read into the factory words, advancing the
// knob state. ok is false for an implausible read: it yields no keys or
// steps, and the knob counts re-prime on the next read.
// TRLC-LINKS: REQ-SDS-022
func panelMatrix(f sramcapture.PanelFrame, k *panelKnobs) ([5]uint16, bool) {
	var m [5]uint16
	for i := range m[:4] {
		m[i] = 0xffff
	}
	down := 0
	for row := 0; row < 8; row++ {
		b := uint8(f.Words[row/2] >> (8 * uint(row%2)))
		for pos := 2; pos < 8; pos++ {
			if b&(1<<uint(pos)) == 0 {
				down++
			}
		}
		if k.primed {
			d := int(int8(uint8(f.Knobs[row/2]>>(8*uint(row%2))) - k.last[row]))
			if d > panelMaxCountsRead || d < -panelMaxCountsRead {
				down = panelMaxKeysDown + 1
			}
		}
	}
	if down > panelMaxKeysDown {
		k.primed = false
		return [5]uint16{0xffff, 0xffff, 0xffff, 0xffff, 0}, false
	}
	for row := 0; row < 8; row++ {
		b := uint8(f.Words[row/2] >> (8 * uint(row%2)))
		word, shift := 3-row/2, uint(8)
		if row%2 == 1 {
			shift = 0
		}
		for pos := 2; pos < 8; pos++ {
			if b&(1<<uint(pos)) == 0 {
				m[word] &^= 1 << (panelPositionBit[pos] + shift)
			}
		}
		count := uint8(f.Knobs[row/2] >> (8 * uint(row%2)))
		if k.primed {
			k.pending[row] += panelKnobSign * int(int8(count-k.last[row]))
		}
		k.last[row] = count
	}
	k.primed = true
	for row := 0; row < 8; row++ {
		steps := k.pending[row] / panelCountsPerDetent
		if steps == 0 {
			continue
		}
		if panelSteppedRow[row] {
			if steps > 0 {
				steps = 1
			} else {
				steps = -1
			}
		}
		k.pending[row] -= steps * panelCountsPerDetent
		mag := steps
		if mag < 0 {
			mag = -mag
		}
		if mag > panelMagnitudeMax {
			mag = panelMagnitudeMax
		}
		word, shift := 3-row/2, uint(8)
		if row%2 == 1 {
			shift = 0
		}
		bit := panelPositionBit[0] // clockwise
		if steps < 0 {
			bit = panelPositionBit[1]
		}
		m[word] &^= 1 << (bit + shift)
		m[4] = uint16(mag)
		break
	}
	return m, true
}
