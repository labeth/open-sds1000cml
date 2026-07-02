package slots

import (
	"os"
	"path/filepath"
	"testing"
)

func mkbin(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestPointerDefaultsAndValidation(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if s.Active() != SlotA {
		t.Errorf("default active = %q, want A", s.Active())
	}
	if s.Confirmed() != SlotA {
		t.Errorf("default confirmed = %q, want A", s.Confirmed())
	}
	if err := s.SetActive("Z"); err == nil {
		t.Error("SetActive should reject invalid slot")
	}
	if err := s.SetActive(SlotB); err != nil {
		t.Fatal(err)
	}
	if s.Active() != SlotB {
		t.Errorf("active = %q after SetActive(B)", s.Active())
	}
	// A garbage pointer file falls back to the default.
	os.WriteFile(filepath.Join(s.Root(), "active"), []byte("garbage"), 0o644)
	if s.Active() != SlotA {
		t.Errorf("garbage pointer should default to A, got %q", s.Active())
	}
}

func TestOther(t *testing.T) {
	if Other(SlotA) != SlotB || Other(SlotB) != SlotA {
		t.Fatal("Other() wrong")
	}
}

func TestInstallAndStatus(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "app")
	mkbin(t, src, "BINARY-CONTENT")

	sum, err := s.Install(SlotB, src)
	if err != nil {
		t.Fatal(err)
	}
	if !s.HasBinary(SlotB) {
		t.Error("slot B should have a binary after install")
	}
	if s.HasBinary(SlotA) {
		t.Error("slot A should be empty")
	}
	got, err := FileSHA256(s.BinPath(SlotB))
	if err != nil || got != sum {
		t.Errorf("sha mismatch: install=%s file=%s err=%v", sum, got, err)
	}

	st := s.Status()
	if st.Binaries[SlotB] != sum {
		t.Errorf("status sha = %q, want %q", st.Binaries[SlotB], sum)
	}
	if _, ok := st.Binaries[SlotA]; ok {
		t.Error("status should not list empty slot A")
	}
}

func TestHasBinaryRejectsEmpty(t *testing.T) {
	s := New(t.TempDir())
	_ = s.Init()
	// zero-byte file is not a valid binary
	mkbin(t, s.BinPath(SlotA), "")
	if s.HasBinary(SlotA) {
		t.Error("empty file should not count as a binary")
	}
}
