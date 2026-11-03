// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// readArchive returns the members of a written archive by name.
// TRLC-LINKS: REQ-SDS-286
func readArchive(t *testing.T, b []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = data
	}
}

// The archive stores its members, lists every one in the manifest with its
// hash, notes what did not fit and the collectors' errors.
// TRLC-LINKS: REQ-SDS-286
func TestArchiveRoundTrip(t *testing.T) {
	var b bytes.Buffer
	a := NewArchive(&b, 64)
	a.AddText("one.txt", "hello")
	a.Add("big.bin", make([]byte, 100), "/dev/x", "")
	a.Errorf("a collector: %s", "failed")
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	m := readArchive(t, b.Bytes())
	if string(m["one.txt"]) != "hello" || m["big.bin"] != nil {
		t.Fatalf("members: one %q, big stored %v", m["one.txt"], m["big.bin"] != nil)
	}
	var entries []Entry
	if err := json.Unmarshal(m["manifest.json"], &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "big.bin" || !strings.Contains(entries[0].Note, "not stored") || entries[1].SHA256 == "" {
		t.Fatalf("manifest %+v", entries)
	}
	if !strings.Contains(string(m["errors.txt"]), "a collector: failed") {
		t.Fatalf("errors %q", m["errors.txt"])
	}
}

// The firmware inventory hashes every file, stores the small ones whole and
// the head of a large one, lists links, and skips the stick's own mount.
// TRLC-LINKS: REQ-SDS-284
func TestFirmwareInventory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "version.txt"), []byte("1.0"), 0o600); err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte{7}, wholeMax+10)
	if err := os.WriteFile(filepath.Join(root, "app.bin"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("version.txt", filepath.Join(root, "v")); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	a := NewArchive(&b, 64<<20)
	collectFirmware(a, []string{root})
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	m := readArchive(t, b.Bytes())
	var inv []FileInfo
	if err := json.Unmarshal(m["firmware/inventory.json"], &inv); err != nil {
		t.Fatal(err)
	}
	got := map[string]FileInfo{}
	for _, f := range inv {
		got[filepath.Base(f.Path)] = f
	}
	if got["version.txt"].Stored != "whole" || string(m["firmware/files"+filepath.Join(root, "version.txt")]) != "1.0" {
		t.Fatalf("small file: %+v", got["version.txt"])
	}
	if f := got["app.bin"]; f.Stored != "head" || len(m["firmware/files"+filepath.Join(root, "app.bin")]) != headMax || f.Size != int64(len(big)) || len(f.SHA256) != 64 {
		t.Fatalf("large file: %+v", f)
	}
	if got["v"].Link != "version.txt" {
		t.Fatalf("link: %+v", got["v"])
	}
}

// The framebuffer's geometry comes from sysfs (the visible mode inside a
// panned buffer), and its pixels convert at each depth.
// TRLC-LINKS: REQ-SDS-284
func TestFramebuffer(t *testing.T) {
	dir := t.TempDir()
	for name, v := range map[string]string{"virtual_size": "800,960\n", "bits_per_pixel": "16\n", "stride": "1600\n", "pan": "0,480\n", "mode": "\n", "modes": "U:800x480p-52\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	g, err := readFBGeom(dir)
	if err != nil || g.W != 800 || g.H != 480 || g.BPP != 16 || g.Stride != 1600 || g.PanY != 480 {
		t.Fatalf("geometry %+v %v", g, err)
	}
	raw := make([]byte, 2*2)
	raw[0], raw[1] = 0x00, 0xf8 // pure red in RGB565
	im, err := fbImage(raw, fbGeom{W: 2, H: 1, BPP: 16, Stride: 4})
	if err != nil {
		t.Fatal(err)
	}
	if r, g, bl, _ := im.At(0, 0).RGBA(); r>>8 != 0xf8 || g != 0 || bl != 0 {
		t.Fatalf("red pixel: %x %x %x", r, g, bl)
	}
	if _, err := fbImage(raw[:2], fbGeom{W: 2, H: 1, BPP: 16, Stride: 4}); err == nil {
		t.Fatal("a short read converted")
	}
}

// The sweep sets every V/div at both probe factors on both channels, each
// step reads back through its own query, and the restore commands are the
// state answers as they stand.
// TRLC-LINKS: REQ-SDS-285
func TestSweepPlan(t *testing.T) {
	steps := SweepSteps()
	n := map[string]int{}
	for _, s := range steps {
		n[s.Group]++
		if q := queryOf(s.Cmd); !strings.HasSuffix(q, "?") || strings.Contains(q, " ") {
			t.Fatalf("step %q reads back with %q", s.Cmd, q)
		}
	}
	if n["vdiv"] != 2*(2*(1+len(vdivs))+2) || n["tdiv"] != len(tdivs)+1 || n["coupling"] != 6 || n["bwl"] != 4 {
		t.Fatalf("steps by group %v", n)
	}
	st := scpiState{Answers: map[string]string{
		"*IDN?": "Siglent", "C1:VDIV?": "C1:VDIV 1.00E+00V", "TDIV?": "[timeout]", "BWL?": "BWL C1,OFF,C2,OFF",
		"C1:ATTN?": "C1:ATTN 10", "TRDL?": "Undefined header", "MSIZ?": "",
	}}
	got := strings.Join(restoreCmds(st), ";")
	if got != "C1:ATTN 10;C1:VDIV 1.00E+00V;BWL C1,OFF,C2,OFF" { // the probe factor before the V/div it scales
		t.Fatalf("restore %q", got)
	}
}

// Off the scope's 32-bit ARM the tracer refuses, saying why.
// TRLC-LINKS: REQ-SDS-285
func TestTraceOnlyOnARM(t *testing.T) {
	if runtime.GOARCH == "arm" {
		t.Skip("on ARM the tracer runs")
	}
	if err := Trace(1, TraceOpts{Out: filepath.Join(t.TempDir(), "x")}); err == nil || !strings.Contains(err.Error(), "ARM") {
		t.Fatalf("trace off ARM: %v", err)
	}
}
