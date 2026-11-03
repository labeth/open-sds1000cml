// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FileInfo is one file of the factory firmware as the inventory lists it.
type FileInfo struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Mode   string `json:"mode"`
	MTime  int64  `json:"mtime"`
	SHA256 string `json:"sha256,omitempty"`
	Stored string `json:"stored,omitempty"` // whole, head, or empty (listed only)
	Link   string `json:"link,omitempty"`
}

// firmwareRoots are walked for the inventory; the stick's own mount and the
// vendor's log and media trees are skipped (the stick holds this archive).
var firmwareRoots = []string{"/usr/bin/siglent", "/etc", "/root", "/home", "/opt", "/usr/local"}

// firmwareSkip are the prefixes the walk leaves out.
var firmwareSkip = []string{"/usr/bin/siglent/usr/media", "/usr/bin/siglent/usr/log"}

// The inventory stores a file whole up to wholeMax bytes (config, version,
// calibration, kernel modules, the FPGA bitstream), and the first headMax
// bytes of a larger one (the vendor app, upgrade images).
const (
	wholeMax = 1 << 20
	headMax  = 4096
)

// collectFirmware inventories the factory firmware: every file's size, mode
// and SHA-256, the small ones stored whole and the large ones by their head.
// TRLC-LINKS: REQ-SDS-284
func collectFirmware(a *Archive, roots []string) {
	var inv []FileInfo
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // an unreadable branch is left out
			}
			for _, s := range firmwareSkip {
				if p == s || strings.HasPrefix(p, s+"/") {
					return filepath.SkipDir
				}
			}
			info, err := d.Info()
			if err != nil || d.IsDir() {
				return nil //nolint:nilerr // gone since the walk listed it, or a directory
			}
			fi := FileInfo{Path: p, Size: info.Size(), Mode: info.Mode().String(), MTime: info.ModTime().Unix()}
			if d.Type()&fs.ModeSymlink != 0 {
				fi.Link, _ = os.Readlink(p)
			} else if d.Type().IsRegular() {
				fi.SHA256, fi.Stored = storeFile(a, p, info.Size())
			}
			inv = append(inv, fi)
			return nil
		})
	}
	a.AddJSON("firmware/inventory.json", inv)
}

// storeFile hashes path and stores it whole or by its head; it returns the
// hash and how it was stored.
// TRLC-LINKS: REQ-SDS-284
func storeFile(a *Archive, path string, size int64) (sum, stored string) {
	f, err := os.Open(path) //nolint:gosec // G304: a file of the inventory walk
	if err != nil {
		a.Errorf("%s: %v", path, err)
		return "", ""
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	keep := int64(headMax)
	stored = "head"
	if size <= wholeMax {
		keep, stored = size, "whole"
	}
	var head strings.Builder
	if _, err := io.Copy(h, io.TeeReader(f, &limitWriter{w: &head, n: keep})); err != nil {
		a.Errorf("%s: %v", path, err)
		return "", ""
	}
	sum = hex.EncodeToString(h.Sum(nil))
	note := ""
	if stored == "head" {
		note = fmt.Sprintf("the first %d of %d bytes", head.Len(), size)
	}
	a.Add("firmware/files"+path, []byte(head.String()), path, note)
	return sum, stored
}

// limitWriter keeps the first n bytes written to it and drops the rest.
type limitWriter struct {
	w *strings.Builder
	n int64
}

// Write keeps what still fits.
// TRLC-LINKS: REQ-SDS-284
func (l *limitWriter) Write(p []byte) (int, error) {
	if room := l.n - int64(l.w.Len()); room > 0 {
		l.w.Write(p[:min(int64(len(p)), room)])
	}
	return len(p), nil
}
