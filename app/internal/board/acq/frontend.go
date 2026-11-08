// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"slices"
	"sync"
	"sync/atomic"

	"github.com/labeth/open-dso-core/hal"
)

// Previewer is a front end that says what a Set would apply without
// applying it (*analog.FrontEnd): the board stages with it, and applies
// the staged settings at the next Arm.
type Previewer interface {
	hal.FrontEnd
	Preview(ch int, c hal.Channel) (hal.Channel, error)
}

// Writer is a front end whose setters leave their hardware writes (the
// relays and gains) for the acquisition owner (*analog.FrontEnd): the
// board writes them while no capture is armed.
type Writer interface {
	Pending() bool
	Flush() (wrote bool, err error)
}

// frontEndFacet is the analog front end behind the HAL's staging: Set
// stages a channel's setting (snapped, or refused, by the front end's
// Preview), Get reads what is applied, and Acquirer.Arm applies what is
// staged before it arms. A front end without Preview applies at Set.
type frontEndFacet struct {
	hal.FrontEnd
	pv Previewer // nil: Set applies at once
	w  Writer    // nil: the front end writes its hardware itself

	mu      sync.Mutex
	staged  []hal.Channel // a channel's staged setting
	has     []bool        // whether staged[ch] holds one
	staging atomic.Bool   // some has[ch]: the owner's reads of the pending state skip the lock otherwise
}

// newFrontEndFacet stages over fe.
// TRLC-LINKS: REQ-SDS-015
func newFrontEndFacet(fe hal.FrontEnd) *frontEndFacet {
	f := &frontEndFacet{FrontEnd: fe, staged: make([]hal.Channel, 2), has: make([]bool, 2)}
	f.pv, _ = fe.(Previewer)
	f.w, _ = fe.(Writer)
	return f
}

// Set stages c for channel ch and returns what the next Arm applies: the
// front end's snapped values, or its refusal (which leaves what was
// staged). A setting equal to what is applied stages nothing. COMMIT.
// TRLC-LINKS: REQ-SDS-015, REQ-SDS-095, REQ-SDS-096, REQ-SDS-097
func (f *frontEndFacet) Set(ch int, c hal.Channel) (hal.Channel, error) {
	if f.pv == nil {
		return f.FrontEnd.Set(ch, c)
	}
	got, err := f.pv.Preview(ch, c)
	if err != nil {
		return f.pending(ch), err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.staged[ch], f.has[ch] = got, got != f.Get(ch)
	f.staging.Store(slices.Contains(f.has, true))
	return got, nil
}

// pending is channel ch as the next Arm leaves it: its staged setting, or
// what is applied.
// TRLC-LINKS: REQ-SDS-015
func (f *frontEndFacet) pending(ch int) hal.Channel {
	if !f.staging.Load() {
		return f.Get(ch)
	}
	f.mu.Lock()
	if ch >= 0 && ch < len(f.has) && f.has[ch] {
		c := f.staged[ch]
		f.mu.Unlock()
		return c
	}
	f.mu.Unlock()
	return f.Get(ch)
}

// commit applies what is staged and reports whether anything was. OWNER.
// TRLC-LINKS: REQ-SDS-015
func (f *frontEndFacet) commit() (bool, error) {
	if !f.staging.Load() {
		return false, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	defer f.staging.Store(false)
	applied := false
	for ch := range f.has {
		if !f.has[ch] {
			continue
		}
		f.has[ch], applied = false, true
		if _, err := f.FrontEnd.Set(ch, f.staged[ch]); err != nil {
			return true, err
		}
	}
	return applied, nil
}

// pendingWrite reports that the front end has a hardware write due: a
// setting staged here, or one its own setters left. ANY.
// TRLC-LINKS: REQ-SDS-015
func (f *frontEndFacet) pendingWrite() bool {
	return f.staging.Load() || f.w != nil && f.w.Pending()
}

// write applies what is staged and writes the front end's due hardware
// words, and reports whether anything reached the hardware. OWNER, with
// no capture armed.
// TRLC-LINKS: REQ-SDS-015
func (f *frontEndFacet) write() (bool, error) {
	applied, err := f.commit()
	if err != nil || f.w == nil {
		return applied, err
	}
	wrote, err := f.w.Flush()
	return applied || wrote, err
}

// flush writes the front end's due hardware words, what its own setters
// left; settings staged here wait for Arm. OWNER, with no capture armed.
// TRLC-LINKS: REQ-SDS-015
func (f *frontEndFacet) flush() error {
	if f.w == nil {
		return nil
	}
	_, err := f.w.Flush()
	return err
}
