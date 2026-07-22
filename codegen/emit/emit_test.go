package emit_test

import (
	"fmt"
	"go/format"
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
// macro literal cannot be part-selected, so the halves are load-bearing).
func TestVerilogContract(t *testing.T) {
	out, err := emit.Verilog(ifacedef.Standard())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"`define IFACE_BUILD_ID_LO 16'h", "`define IFACE_BUILD_ID_HI 16'h",
		"`define REC_DEPTH 20480", "`define ADDR_W 15", "`define PRETRIG_MAX 20478",
		"`define SEL_BURST 8'h30",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("regs.vh missing %q", want)
		}
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
