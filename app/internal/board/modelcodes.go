// ENGMODEL-OWNER-UNIT: FU-APP-ANALOG

package board

import (
	"sync"

	"github.com/labeth/open-dso-core/instrument"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// ModelCodes is the levels' engine over the instrument model alone: the
// DAC codes the controllers chose (the trigger comparator's and the
// offsets'), kept as the engine keeps them, beside the model's state. With
// it Levels converts and keeps exactly what it does over the engine, and
// projects the comparator's level into the model (Levels.Project); the
// board stages the codes from the model at its commit (Levels.LevelCode).
type ModelCodes struct {
	m *instrument.Model

	mu   sync.Mutex
	trig uint16    // the comparator's code; 0 never set (the boot comparator)
	off  [2]uint16 // the offsets' codes; 0 never set (the inherited boot offset)
}

var _ Engine = (*ModelCodes)(nil)

// NewModelCodes keeps the codes beside m.
// TRLC-LINKS: REQ-SDS-098, REQ-SDS-095
func NewModelCodes(m *instrument.Model) *ModelCodes { return &ModelCodes{m: m} }

// Snapshot is the model's state with the codes kept.
// TRLC-LINKS: REQ-SDS-098, REQ-SDS-095
func (c *ModelCodes) Snapshot() engine.Stats { return c.overlay(c.m.Snapshot()) }

// overlay is st, a snapshot of the scope's controls, with the codes kept:
// the controls over the model alone carry none.
// TRLC-LINKS: REQ-SDS-098, REQ-SDS-095
func (c *ModelCodes) overlay(st engine.Stats) engine.Stats {
	c.mu.Lock()
	st.TrigCode, st.OffC1, st.OffC2 = c.trig, c.off[0], c.off[1]
	c.mu.Unlock()
	return st
}

// SetTrigLevelCode keeps the comparator code given within the DAC's operating
// window, as the engine does; 0 is no level and changes nothing.
// TRLC-LINKS: REQ-SDS-098
func (c *ModelCodes) SetTrigLevelCode(code uint16) uint16 {
	if code == 0 {
		return 0
	}
	code = min(max(code, engine.TrigCodeMin), engine.TrigCodeMax)
	c.mu.Lock()
	c.trig = code
	c.mu.Unlock()
	return code
}

// SetOffsetDAC keeps channel ch's offset code (the front end's offset
// hook).
// TRLC-LINKS: REQ-SDS-095
func (c *ModelCodes) SetOffsetDAC(ch int, code uint16) {
	c.mu.Lock()
	c.off[ch&1] = code
	c.mu.Unlock()
}
