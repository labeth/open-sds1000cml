// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT

package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TRLC-LINKS: REQ-SDS-106
func TestUploadRejectsGapWhenSizeDeclared(t *testing.T) {
	a := testAgent(t)
	dest := filepath.Join(t.TempDir(), "out.bin")

	// Declare size 1000, no sha, then write only bytes 500..999 — a hole in
	// 0..499. commit must refuse (sha required when size declared).
	id, err := a.newUpload(dest, 1000, "", 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.writeUpload(id, 500, make([]byte, 500)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.commitUpload(id); err == nil {
		t.Fatal("commit with an interior gap and no sha must fail")
	} else if !strings.Contains(err.Error(), "sha256 required") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TRLC-LINKS: REQ-SDS-106
func TestUploadRoundTripWithSHA(t *testing.T) {
	a := testAgent(t)
	dest := filepath.Join(t.TempDir(), "ok.bin")
	data := []byte("hello world payload")
	// sha256 of the data.
	sum, _ := fileSHAOfBytes(data)

	id, err := a.newUpload(dest, int64(len(data)), sum, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	// Two out-of-order chunks that fully cover the range.
	if _, err := a.writeUpload(id, 6, data[6:]); err != nil {
		t.Fatal(err)
	}
	if _, err := a.writeUpload(id, 0, data[:6]); err != nil {
		t.Fatal(err)
	}
	path, gotSum, err := a.commitUpload(id)
	if err != nil {
		t.Fatalf("commit failed: %v", err)
	}
	if gotSum != sum {
		t.Errorf("sha mismatch: %s vs %s", gotSum, sum)
	}
	if path != dest {
		t.Errorf("path = %q, want %q", path, dest)
	}
}

// TRLC-LINKS: REQ-SDS-106
func fileSHAOfBytes(b []byte) (string, error) {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// A stale session is evicted while a chunk may still be writing to it: the
// eviction waits for the session's own lock, so it never closes the file
// under a chunk in progress.
// TRLC-LINKS: REQ-SDS-106
func TestUploadEvictionWaitsForAChunk(t *testing.T) {
	a := testAgent(t)
	dir := t.TempDir()
	id, err := a.newUpload(filepath.Join(dir, "old.bin"), 0, "", 0o644)
	if err != nil {
		t.Fatal(err)
	}
	s, err := a.getUpload(id)
	if err != nil {
		t.Fatal(err)
	}
	s.created = time.Now().Add(-time.Hour)

	s.mu.Lock() // a chunk in progress
	done := make(chan error, 1)
	go func() {
		_, err := a.newUpload(filepath.Join(dir, "new.bin"), 0, "", 0o644)
		done <- err
	}()
	var evictErr error
	early := false
	select {
	case evictErr = <-done:
		early = true
	case <-time.After(200 * time.Millisecond):
	}
	if s.f == nil {
		t.Error("eviction closed the file under a chunk in progress")
	}
	s.mu.Unlock()
	if !early {
		evictErr = <-done
	}
	if evictErr != nil {
		t.Fatal(evictErr)
	}
	if _, err := a.getUpload(id); err == nil {
		t.Error("the stale session survived eviction")
	}
}
