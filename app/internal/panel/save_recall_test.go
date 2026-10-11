// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type memSlots struct {
	mu    sync.Mutex
	saved map[int]bool
	rec   []int
}

func (m *memSlots) SaveSetup(n int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saved[n] = true
	return nil
}

func (m *memSlots) RecallSetup(n int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.saved[n] {
		return errors.New("empty")
	}
	m.rec = append(m.rec, n)
	return nil
}

// The SAVE/RECALL key opens a page that saves and recalls numbered setups
// (it was claim-and-ignore; bug hunt 2026-10-11).
// TRLC-LINKS: REQ-SDS-136
func TestSaveRecallKeyPage(t *testing.T) {
	c, _, _ := newC(t)
	m := &memSlots{saved: map[int]bool{}}
	c.SetSetupMemory(m)
	c.button(btnSaveRecall)
	if v := c.MenuView(); v.Title != "SAVE/RCL" {
		t.Fatalf("SAVE/RECALL key opened %q", v.Title)
	}
	result := func() string {
		for i := 0; i < 100; i++ {
			if r := c.MenuView().Items[3].Value; r != "busy" {
				return r
			}
			time.Sleep(5 * time.Millisecond)
		}
		return "busy"
	}
	c.button(btnF1) // slot 1 -> 2
	c.button(btnF3) // recall an empty slot
	if r := result(); r != "slot 2 empty" {
		t.Fatalf("recall of an empty slot: %q", r)
	}
	c.button(btnF2)
	if r := result(); r != "saved 2" {
		t.Fatalf("save: %q", r)
	}
	c.button(btnF3)
	if r := result(); r != "recalled 2" || len(m.rec) != 1 || m.rec[0] != 2 {
		t.Fatalf("recall: %q %v", r, m.rec)
	}
}

// PRINT saves through the hardcopy hook and shows the file name; without a
// hook it says so (bug hunt #2: the key was claim-and-ignore).
// TRLC-LINKS: REQ-SDS-136
func TestPrintKeyShowsResult(t *testing.T) {
	c, _, _ := newC(t)
	press := func(c *Controller, name string) {
		if !c.InjectButton(name) {
			t.Fatalf("%s rejected", name)
		}
		(<-c.inject)()
	}
	press(c, "print")
	if n := c.MenuView().Notice; n != "PRINT: unavailable" {
		t.Fatalf("no hook: notice %q", n)
	}
	done := make(chan struct{})
	c.SetHardcopy(func() (string, error) { defer close(done); return "SDS0007.png", nil })
	press(c, "print")
	<-done
	deadline := time.Now().Add(time.Second)
	for c.MenuView().Notice != "SAVED SDS0007.png" {
		if time.Now().After(deadline) {
			t.Fatalf("notice %q", c.MenuView().Notice)
		}
		time.Sleep(time.Millisecond)
	}
}
