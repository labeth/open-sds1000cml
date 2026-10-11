package main

import (
	"os"
	"path/filepath"

	"open-sds/app/internal/lcd"
	"testing"
)

// PRINT numbers its screenshots and never overwrites one (bug hunt #2: the
// key was claim-and-ignore).
// TRLC-LINKS: REQ-SDS-136
func TestSaveHardcopyNumbers(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SDS0001.png"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	name, err := saveHardcopy(dir, []byte("png"))
	if err != nil || name != "SDS0002.png" {
		t.Fatalf("got %q, %v; want SDS0002.png", name, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "SDS0001.png")); string(b) != "old" {
		t.Fatal("overwrote an existing screenshot")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, name)); string(b) != "png" {
		t.Fatalf("content %q", b)
	}
}

// A softkey that changes only a menu value repaints a held display (bug hunt
// #2: TRIGGER Type Pulse->Slope while stopped left "Pulse" on the LCD).
// TRLC-LINKS: REQ-SDS-168
func TestRenderSigSeesMenuValues(t *testing.T) {
	h := lcd.HUD{MenuOpen: true, MenuTitle: "TRIGGER", MenuSel: 3,
		MenuItems: []lcd.MenuItem{{Label: "Type", Value: "Pulse"}}}
	a := renderSigOf(nil, h, false)
	h.MenuItems = []lcd.MenuItem{{Label: "Type", Value: "Slope"}}
	if renderSigOf(nil, h, false) == a {
		t.Fatal("menu value change left the render signature unchanged")
	}
	h.Notice = "SAVED SDS0001.png"
	if renderSigOf(nil, h, false) == a {
		t.Fatal("a PRINT notice left the render signature unchanged")
	}
}
