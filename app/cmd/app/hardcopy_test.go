package main

import (
	"os"
	"path/filepath"

	"open-sds/app/internal/lcd"
	"open-sds/app/internal/panel/wsview"
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

// A key that changes only a value in a sheet, a pane's text, the rail label
// or a notice repaints a held display (bug hunt #2: TRIGGER Type
// Pulse->Slope while stopped left "Pulse" on the LCD); the same snapshot
// rebuilt (new pointers, the same content) does not.
// TRLC-LINKS: REQ-SDS-168, REQ-SDS-211
func TestRenderSigSeesUIChanges(t *testing.T) {
	view := func(typ string) wsview.View {
		return wsview.View{Layout: wsview.Layout{MenuW: 116, Sheet: true},
			Sheet: &wsview.Sheet{WSName: "SCOPE", Rows: []wsview.Row{{Kind: wsview.Enum, Label: "Type",
				Opts: []wsview.Opt{{Text: "Pulse", Sel: typ == "Pulse"}, {Text: "Slope", Sel: typ == "Slope"}}}},
				Sub: &wsview.Sub{Title: "x"}},
			Keys: &wsview.Keys{Title: "TYPE"}}
	}
	h := lcd.HUD{UI: view("Pulse")}
	a := renderSigOf(nil, h, false)
	if h.UI = view("Pulse"); renderSigOf(nil, h, false) != a {
		t.Fatal("the same UI rebuilt changed the render signature")
	}
	if h.UI = view("Slope"); renderSigOf(nil, h, false) == a {
		t.Fatal("a sheet value change left the render signature unchanged")
	}
	h.UI = view("Pulse")
	h.UI.RailNote = &wsview.Note{Head: "F1 SCOPE"}
	if renderSigOf(nil, h, false) == a {
		t.Fatal("the rail label left the render signature unchanged")
	}
	h.UI = view("Pulse")
	h.Notice = "SAVED SDS0001.png"
	if renderSigOf(nil, h, false) == a {
		t.Fatal("a PRINT notice left the render signature unchanged")
	}
	h.Notice = ""
	h.Remote = "REMOTE  TRIG level 1.25V"
	if renderSigOf(nil, h, false) == a {
		t.Fatal("a REMOTE note left the render signature unchanged")
	}
}
