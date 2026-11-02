// Package slots manages the app A/B slot store on the USB stick:
//
//	<root>/A/app            slot A binary
//	<root>/B/app            slot B binary
//	<root>/active           pointer file: "A" or "B"
//	<root>/confirmed        pointer file: last slot that ran stably
//	<root>/staging/         upload staging area
//
// Everything lives on the stick (never instrument NAND). Writes go through a
// temp file + rename; FAT gives no atomicity guarantees, which is why the
// A/B + confirmed design tolerates a torn write.
// ENGMODEL-OWNER-UNIT: FU-OTA-SLOTS
package slots

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The two slot names and the binary's file name inside a slot directory.
const (
	SlotA   = "A"
	SlotB   = "B"
	BinName = "app"
)

// Store is the slot tree under one root directory on the stick.
// TRLC-LINKS: REQ-SDS-087
type Store struct {
	root string
	// sums keeps each slot binary's sha256 against the file it was read
	// from (path, inode, size, mtime): otherwise every status call hashes
	// each ~15 MB binary off the stick again.
	mu   sync.Mutex
	sums map[string]fileSum
}

// fileSum is a file's sha256 and the identity it was computed for.
// TRLC-LINKS: REQ-SDS-087
type fileSum struct {
	ino  uint64
	size int64
	mod  time.Time
	sum  string
}

// New returns the store rooted at root; Init creates its directories.
// TRLC-LINKS: REQ-SDS-087
func New(root string) *Store { return &Store{root: root} }

// Root is the store's root directory.
// TRLC-LINKS: REQ-SDS-087
func (s *Store) Root() string { return s.root }

// Init creates the root, both slot directories and the staging directory.
// TRLC-LINKS: REQ-SDS-087
func (s *Store) Init() error {
	for _, d := range []string{s.root, s.SlotDir(SlotA), s.SlotDir(SlotB), s.StagingDir()} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return err
		}
	}
	return nil
}

// SlotDir is a slot's directory, the app's working directory when it runs.
// TRLC-LINKS: REQ-SDS-087
func (s *Store) SlotDir(slot string) string { return filepath.Join(s.root, slot) }

// BinPath is the path of a slot's app binary.
// TRLC-LINKS: REQ-SDS-087
func (s *Store) BinPath(slot string) string { return filepath.Join(s.SlotDir(slot), BinName) }

// StagingDir is where uploads land before they are installed into a slot.
// TRLC-LINKS: REQ-SDS-087
func (s *Store) StagingDir() string { return filepath.Join(s.root, "staging") }

// TRLC-LINKS: REQ-SDS-087
func readPointer(path, def string) string {
	b, err := os.ReadFile(path) //nolint:gosec // path is one of the store's own pointer files
	if err != nil {
		return def
	}
	v := strings.TrimSpace(string(b))
	if v != SlotA && v != SlotB {
		return def
	}
	return v
}

// TRLC-LINKS: REQ-SDS-087
func writePointer(path, v string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(v+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Active is the slot the supervisor launches: the "active" pointer, A when
// it is missing or unreadable.
// TRLC-LINKS: REQ-SDS-087
func (s *Store) Active() string { return readPointer(filepath.Join(s.root, "active"), SlotA) }

// Confirmed is the last slot that ran stably, the rollback target: the
// "confirmed" pointer, the active slot when it is missing or unreadable.
// TRLC-LINKS: REQ-SDS-087
func (s *Store) Confirmed() string {
	return readPointer(filepath.Join(s.root, "confirmed"), s.Active())
}

// SetActive points the active pointer at slot.
// TRLC-LINKS: REQ-SDS-087
func (s *Store) SetActive(slot string) error {
	if slot != SlotA && slot != SlotB {
		return fmt.Errorf("slots: invalid slot %q", slot)
	}
	return writePointer(filepath.Join(s.root, "active"), slot)
}

// SetConfirmed points the confirmed pointer at slot.
// TRLC-LINKS: REQ-SDS-087
func (s *Store) SetConfirmed(slot string) error {
	if slot != SlotA && slot != SlotB {
		return fmt.Errorf("slots: invalid slot %q", slot)
	}
	return writePointer(filepath.Join(s.root, "confirmed"), slot)
}

// Other returns the inactive slot.
// TRLC-LINKS: REQ-SDS-087
func Other(slot string) string {
	if slot == SlotA {
		return SlotB
	}
	return SlotA
}

// HasBinary reports whether a slot has an app binary.
// TRLC-LINKS: REQ-SDS-087
func (s *Store) HasBinary(slot string) bool {
	fi, err := os.Stat(s.BinPath(slot))
	return err == nil && fi.Mode().IsRegular() && fi.Size() > 0
}

// Install copies src into the given slot's binary path (tmp + rename) and
// returns its sha256.
// TRLC-LINKS: REQ-SDS-087
func (s *Store) Install(slot, src string) (string, error) {
	if slot != SlotA && slot != SlotB {
		return "", fmt.Errorf("slots: invalid slot %q", slot)
	}
	if err := os.MkdirAll(s.SlotDir(slot), 0o750); err != nil {
		return "", err
	}
	return CopyExecutable(src, s.BinPath(slot))
}

// CopyExecutable copies src to dst as an executable (0755): written to
// dst.tmp, synced and renamed over dst, so a power cut leaves either the old
// file or the whole new one. It returns the copy's sha256.
// TRLC-LINKS: REQ-SDS-087, REQ-SDS-119
func CopyExecutable(src, dst string) (string, error) {
	in, err := os.Open(src) //nolint:gosec // src is the operator's upload; the control RPC runs any command anyway
	if err != nil {
		return "", err
	}
	defer func() { _ = in.Close() }() // read-only: a close error loses nothing

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755) //nolint:gosec // dst is a slot binary or the agent's configured A/B path
	if err != nil {
		return "", err
	}
	discard := func(err error) (string, error) {
		_ = out.Close() // the write already failed; that error is the one reported
		_ = os.Remove(tmp)
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		return discard(err)
	}
	if err := out.Sync(); err != nil {
		return discard(err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// FileSHA256 hashes an arbitrary file.
// TRLC-LINKS: REQ-SDS-087
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // hashing a file the caller names is this function's job
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }() // read-only: a close error loses nothing
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Status is the store's state as the agent's status RPC reports it.
// TRLC-LINKS: REQ-SDS-087
type Status struct {
	Active    string            `json:"active"`
	Confirmed string            `json:"confirmed"`
	Binaries  map[string]string `json:"binaries"` // slot -> sha256 ("" if missing)
	Root      string            `json:"root"`
}

// Status reads both pointers and hashes each slot's binary (cached while
// the file is unchanged).
// TRLC-LINKS: REQ-SDS-087
func (s *Store) Status() Status {
	st := Status{Active: s.Active(), Confirmed: s.Confirmed(), Root: s.root, Binaries: map[string]string{}}
	for _, slot := range []string{SlotA, SlotB} {
		if s.HasBinary(slot) {
			sum, err := s.cachedSHA256(s.BinPath(slot))
			if err != nil {
				sum = "unreadable: " + err.Error()
			}
			st.Binaries[slot] = sum
		}
	}
	return st
}

// cachedSHA256 is FileSHA256, reused while the file is the one it hashed.
// Install replaces a binary by rename, a new inode.
// TRLC-LINKS: REQ-SDS-087
func (s *Store) cachedSHA256(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	var ino uint64
	if sys, ok := fi.Sys().(*syscall.Stat_t); ok {
		ino = sys.Ino
	}
	id := fileSum{ino: ino, size: fi.Size(), mod: fi.ModTime()}
	s.mu.Lock()
	c, ok := s.sums[path]
	s.mu.Unlock()
	if ok && c.ino == id.ino && c.size == id.size && c.mod.Equal(id.mod) {
		return c.sum, nil
	}
	sum, err := FileSHA256(path)
	if err != nil {
		return "", err
	}
	id.sum = sum
	s.mu.Lock()
	if s.sums == nil {
		s.sums = map[string]fileSum{}
	}
	s.sums[path] = id
	s.mu.Unlock()
	return sum, nil
}
