// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"strings"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
	"open-sds/app/internal/settings"
)

// SetupMemory stores and recalls whole setups in numbered slots (the same
// store as SCPI *SAV/*RCL).
type SetupMemory interface {
	SaveSetup(slot int) error
	RecallSetup(slot int) error
}

// setupPeeker reads a slot without applying it (for the FILE rows).
type setupPeeker interface {
	PeekSetup(slot int) (settings.Settings, bool)
}

// setupCurrent gives the whole setup a save would write now (the Save/Recall
// lamp compares it).
type setupCurrent interface {
	CurrentSetup() settings.Settings
}

// setupCheckEvery is how often, without an input, the Save/Recall lamp
// compares the settings with the setup last saved or recalled (a web page or
// SCPI can change them).
const setupCheckEvery = 250 * time.Millisecond

// noteSetup takes the settings now as those of slot n.
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) noteSetup(n int) {
	print := setupPrint(c.snap())
	c.mu.Lock()
	m := c.setups
	c.mu.Unlock()
	var cur settings.Settings
	if sc, ok := m.(setupCurrent); ok {
		cur = sc.CurrentSetup()
	}
	c.mu.Lock()
	c.lastSetup, c.lastSetupPrint, c.lastSetupSt = n, print, cur
	c.setupSameAt = time.Time{}
	c.mu.Unlock()
}

// sameAsSavedSetup reports whether the settings are still those last saved
// or recalled: the whole setup a save writes, compared again after an input
// and otherwise at most every setupCheckEvery. A store that cannot give it
// compares setupPrint.
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) sameAsSavedSetup(s *sv) bool {
	c.mu.Lock()
	last, print, base, m := c.lastSetup, c.lastSetupPrint, c.lastSetupSt, c.setups
	same, at := c.setupSame, c.setupSameAt
	c.mu.Unlock()
	if last <= 0 || !s.slotUsed[last] {
		return false
	}
	sc, ok := m.(setupCurrent)
	if !ok {
		return print == setupPrint(s)
	}
	if !at.IsZero() && s.now.Sub(at) < setupCheckEvery && !s.now.Before(at) {
		return same
	}
	same = sc.CurrentSetup() == base
	c.mu.Lock()
	c.setupSame, c.setupSameAt = same, s.now
	c.mu.Unlock()
	return same
}

// setupSlots is the FILE tab's slot count (the SCPI *SAV range).
const setupSlots = 20

// SetSetupMemory wires the FILE tab (Save/Recall key) to a setup store.
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) SetSetupMemory(m SetupMemory) {
	c.mu.Lock()
	c.setups = m
	c.mu.Unlock()
	for n := 1; n <= setupSlots; n++ {
		c.refreshSlot(n)
	}
}

// refreshSlot reads slot n's summary for its FILE row.
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) refreshSlot(n int) {
	c.mu.Lock()
	m := c.setups
	c.mu.Unlock()
	p, ok := m.(setupPeeker)
	if !ok {
		return
	}
	st, used := p.PeekSetup(n)
	c.mu.Lock()
	c.slotUsed[n], c.slotSum[n], c.slotWS[n] = used, "", -1
	if used {
		c.slotSum[n] = setupSummary(st)
		if st.UI.Set {
			c.slotWS[n] = clampInt(st.UI.Workspace, 0, wsview.Count-1)
		}
	}
	c.mu.Unlock()
}

// slotWorkspace is the workspace a slot holds (-1 unknown).
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) slotWorkspace(n int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.slotWS[n]
}

// setupSummary is a slot's FILE row: "BUS  500us/div  C1^ 0V  C1 1V C2 1V  UART".
// TRLC-LINKS: REQ-SDS-136
func setupSummary(st settings.Settings) string {
	var parts []string
	if st.UI.Set {
		parts = append(parts, wsview.Names[clampInt(st.UI.Workspace, 0, wsview.Count-1)])
	}
	parts = append(parts, si(st.TdivS, "s")+"/div")
	edge := "^"
	if !st.Trigger.Rising {
		edge = "v"
	}
	tr := []string{"", "PLS ", "SLP ", "VID "}[clampInt(st.Trigger.Type, 0, 3)]
	parts = append(parts, fmt.Sprintf("%sC%d%s %s", tr, st.Trigger.Source+1, edge, si(engine.TrigLevelVolts(uint16(clampInt(st.Trigger.LevelCode, 1, 65535))), "V")))
	var ch []string
	for i := 0; i < 2; i++ {
		if !st.Display.Hidden[i] && st.Ch[i].VdivV > 0 {
			ch = append(ch, fmt.Sprintf("C%d %s", i+1, si(st.Ch[i].VdivV*max(st.Ch[i].Probe, 1), "V")))
		}
	}
	parts = append(parts, strings.Join(ch, " "))
	if st.Decode.Proto != framecalc.ProtoOff {
		parts = append(parts, strings.ToUpper(protoName(st.Decode.Proto)))
	}
	if st.ViewMode != viewYT {
		parts = append(parts, strings.ToUpper(viewName(st.ViewMode)))
	}
	return strings.Join(parts, "  ")
}

// SharedSetups is the panel's setup store as the web page and SCPI use it:
// the same slots, and the FILE rows and the SAVE/RECALL lamp follow a save or
// recall made there as they follow the scope's own (a slot saved from the web
// read "empty" on the scope until a restart; bug hunt #3).
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) SharedSetups() *SharedSetups { return &SharedSetups{c: c} }

// SharedSetups wraps the store SetSetupMemory wired.
type SharedSetups struct{ c *Controller }

// SetupSlot is one setup slot as the FILE tab shows it.
type SetupSlot struct {
	Slot    int
	Used    bool
	Summary string
}

func (s *SharedSetups) store() SetupMemory {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	return s.c.setups
}

// SaveSetup saves slot n and updates its FILE row and the lamp.
// TRLC-LINKS: REQ-SDS-136
func (s *SharedSetups) SaveSetup(n int) error {
	m := s.store()
	if m == nil {
		return fmt.Errorf("setup memory unavailable")
	}
	if err := m.SaveSetup(n); err != nil {
		return err
	}
	s.c.refreshSlot(n)
	s.c.noteSetup(n)
	return nil
}

// RecallSetup recalls slot n and lights the lamp.
// TRLC-LINKS: REQ-SDS-136
func (s *SharedSetups) RecallSetup(n int) error {
	m := s.store()
	if m == nil {
		return fmt.Errorf("setup memory unavailable")
	}
	if err := m.RecallSetup(n); err != nil {
		return err
	}
	s.c.noteSetup(n)
	return nil
}

// SaveSetupFile saves a named setup (SCPI STPN) when the store keeps them.
// TRLC-LINKS: REQ-SDS-136
func (s *SharedSetups) SaveSetupFile(name string) error {
	f, ok := s.store().(interface{ SaveSetupFile(string) error })
	if !ok {
		return fmt.Errorf("named setups unavailable")
	}
	return f.SaveSetupFile(name)
}

// RecallSetupFile recalls a named setup (SCPI RCPN) when the store keeps them.
// TRLC-LINKS: REQ-SDS-136
func (s *SharedSetups) RecallSetupFile(name string) error {
	f, ok := s.store().(interface{ RecallSetupFile(string) error })
	if !ok {
		return fmt.Errorf("named setups unavailable")
	}
	return f.RecallSetupFile(name)
}

// Slots lists the slots as the FILE rows show them.
// TRLC-LINKS: REQ-SDS-136
func (s *SharedSetups) Slots() []SetupSlot {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	out := make([]SetupSlot, 0, setupSlots)
	for n := 1; n <= setupSlots; n++ {
		out = append(out, SetupSlot{Slot: n, Used: s.c.slotUsed[n], Summary: s.c.slotSum[n]})
	}
	return out
}

// saveSlot stores every setting and the workspace in slot n.
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) saveSlot(n int) error {
	c.mu.Lock()
	m := c.setups
	c.mu.Unlock()
	if m == nil {
		return fmt.Errorf("setup memory unavailable")
	}
	if err := m.SaveSetup(n); err != nil {
		c.logf("panel: setup slot %d: %v", n, err)
		return fmt.Errorf("save failed")
	}
	c.refreshSlot(n)
	c.noteSetup(n)
	c.mu.Lock()
	c.toastLocked(fmt.Sprintf("Setup saved to slot %d", n), 1500*time.Millisecond)
	c.mu.Unlock()
	return nil
}

// recallSlot restores slot n: its settings and the workspace it was saved in.
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) recallSlot(n int) error {
	c.mu.Lock()
	m := c.setups
	c.mu.Unlock()
	if m == nil {
		return fmt.Errorf("setup memory unavailable")
	}
	if err := m.RecallSetup(n); err != nil {
		return fmt.Errorf("setup %d is empty", n)
	}
	c.noteSetup(n)
	c.mu.Lock()
	c.toastLocked(fmt.Sprintf("Setup %d recalled into %s", n, wsview.Names[c.ui.ws]), 1600*time.Millisecond)
	c.mu.Unlock()
	return nil
}

// SetHardcopy wires the PRINT key: fn saves a screenshot and returns where.
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) SetHardcopy(fn func() (string, error)) {
	c.mu.Lock()
	c.hardcopy = fn
	c.mu.Unlock()
}

// printScreen saves a hardcopy off the panel goroutine and shows the result
// for a few seconds.
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) printScreen() {
	c.mu.Lock()
	fn := c.hardcopy
	c.mu.Unlock()
	if fn == nil {
		c.toast("PRINT: unavailable", 3*time.Second)
		return
	}
	go func() {
		name, err := fn()
		if err != nil {
			c.logf("panel: print: %v", err)
			c.toast("PRINT failed", 3*time.Second)
			return
		}
		c.toast("SAVED "+name, 3*time.Second)
	}()
}
