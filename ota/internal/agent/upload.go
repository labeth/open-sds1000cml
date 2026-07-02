package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
}

func (a *Agent) newUpload(dest string, size int64, sha string, mode uint32) (string, error) {
	dest = filepath.Clean(dest)
	if !filepath.IsAbs(dest) {
		return "", fmt.Errorf("upload: dest must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	a.upMu.Lock()
	defer a.upMu.Unlock()
	// Evict stale sessions (> 10 min) and cap concurrency.
	for id, s := range a.ups {
		if time.Since(s.created) > 10*time.Minute {
			s.abort()
			delete(a.ups, id)
		}
	}
	if len(a.ups) >= 8 {
		return "", fmt.Errorf("upload: too many concurrent sessions")
	}
	id := "up-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	// Temp file in the DESTINATION's directory so the final rename is same-
	// filesystem (a fixed staging dir on the stick can't rename to a /tmp dest
	// — that is a cross-device link error).
	tmp := filepath.Join(filepath.Dir(dest), "."+id+".part")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o644)
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
	}
	return id, nil
}

func (a *Agent) getUpload(id string) (*uploadSession, error) {
	a.upMu.Lock()
	defer a.upMu.Unlock()
	s, ok := a.ups[id]
	if !ok {
		return nil, fmt.Errorf("upload: unknown session %q", id)
	}
	return s, nil
}

func (a *Agent) writeUpload(id string, offset int64, data []byte) (int, error) {
	s, err := a.getUpload(id)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return 0, fmt.Errorf("upload: session closed")
	}
	if s.size > 0 && offset+int64(len(data)) > s.size {
		return 0, fmt.Errorf("upload: chunk exceeds declared size")
	}
	n, err := s.f.WriteAt(data, offset)
	if err != nil {
		return n, err
	}
	if end := offset + int64(n); end > s.written {
		s.written = end
	}
	return n, nil
}

func (a *Agent) commitUpload(id string) (string, string, error) {
	s, err := a.getUpload(id)
	if err != nil {
		return "", "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return "", "", fmt.Errorf("upload: session already committed")
	}
	if err := s.f.Sync(); err != nil {
		return "", "", err
	}
	if err := s.f.Close(); err != nil {
		return "", "", err
	}
	s.f = nil

	if s.size > 0 && s.written != s.size {
		os.Remove(s.tmp)
		return "", "", fmt.Errorf("upload: size mismatch (got %d, want %d)", s.written, s.size)
	}
	// `written` is a high-water mark, not a coverage map: an out-of-order
	// transfer with a missing interior chunk can reach written==size with
	// holes. Require a declared sha256 whenever a size is given so the hash
	// (below) catches any gap — a sparse-zero hole cannot match.
	if s.size > 0 && s.sha == "" {
		os.Remove(s.tmp)
		return "", "", fmt.Errorf("upload: sha256 required when size is declared (guards against sparse/out-of-order gaps)")
	}
	sum, err := fileSHA(s.tmp)
	if err != nil {
		return "", "", err
	}
	if s.sha != "" && sum != s.sha {
		os.Remove(s.tmp)
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

func (s *uploadSession) abort() {
	if s.f != nil {
		s.f.Close()
		s.f = nil
	}
	os.Remove(s.tmp)
}

func fileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 64*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
