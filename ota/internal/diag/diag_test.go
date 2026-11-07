// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	core "github.com/labeth/open-dso-core/supervisor/diag"
)

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

// scopeAsSeen is an SDS1102CML+ under its factory firmware, as the passive
// stage saw it.
// TRLC-LINKS: REQ-SDS-287
func scopeAsSeen() gateInput {
	return gateInput{
		cpuinfo: "Hardware\t: am335xevm\n",
		procs:   []core.Proc{{PID: 534, Comm: "SDS1000_arm.app", FDs: []string{"5 -> /dev/Gpmc", "12 -> /dev/fb0"}}},
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
	if ok, why := core.Passed(Gate(in)); !ok {
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
		ok, why := core.Passed(Gate(g))
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
