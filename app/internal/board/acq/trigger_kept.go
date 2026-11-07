// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"slices"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/hal/prototrig"
)

// keptSpec is the trigger spec the staged trigger was planned from, copied
// deep, with the board's inputs then: the owner programs its trigger at
// every pass, and a spec it programs again at the same inputs is the same
// trigger, which the board knows without planning it again (the fabric's
// configuration is 2.7 KB, and planning it at every pass and every look at
// the settings cost the Cortex-A8 several points of CPU).
//
// Only the shapes the owner sends are kept: one stage with its levels, a
// qualifier, a video line, zones and a bus request by pointer. A spec with
// any other part (a gate, a pattern, a typed protocol trigger, a bus by
// value, a reset stage) is planned every time.
type keptSpec struct {
	ok    bool
	in    trigInputs
	spec  hal.TriggerSpec // Stages is stage; Reset nil
	stage [1]hal.TrigStage
	qual  hal.Qualifier
	video hal.Video
	zones []hal.Zone
	bus   prototrig.Bus
	bytes []int
	pat   []prototrig.PatternElem
	arinc []float64
}

// keepable reports whether the board keeps spec s to know a repeat.
// TRLC-LINKS: REQ-SDS-011
func keepable(s *hal.TriggerSpec) bool {
	if len(s.Stages) != 1 || s.Reset != nil {
		return false
	}
	g := &s.Stages[0]
	if g.Gate != nil || g.Pattern != nil {
		return false
	}
	if g.Proto == nil {
		return true
	}
	bus, ok := g.Proto.(*prototrig.Bus)
	return ok && bus != nil
}

// keep keeps spec s, programmed at inputs in.
// TRLC-LINKS: REQ-SDS-011
func (k *keptSpec) keep(s *hal.TriggerSpec, in *trigInputs) {
	k.ok = keepable(s)
	if !k.ok {
		return
	}
	k.in = *in
	k.spec = *s
	k.stage[0] = s.Stages[0]
	k.spec.Stages = k.stage[:]
	g := &k.stage[0]
	if g.Qual != nil {
		k.qual = *g.Qual
		g.Qual = &k.qual
	}
	if g.Video != nil {
		k.video = *g.Video
		g.Video = &k.video
	}
	if g.Zones != nil {
		k.zones = append(k.zones[:0], g.Zones...)
		g.Zones = k.zones
	}
	if bus, ok := g.Proto.(*prototrig.Bus); ok {
		k.bus = *bus
		k.bus.Request.Bytes = keepSlice(&k.bytes, bus.Request.Bytes)
		k.bus.Request.Pattern = keepSlice(&k.pat, bus.Request.Pattern)
		k.bus.ARINCSlicer = keepSlice(&k.arinc, bus.ARINCSlicer)
		g.Proto = &k.bus
	}
}

// keepSlice copies src into the storage of *dst, nil kept nil.
// TRLC-LINKS: REQ-SDS-011
func keepSlice[E any](dst *[]E, src []E) []E {
	if src == nil {
		return nil
	}
	*dst = append((*dst)[:0], src...)
	return *dst
}

// matches reports that spec s at inputs in is the kept spec at its inputs.
// TRLC-LINKS: REQ-SDS-011
func (k *keptSpec) matches(s *hal.TriggerSpec, in *trigInputs) bool {
	if !k.ok || *in != k.in || !keepable(s) {
		return false
	}
	a, b := &k.spec, s
	if a.HoldoffS != b.HoldoffS || a.HoldoffEv != b.HoldoffEv || a.Sweep != b.Sweep ||
		a.Coupling != b.Coupling || a.NoiseReject != b.NoiseReject || a.FreeRun != b.FreeRun {
		return false
	}
	return stageEqual(&a.Stages[0], &b.Stages[0])
}

// stageEqual reports that two keepable stages are equal.
// TRLC-LINKS: REQ-SDS-011
func stageEqual(a, b *hal.TrigStage) bool {
	if a.Node != b.Node || a.Source != b.Source || a.Levels != b.Levels || a.Timeout != b.Timeout ||
		a.DelayS != b.DelayS || a.Count != b.Count {
		return false
	}
	if !ptrEqual(a.Qual, b.Qual) || !ptrEqual(a.Video, b.Video) || !sliceEqual(a.Zones, b.Zones) {
		return false
	}
	if a.Proto == nil || b.Proto == nil {
		return a.Proto == b.Proto
	}
	ab, aBus := a.Proto.(*prototrig.Bus)
	bb, bBus := b.Proto.(*prototrig.Bus)
	return aBus && bBus && ab.Equal(bb)
}

// ptrEqual reports that two optional parts are both absent or equal.
// TRLC-LINKS: REQ-SDS-011
func ptrEqual[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// sliceEqual reports that two slices are both nil or hold equal elements.
// TRLC-LINKS: REQ-SDS-011
func sliceEqual[E comparable](a, b []E) bool {
	return (a == nil) == (b == nil) && slices.Equal(a, b)
}
