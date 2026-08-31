package emit_test

import (
	"fmt"
	"go/format"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"open-sds/codegen/emit"
	"open-sds/codegen/ifacedef"
	"open-sds/codegen/schema"
)

type gen struct {
	name string
	fn   func(schema.Interface) (string, error)
}

var gens = []gen{
	{"regs.vh", emit.Verilog},
	{"regmux.vh", emit.Regmux},
	{"iface.go", emit.GoBindings},
	{"REGISTER-MAP.md", emit.Doc},
}

// Every emitter must render deterministically — regeneration is a stable drift
// check, so the same schema must produce byte-identical output every time.
func TestEmittersDeterministic(t *testing.T) {
	std := ifacedef.Standard()
	for _, g := range gens {
		a, err := g.fn(std)
		if err != nil {
			t.Fatalf("%s: %v", g.name, err)
		}
		b, err := g.fn(std)
		if err != nil {
			t.Fatalf("%s: %v", g.name, err)
		}
		if a != b {
			t.Errorf("%s: emitter output not deterministic", g.name)
		}
	}
}

// The Go bindings must be gofmt-stable: running gofmt again is a no-op, so the
// checked-in file is byte-identical across regenerations.
func TestGoBindingsGofmtStable(t *testing.T) {
	out, err := emit.GoBindings(ifacedef.Standard())
	if err != nil {
		t.Fatal(err)
	}
	reformatted, err := format.Source([]byte(out))
	if err != nil {
		t.Fatalf("generated bindings do not gofmt: %v", err)
	}
	if string(reformatted) != out {
		t.Error("generated bindings are not gofmt-stable")
	}
}

// The regs.vh header must carry the build-ID halves and the capture geometry (a
// macro literal cannot be part-selected, so the halves are load-bearing), plus
// the selectors that hardware has actually answered on.
//
// The SEL_BURST literal here was `8'h30` and had been red since the CS1 map was
// respaced to multiples of 4 (A3-A7-only decode). 0x30 is now PRETRIG_LO, so the
// stale assertion was not merely failing — it was asserting the wrong register.
// The frozen values below are the ones a flashed fabric returned on the real
// GPMC bus, so they are ground truth, not preference; moving one means re-proving
// it on hardware and re-flashing, never editing this list to match a schema edit.
func TestVerilogContract(t *testing.T) {
	out, err := emit.Verilog(ifacedef.Standard())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"`define IFACE_BUILD_ID_LO 16'h", "`define IFACE_BUILD_ID_HI 16'h",
		"`define REC_DEPTH 20480", "`define ADDR_W 15", "`define PRETRIG_MAX 20478",
		// HW-verified reads: 0x10=eb5f, 0x14=c2f6, 0x18=0052; HW-verified writes:
		// 0x24=RUN, 0x28=DECIM_LO, 0x30=PRETRIG_LO. BURST sits at 0x40.
		"`define SEL_BUILDID_LO 8'h10", "`define SEL_BUILDID_HI 8'h14",
		"`define SEL_VERSION 8'h18", "`define SEL_RUN 8'h24",
		"`define SEL_DECIM_LO 8'h28", "`define SEL_PRETRIG_LO 8'h30",
		"`define SEL_BURST 8'h40",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("regs.vh missing %q", want)
		}
	}
}

// Every emitted CS1 SEL_ macro must be decodable by the fabric. acq.v builds its
// selector as {1'b0, sel[6:2], 2'b00} because A1 carries a clock, A2 floats high
// and A8 is unwired, so a CS1 selector with bit 0, 1 or 7 set answers with a
// neighbour's data instead of its own. Reading the constraint back out of the
// GENERATED text (not just the schema) closes the loop: it also catches an
// emitter that renders a selector wrongly.
func TestVerilogCS1SelectorsDecodable(t *testing.T) {
	out, err := emit.Verilog(ifacedef.Standard())
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]uint16{}
	for _, r := range ifacedef.Standard().AllRegs() {
		if r.Plane == schema.CS1 {
			byName[r.Name] = r.Sel
		}
	}
	re := regexp.MustCompile("`define SEL_([A-Z0-9_]+) 8'h([0-9a-f]{2})")
	seen := 0
	for _, m := range re.FindAllStringSubmatch(out, -1) {
		sel, isCS1 := byName[m[1]]
		if !isCS1 {
			continue // CS3 is decoded by the MAX V, not by us
		}
		seen++
		v, err := strconv.ParseUint(m[2], 16, 16)
		if err != nil {
			t.Fatalf("SEL_%s: unparsable literal %q", m[1], m[2])
		}
		if uint16(v) != sel {
			t.Errorf("SEL_%s emitted 0x%02x, schema says 0x%02x", m[1], v, sel)
		}
		if v&0x83 != 0 {
			t.Errorf("SEL_%s = 0x%02x is undecodable on CS1 (bits 0/1/7 are forced to 0 by the fabric)", m[1], v)
		}
	}
	if seen != len(byName) {
		t.Errorf("regs.vh emitted %d CS1 SEL_ macros, schema has %d registers", seen, len(byName))
	}
}

// The regmux include must generate write strobes and drive the identity/build-ID
// reads from the schema (so the selector decode can never drift).
func TestRegmuxContract(t *testing.T) {
	out, err := emit.Regmux(ifacedef.Standard())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"wire we_OPCODE =", "rmux_rdata = `IFACE_BUILD_ID_LO;", "rmux_rdata = rdata_STATUS_A;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("regmux.vh missing %q", want)
		}
	}
	// A read-only register must NOT get a write strobe.
	if strings.Contains(out, "we_CONF_DONE") || strings.Contains(out, "we_BURST ") {
		t.Error("regmux.vh generated a write strobe for a read-only register")
	}
}

// The 0x00 -> BURST alias is BUS BEHAVIOUR, not decoration: the GPMC prefetch /
// sDMA engine reads the chip-select BASE address and cannot be pointed at 0x40,
// so without this case that whole drain path reads a dead selector. It used to be
// a hand edit inside the generated regmux.vh, which meant `make generate` deleted
// it silently. It is now declared in the schema; this test is the guard that it
// keeps being emitted, in the same plane, onto the same read source as BURST, and
// WITHOUT a write strobe (an alias is a read doorway only).
func TestRegmuxAliasEmitted(t *testing.T) {
	out, err := emit.Regmux(ifacedef.Standard())
	if err != nil {
		t.Fatal(err)
	}
	const want = "{ `PLANE_CS1, 8'h00 }: rmux_rdata = rdata_BURST;"
	if !strings.Contains(out, want) {
		t.Errorf("regmux.vh lost the CS1 0x00 -> BURST alias: missing %q", want)
	}
	if strings.Contains(out, "wr_sel == 8'h00") {
		t.Error("regmux.vh generated a write strobe for a read alias")
	}
	// The alias must resolve to the SAME read source as the register it aliases,
	// so a future change to BURST's read expression cannot leave the alias behind.
	var burstExpr string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "`SEL_BURST }") {
			burstExpr = strings.TrimSpace(strings.SplitN(line, "=", 2)[1])
			break
		}
	}
	if burstExpr == "" {
		t.Fatal("regmux.vh has no `SEL_BURST read case")
	}
	if !strings.Contains(out, "8'h00 }: rmux_rdata = "+strings.TrimSuffix(burstExpr, "; // behavior")) {
		t.Errorf("alias read source diverged from SEL_BURST (%q)", burstExpr)
	}
}

// The Go bindings must expose the behavioral surface (§3.4).
func TestBindingsSurface(t *testing.T) {
	out, err := emit.GoBindings(ifacedef.Standard())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"const BuildID uint32 =", "func Verify(", "func Writable(",
		"type EnvelopeRecord struct", "func StatusADone(w uint16) bool",
		"var AutoIncPorts = []Reg{", "var ChannelPorts = []ChannelPort{",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("iface.go missing %q", want)
		}
	}
}

// The one build-ID must appear identically in the Verilog, the Go bindings, and
// the doc — the whole point of the handshake is one fingerprint on all sides.
func TestBuildIDConsistentAcrossOutputs(t *testing.T) {
	std := ifacedef.Standard()
	hex := fmt.Sprintf("%08x", std.BuildID())
	for _, g := range gens {
		out, err := g.fn(std)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, hex) {
			t.Errorf("%s does not carry build-ID %s", g.name, hex)
		}
	}
}
