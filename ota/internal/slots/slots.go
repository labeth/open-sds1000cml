// Package slots manages the app A/B slot store on the USB stick:
//
//	<root>/A/app            slot A binary
//	<root>/B/app            slot B binary
//	<root>/emergency/app    optional known-good backstop
//	<root>/active           pointer file: "A" or "B"
//	<root>/confirmed        pointer file: last slot that ran stably
//	<root>/staging/         upload staging area
//
// Everything lives on the stick (never instrument NAND). Writes go through a
// temp file + rename; FAT gives no atomicity guarantees, which is why the
// A/B + confirmed design tolerates a torn write.
package slots

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	SlotA         = "A"
	SlotB         = "B"
	SlotEmergency = "emergency"
	BinName       = "app"
)

type Store struct{ root string }

func New(root string) *Store { return &Store{root: root} }

func (s *Store) Root() string { return s.root }

func (s *Store) Init() error {
	for _, d := range []string{s.root, s.SlotDir(SlotA), s.SlotDir(SlotB), s.StagingDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SlotDir(slot string) string { return filepath.Join(s.root, slot) }
func (s *Store) BinPath(slot string) string { return filepath.Join(s.SlotDir(slot), BinName) }
func (s *Store) StagingDir() string         { return filepath.Join(s.root, "staging") }

func readPointer(path, def string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return def
	}
	v := strings.TrimSpace(string(b))
	if v != SlotA && v != SlotB {
		return def
	}
	return v
}

func writePointer(path, v string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(v+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) Active() string { return readPointer(filepath.Join(s.root, "active"), SlotA) }
func (s *Store) Confirmed() string {
	return readPointer(filepath.Join(s.root, "confirmed"), s.Active())
}

func (s *Store) SetActive(slot string) error {
	if slot != SlotA && slot != SlotB {
		return fmt.Errorf("slots: invalid slot %q", slot)
	}
	return writePointer(filepath.Join(s.root, "active"), slot)
}

func (s *Store) SetConfirmed(slot string) error {
	if slot != SlotA && slot != SlotB {
		return fmt.Errorf("slots: invalid slot %q", slot)
	}
	return writePointer(filepath.Join(s.root, "confirmed"), slot)
}

// Other returns the inactive slot.
func Other(slot string) string {
	if slot == SlotA {
		return SlotB
	}
	return SlotA
}

// HasBinary reports whether a slot has an app binary.
func (s *Store) HasBinary(slot string) bool {
	fi, err := os.Stat(s.BinPath(slot))
	return err == nil && fi.Mode().IsRegular() && fi.Size() > 0
}

// Install copies src into the given slot's binary path (tmp + rename) and
// returns its sha256.
func (s *Store) Install(slot, src string) (string, error) {
	if slot != SlotA && slot != SlotB && slot != SlotEmergency {
		return "", fmt.Errorf("slots: invalid slot %q", slot)
	}
	if err := os.MkdirAll(s.SlotDir(slot), 0o755); err != nil {
		return "", err
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()

	dst := s.BinPath(slot)
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		out.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// FileSHA256 hashes an arbitrary file.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type Status struct {
	Active    string            `json:"active"`
	Confirmed string            `json:"confirmed"`
	Binaries  map[string]string `json:"binaries"` // slot -> sha256 ("" if missing)
	Root      string            `json:"root"`
	HasEmerg  bool              `json:"has_emergency"`
}

func (s *Store) Status() Status {
	st := Status{Active: s.Active(), Confirmed: s.Confirmed(), Root: s.root, Binaries: map[string]string{}}
	for _, slot := range []string{SlotA, SlotB, SlotEmergency} {
		if s.HasBinary(slot) {
			sum, err := FileSHA256(s.BinPath(slot))
			if err != nil {
				sum = "unreadable: " + err.Error()
			}
			st.Binaries[slot] = sum
		}
	}
	st.HasEmerg = s.HasBinary(SlotEmergency)
	return st
}
