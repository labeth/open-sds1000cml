// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
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

// A process's state comes from /proc: this test's own is running or
// sleeping, a pid no process has reads as gone.
// TRLC-LINKS: REQ-SDS-285
func TestProcState(t *testing.T) {
	if st := procState(os.Getpid()); st != "R" && st != "S" {
		t.Fatalf("own state %q", st)
	}
	if st := procState(1 << 30); st != "" {
		t.Fatalf("no such pid: %q", st)
	}
}

// scopeAsSeen is an SDS1102CML+ under its factory firmware, as the passive
// stage saw it.
// TRLC-LINKS: REQ-SDS-287
func scopeAsSeen() gateInput {
	return gateInput{
		cpuinfo: "Hardware\t: am335xevm\n",
		procs:   []Proc{{PID: 534, Comm: "SDS1000_arm.app", FDs: []string{"5 -> /dev/Gpmc", "12 -> /dev/fb0"}}},
		pid:     534, fpgaOK: true, fpgaVer: 0x52,
		scpi: &scpiState{Answers: map[string]string{
			"*IDN?": "Siglent,SDS1102CML+,SDS10BA2160661,6.01.01.21R2", "C1:VDIV?": "C1:VDIV 1.00E+00V",
			"C1:ATTN?": "C1:ATTN 10", "C1:CPL?": "C1:CPL D1M", "TDIV?": "TDIV 1.00E-08s", "TRMD?": "TRMD AUTO",
		}},
	}
}

// The gate lets the active stage run on a scope like the one it was made on,
// whatever its FPGA version, and names the first check that fails otherwise.
// TRLC-LINKS: REQ-SDS-287
func TestGate(t *testing.T) {
	in := scopeAsSeen()
	in.fpgaVer = 0x77 // another population's fabric: not a reason to stop
	if ok, why := passed(Gate(in)); !ok {
		t.Fatalf("the scope as seen: %s", why)
	}
	for name, mod := range map[string]func(*gateInput){
		"cpu":         func(g *gateInput) { g.cpuinfo = "Hardware\t: imx6\n" },
		"factory app": func(g *gateInput) { g.pid = 0 },
		"factory bus": func(g *gateInput) { g.procs[0].FDs = nil },
		"fpga":        func(g *gateInput) { g.fpgaOK = false },
		"identity":    func(g *gateInput) { g.scpi.Answers["*IDN?"] = "Rigol,DS1054Z,x,y" },
		"scpi form":   func(g *gateInput) { g.scpi.Answers["TDIV?"] = "1.00E-08" },
	} {
		g := scopeAsSeen()
		mod(&g)
		ok, why := passed(Gate(g))
		if ok || !strings.HasPrefix(why, name+":") {
			t.Errorf("%s: passed %v, %q", name, ok, why)
		}
	}
}

// The register map counts each register's reads and writes, the values
// written and the steps that wrote them, from the steps' trace files.
// TRLC-LINKS: REQ-SDS-285
func TestRegisterMap(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		var b bytes.Buffer
		gz := gzip.NewWriter(&b)
		_, _ = gz.Write([]byte(body))
		_ = gz.Close()
		if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	m := regMap{}
	for i, body := range []string{
		"# pid 534\n1 619 gpmc R 1 0x0039 0x0000 ret=0\n2 620 gpmc W 3 0x0012 0x00d9 ret=0\n",
		"3 620 gpmc W 3 0x0012 0x00ce ret=0\n4 619 ioctl /dev/fb0 req=0x4606 arg=- ret=0\n5 619 spi /dev/spidev1.0 tx=2dad7000\n",
	} {
		if err := m.addTrace(i, write(fmt.Sprintf("s%d.gz", i), body)); err != nil {
			t.Fatal(err)
		}
	}
	steps := []Step{{"vdiv", "C1:VDIV 2MV"}, {"vdiv", "C1:VDIV 5MV"}}
	txt := m.text(steps)
	if u := m["CS3 0x0012"]; u == nil || u.writes != 2 || len(u.values) != 2 || !u.steps[0] || !u.steps[1] {
		t.Fatalf("CS3 0x12: %+v", u)
	}
	if u := m["/dev/spidev1.0"]; u == nil || u.writes != 1 || !u.values["2dad7000"] || !u.steps[1] {
		t.Fatalf("the relay word: %+v", u)
	}
	if u := m["CS1 0x0039"]; u == nil || u.reads != 1 || u.writes != 0 {
		t.Fatalf("CS1 0x39: %+v", u)
	}
	if !strings.Contains(txt, "0x00ce,0x00d9  0(vdiv) 1(vdiv)") {
		t.Fatalf("table:\n%s", txt)
	}
}
