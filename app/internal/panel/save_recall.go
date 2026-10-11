// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"time"
)

// SetupMemory stores and recalls whole setups in numbered slots (the same
// store as SCPI *SAV/*RCL).
type SetupMemory interface {
	SaveSetup(slot int) error
	RecallSetup(slot int) error
}

// setupSlots is the SAVE/RCL page's slot count (the SCPI *SAV range).
const setupSlots = 20

// SetSetupMemory wires the SAVE/RECALL key's page to a setup store.
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) SetSetupMemory(m SetupMemory) {
	c.mu.Lock()
	c.setups = m
	c.mu.Unlock()
}

// saveRecall saves or recalls the page's slot. The SAVE/RECALL key was
// claim-and-ignore (bug hunt 2026-10-11). A recall applies the setup through
// the panel's own setters, which take c.mu, so it runs off the panel
// goroutine; the page shows the outcome.
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) saveRecall(save bool) {
	c.mu.Lock()
	m, slot := c.setups, c.saveSlot
	if m == nil {
		c.saveMsg = "unavailable"
		c.mu.Unlock()
		return
	}
	c.saveMsg = "busy"
	c.mu.Unlock()
	go func() {
		var err error
		msg := ""
		if save {
			err, msg = m.SaveSetup(slot), fmt.Sprintf("saved %d", slot)
		} else {
			err, msg = m.RecallSetup(slot), fmt.Sprintf("recalled %d", slot)
		}
		if err != nil {
			msg = fmt.Sprintf("slot %d empty", slot)
			if save {
				msg = "save failed"
			}
			c.logf("panel: setup slot %d: %v", slot, err)
		}
		c.mu.Lock()
		c.saveMsg = msg
		c.mu.Unlock()
	}()
}

// SetHardcopy wires the PRINT key: fn saves a screenshot and returns where.
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) SetHardcopy(fn func() (string, error)) {
	c.mu.Lock()
	c.hardcopy = fn
	c.mu.Unlock()
}

// printScreen saves a hardcopy off the panel goroutine and shows the result
// for a few seconds. PRINT was claim-and-ignore (bug hunt #2).
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) printScreen() {
	c.mu.Lock()
	fn := c.hardcopy
	c.mu.Unlock()
	note := func(msg string) {
		c.mu.Lock()
		c.notice, c.noticeUntil = msg, time.Now().Add(3*time.Second)
		c.mu.Unlock()
	}
	if fn == nil {
		note("PRINT: unavailable")
		return
	}
	go func() {
		name, err := fn()
		if err != nil {
			c.logf("panel: print: %v", err)
			note("PRINT failed")
			return
		}
		note("SAVED " + name)
	}()
}
