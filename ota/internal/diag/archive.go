// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry is one member of the archive as the manifest lists it.
type Entry struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	From   string `json:"from,omitempty"` // the device path it was read from
	Note   string `json:"note,omitempty"`
}

// Archive is the one file the stick leaves behind: a gzip'd tar of every
// collector's output, its manifest and the errors met on the way. It is safe
// for concurrent use; members are written in the order they are added.
type Archive struct {
	mu       sync.Mutex
	gz       *gzip.Writer
	tw       *tar.Writer
	entries  []Entry
	errs     []string
	maxTotal int64 // members past this many bytes are listed, not stored
	total    int64
}

// NewArchive writes a new archive to w; maxTotal caps the stored bytes.
// TRLC-LINKS: REQ-SDS-286
func NewArchive(w io.Writer, maxTotal int64) *Archive {
	gz := gzip.NewWriter(w)
	return &Archive{gz: gz, tw: tar.NewWriter(gz), maxTotal: maxTotal}
}

// Add stores data as name; from and note go into the manifest.
// TRLC-LINKS: REQ-SDS-286
func (a *Archive) Add(name string, data []byte, from, note string) {
	a.add(name, data, from, note, false)
}

// add stores a member; always stores it past the cap (the manifest and the
// error list are always in the archive).
// TRLC-LINKS: REQ-SDS-286
func (a *Archive) add(name string, data []byte, from, note string, always bool) {
	sum := sha256.Sum256(data)
	a.mu.Lock()
	defer a.mu.Unlock()
	e := Entry{Name: name, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), From: from, Note: note}
	if !always && a.total+e.Size > a.maxTotal {
		e.Note = strings.TrimSpace(e.Note + " not stored: the archive is full")
		a.entries = append(a.entries, e)
		return
	}
	hdr := &tar.Header{Name: name, Mode: 0o644, Size: e.Size, ModTime: time.Unix(0, 0), Format: tar.FormatPAX}
	if err := a.tw.WriteHeader(hdr); err != nil {
		a.errs = append(a.errs, fmt.Sprintf("%s: %v", name, err))
		return
	}
	if _, err := a.tw.Write(data); err != nil {
		a.errs = append(a.errs, fmt.Sprintf("%s: %v", name, err))
		return
	}
	a.total += e.Size
	a.entries = append(a.entries, e)
}

// AddText stores s as name.
// TRLC-LINKS: REQ-SDS-286
func (a *Archive) AddText(name, s string) { a.Add(name, []byte(s), "", "") }

// AddJSON stores v, indented, as name.
// TRLC-LINKS: REQ-SDS-286
func (a *Archive) AddJSON(name string, v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		a.Errorf("%s: %v", name, err)
		return
	}
	a.Add(name, append(b, '\n'), "", "")
}

// AddFile stores the device file path as name, reading at most limit bytes
// (a longer file is cut there, and the manifest says so).
// TRLC-LINKS: REQ-SDS-286
func (a *Archive) AddFile(name, path string, limit int64) {
	f, err := os.Open(path) //nolint:gosec // G304: the collectors name the device files they read
	if err != nil {
		a.Errorf("%s: %v", path, err)
		return
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil && len(data) == 0 {
		a.Errorf("%s: %v", path, err)
		return
	}
	note := ""
	if int64(len(data)) > limit {
		data, note = data[:limit], fmt.Sprintf("cut at %d bytes", limit)
	}
	a.Add(name, data, path, note)
}

// Errorf notes a collector's failure; the collection goes on.
// TRLC-LINKS: REQ-SDS-286
func (a *Archive) Errorf(format string, args ...any) {
	a.mu.Lock()
	a.errs = append(a.errs, fmt.Sprintf(format, args...))
	a.mu.Unlock()
}

// Errors returns the failures noted so far.
// TRLC-LINKS: REQ-SDS-286
func (a *Archive) Errors() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.errs...)
}

// Close writes the manifest and the error list and finishes the archive.
// TRLC-LINKS: REQ-SDS-286
func (a *Archive) Close() error {
	a.mu.Lock()
	entries := append([]Entry(nil), a.entries...)
	errs := append([]string(nil), a.errs...)
	a.mu.Unlock()
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	manifest, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	a.add("manifest.json", append(manifest, '\n'), "", "", true)
	a.add("errors.txt", []byte(strings.Join(errs, "\n")+"\n"), "", "", true)
	if err := a.tw.Close(); err != nil {
		return err
	}
	return a.gz.Close()
}
