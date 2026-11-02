// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT

package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"open-sds/ota/internal/slots"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// uploadSession is a chunked file transfer into the staging area. Chunks may
// arrive out of order (WriteAt); commit verifies size + sha256 then renames
// into place. Sessions are capped and time out so a dropped transfer can't
// leak a temp file forever.
// TRLC-LINKS: REQ-SDS-106
type uploadSession struct {
	mu      sync.Mutex
	id      string
	dest    string
	tmp     string
	f       *os.File
	size    int64
	sha     string
	mode    os.FileMode
	written int64
	created time.Time
	// hash covers bytes [0, hashed) while chunks arrive in order, so commit
	// need not read the file back off the stick; -1 once a chunk lands
	// anywhere else.
	hash   hash.Hash
	hashed int64
}

// TRLC-LINKS: REQ-SDS-106
func (a *Agent) newUpload(dest string, size int64, sha string, mode uint32) (string, error) {
	dest = filepath.Clean(dest)
	if !filepath.IsAbs(dest) {
		return "", errors.New("upload: dest must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return "", err
	}
	a.evictStale()
	a.upMu.Lock()
	defer a.upMu.Unlock()
	if len(a.ups) >= 8 {
		return "", errors.New("upload: too many concurrent sessions")
	}
	id := "up-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	// Temp file in the DESTINATION's directory so the final rename is same-
	// filesystem (a fixed staging dir on the stick can't rename to a /tmp dest
	// — that is a cross-device link error).
	tmp := filepath.Join(filepath.Dir(dest), "."+id+".part")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600) //nolint:gosec // the put RPC writes where the operator says; commit applies the requested mode
	if err != nil {
		return "", err
	}
	m := os.FileMode(mode)
	if m == 0 {
		m = 0o755
	}
	a.ups[id] = &uploadSession{
		id: id, dest: dest, tmp: tmp, f: f, size: size,
		sha: strings.ToLower(sha), mode: m, created: time.Now(),
		hash: sha256.New(),
	}
	return id, nil
}

// evictStale drops the sessions older than 10 minutes and removes their
// partial files. A session is aborted under its own lock, after upMu is
// released: a chunk may be writing to it, and commit takes upMu while holding
// a session's lock.
// TRLC-LINKS: REQ-SDS-106
func (a *Agent) evictStale() {
	var stale []*uploadSession
	a.upMu.Lock()
	for id, s := range a.ups {
		if time.Since(s.created) > 10*time.Minute {
			stale = append(stale, s)
			delete(a.ups, id)
		}
	}
	a.upMu.Unlock()
	for _, s := range stale {
		s.abort()
	}
}

// TRLC-LINKS: REQ-SDS-106
func (a *Agent) getUpload(id string) (*uploadSession, error) {
	a.upMu.Lock()
	defer a.upMu.Unlock()
	s, ok := a.ups[id]
	if !ok {
		return nil, fmt.Errorf("upload: unknown session %q", id)
	}
	return s, nil
}

// TRLC-LINKS: REQ-SDS-106
func (a *Agent) writeUpload(id string, offset int64, data []byte) (int, error) {
	s, err := a.getUpload(id)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return 0, errors.New("upload: session closed")
	}
	if s.size > 0 && offset+int64(len(data)) > s.size {
		return 0, errors.New("upload: chunk exceeds declared size")
	}
	n, err := s.f.WriteAt(data, offset)
	if offset == s.hashed {
		s.hash.Write(data[:n])
		s.hashed += int64(n)
	} else {
		s.hashed = -1
	}
	if err != nil {
		return n, err
	}
	if end := offset + int64(n); end > s.written {
		s.written = end
	}
	return n, nil
}

// TRLC-LINKS: REQ-SDS-106
func (a *Agent) commitUpload(id string) (string, string, error) {
	s, err := a.getUpload(id)
	if err != nil {
		return "", "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return "", "", errors.New("upload: session already committed")
	}
	if err := s.f.Sync(); err != nil {
		return "", "", err
	}
	if err := s.f.Close(); err != nil {
		return "", "", err
	}
	s.f = nil

	if s.size > 0 && s.written != s.size {
		_ = os.Remove(s.tmp)
		return "", "", fmt.Errorf("upload: size mismatch (got %d, want %d)", s.written, s.size)
	}
	// `written` is a high-water mark, not a coverage map: an out-of-order
	// transfer with a missing interior chunk can reach written==size with
	// holes. Require a declared sha256 whenever a size is given so the hash
	// (below) catches any gap — a sparse-zero hole cannot match.
	if s.size > 0 && s.sha == "" {
		_ = os.Remove(s.tmp)
		return "", "", errors.New("upload: sha256 required when size is declared (guards against sparse/out-of-order gaps)")
	}
	var sum string
	if s.hashed == s.written {
		sum = hex.EncodeToString(s.hash.Sum(nil))
	} else if sum, err = slots.FileSHA256(s.tmp); err != nil { // a read error fails the commit, not the hash
		return "", "", err
	}
	if s.sha != "" && sum != s.sha {
		_ = os.Remove(s.tmp)
		return "", "", fmt.Errorf("upload: sha256 mismatch (got %s, want %s)", sum, s.sha)
	}
	if err := os.Chmod(s.tmp, s.mode); err != nil {
		return "", "", err
	}
	if err := os.Rename(s.tmp, s.dest); err != nil {
		return "", "", err
	}
	a.upMu.Lock()
	delete(a.ups, id)
	a.upMu.Unlock()
	return s.dest, sum, nil
}

// abort closes the session and removes its partial file.
// TRLC-LINKS: REQ-SDS-106
func (s *uploadSession) abort() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f != nil {
		_ = s.f.Close() // abandoned: the partial file is removed next
		s.f = nil
	}
	_ = os.Remove(s.tmp)
}
