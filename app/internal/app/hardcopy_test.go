package app

import (
	"os"
	"path/filepath"
	"testing"
)

// PRINT numbers its screenshots and never overwrites one.
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
