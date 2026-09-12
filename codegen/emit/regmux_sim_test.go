package emit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"open-sds/codegen/ifacedef"
	"open-sds/codegen/schema"
)

// Testbench renders a self-checking Verilog-2001 testbench around regs.vh +
// regmux.vh: for every selector (with the bits outside SEL_MASK set to 1 to
// prove the masking) it checks exactly the right we_/pop_/op_ strobe fires and
// the read mux returns the right source; stub registers and undecoded
// selectors must read 0 and fire nothing. It prints "REGMUX PASS" and exits
// through $finish only when every check passed; a mismatch prints FAIL lines
// and ends with $fatal (non-zero vvp exit; $finish instead under `define
// NO_FATAL so the bench also compiles as plain Verilog-2001).
func Testbench(i schema.Interface) string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	junk := ^i.SelMask // the bits the fabric must ignore
	p("`timescale 1ns/1ps\n`include \"regs.vh\"\nmodule regmux_tb;\n")
	p("reg we_commit = 0, rd_pop = 0; reg [7:0] wr_sel = 0, rd_sel = 0; reg [15:0] wr_data = 0;\n")
	var writable, pops []schema.Register
	for _, r := range i.Regs {
		if r.Access.CanRead() && r.Source == schema.SrcWire && !r.Stub {
			p("wire [15:0] rdata_%s = 16'h%04x;\n", r.Name, 0x1000|uint16(r.Sel))
		}
		if r.Access.CanWrite() && !r.Stub {
			writable = append(writable, r)
		}
		if r.Pop && r.Source != schema.SrcAlias {
			pops = append(pops, r)
		}
	}
	p("`include \"regmux.vh\"\ninteger fails = 0, checks = 0;\n")
	p("task chk(input cond, input [8*40:1] what); begin checks = checks + 1; if (!cond) begin fails = fails + 1; $display(\"FAIL %%0s sel=%%02x\", what, wr_sel); end end endtask\n")
	// the vector of every write strobe, to prove one-hot-ness
	p("wire [%d:0] we_all = {", len(writable)-1)
	for k, r := range writable {
		if k > 0 {
			p(", ")
		}
		p("we_%s", r.Name)
	}
	p("};\nwire [%d:0] pop_all = {", len(pops)-1)
	for k, r := range pops {
		if k > 0 {
			p(", ")
		}
		p("pop_%s", r.Name)
	}
	p("};\ninitial begin\n")
	// writes
	for _, r := range i.Regs {
		p("  wr_sel = `SEL_%s | 8'h%02x; we_commit = 1; wr_data = 16'hffff; #1;\n", r.Name, junk)
		if r.Access.CanWrite() && !r.Stub {
			p("  chk(we_%s === 1'b1, \"we_%s\"); chk(we_all == (we_all & -we_all), \"we onehot %s\");\n", r.Name, r.Name, r.Name)
			p("  chk(rmux_wr_sel == `SEL_%s, \"wr mask %s\");\n", r.Name, r.Name)
		} else {
			p("  chk(we_all == 0, \"no we for %s\");\n", r.Name)
		}
		p("  we_commit = 0; #1; chk(we_all == 0, \"we idle %s\");\n", r.Name)
	}
	for _, u := range i.Undecoded {
		p("  wr_sel = 8'h%02x; we_commit = 1; wr_data = 16'h0001; #1; chk(we_all == 0, \"undecoded write %02x\");\n", u, u)
		for _, o := range i.Opcodes {
			p("  chk(op_%s === 1'b0, \"undecoded op_%s %02x\");\n", o.Name, o.Name, u)
		}
		p("  we_commit = 0; #1;\n")
	}
	// opcodes: each fires only for its value, and only on the opcode register
	for _, o := range i.Opcodes {
		p("  wr_sel = `SEL_%s; wr_data = `OP_%s; we_commit = 1; #1; chk(op_%s === 1'b1, \"op_%s\");\n", i.OpcodeReg, o.Name, o.Name, o.Name)
		for _, o2 := range i.Opcodes {
			if o2.Name != o.Name {
				p("  chk(op_%s === 1'b0, \"op_%s not %s\");\n", o2.Name, o2.Name, o.Name)
			}
		}
		p("  wr_sel = `SEL_%s | 8'h04; #1; chk(op_%s === 1'b0, \"op_%s other sel\"); we_commit = 0; #1;\n", i.OpcodeReg, o.Name, o.Name)
	}
	p("  wr_sel = `SEL_%s; wr_data = 16'h00c3; we_commit = 1; #1;\n", i.OpcodeReg)
	for _, o := range i.Opcodes {
		p("  chk(op_%s === 1'b0, \"op_%s vendor word\");\n", o.Name, o.Name)
	}
	p("  we_commit = 0; #1;\n")
	// reads
	for _, r := range i.Regs {
		p("  rd_sel = `SEL_%s | 8'h%02x; rd_pop = 1; #1;\n", r.Name, junk)
		var want string
		switch {
		case !r.Access.CanRead(), r.Stub:
			want = "16'h0000"
		case r.Source == schema.SrcWire:
			want = "rdata_" + r.Name
		case r.Source == schema.SrcAlias:
			want = "rdata_" + r.AliasOf
		case r.Source == schema.SrcConst:
			want = fmt.Sprintf("16'h%04x", r.Const)
		case r.Source == schema.SrcBuildIDLo:
			want = "`IFACE_BUILD_ID_LO"
		case r.Source == schema.SrcBuildIDHi:
			want = "`IFACE_BUILD_ID_HI"
		}
		p("  chk(rmux_rdata === %s, \"rdata %s\");\n", want, r.Name)
		target := r.Name
		if r.Source == schema.SrcAlias {
			target = r.AliasOf
		}
		if r.Pop {
			p("  chk(pop_%s === 1'b1, \"pop_%s via %s\"); chk(pop_all == (pop_all & -pop_all), \"pop onehot %s\");\n", target, target, r.Name, r.Name)
		} else {
			p("  chk(pop_all == 0, \"no pop for %s\");\n", r.Name)
		}
		p("  rd_pop = 0; #1; chk(pop_all == 0, \"pop idle %s\");\n", r.Name)
	}
	for _, u := range i.Undecoded {
		p("  rd_sel = 8'h%02x; rd_pop = 1; #1; chk(rmux_rdata === 16'h0000, \"undecoded read %02x\"); chk(pop_all == 0, \"undecoded pop %02x\"); rd_pop = 0; #1;\n", u, u, u)
	}
	// every selector the mask can produce but no register claims reads 0
	claimed := map[uint8]bool{}
	for _, r := range i.Regs {
		claimed[r.Sel] = true
	}
	for s := 0; s < 256; s++ {
		if uint8(s)&^i.SelMask != 0 || claimed[uint8(s)] {
			continue
		}
		p("  rd_sel = 8'h%02x; #1; chk(rmux_rdata === 16'h0000, \"unclaimed read %02x\");\n", s, s)
	}
	p("  rd_sel = 8'h%02x; #1; chk(rmux_rdata === rdata_BURST, \"rd mask -> 0x00 alias\");\n", junk)
	p("  if (fails == 0) begin $display(\"REGMUX PASS %%0d checks\", checks); $finish; end\n")
	p("  else begin $display(\"REGMUX FAIL %%0d of %%0d checks\", fails, checks);\n")
	p("`ifdef NO_FATAL\n    $finish;\n`else\n    $fatal(1);\n`endif\n  end\nend\nendmodule\n")
	return b.String()
}

// The generated decode include must compile as Verilog-2001 and behave under
// simulation (iverilog). Skipped when iverilog is absent.
func TestRegmuxSimulates(t *testing.T) {
	iv, err := exec.LookPath("iverilog")
	if err != nil {
		t.Skip("iverilog not on PATH")
	}
	i := ifacedef.Default()
	regs, err := Regs(i)
	if err != nil {
		t.Fatal(err)
	}
	regmux, err := Regmux(i)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "regs.vh"), []byte(regs), 0o644)
	os.WriteFile(filepath.Join(dir, "regmux.vh"), []byte(regmux), 0o644)
	os.WriteFile(filepath.Join(dir, "regmux_tb.v"), []byte(Testbench(i)), 0o644)

	// The includes themselves must be plain Verilog-2001 (Quartus reads them
	// that way); the testbench uses $fatal, which needs -g2012 for the run.
	comp := exec.Command(iv, "-g2001", "-Wall", "-t", "null", "-I", dir, "-DNO_FATAL", filepath.Join(dir, "regmux_tb.v"))
	if out, err := comp.CombinedOutput(); err != nil || strings.Contains(string(out), "warning") {
		t.Fatalf("Verilog-2001 compile not clean: %v\n%s", err, out)
	}
	run := exec.Command(iv, "-g2012", "-Wall", "-o", filepath.Join(dir, "tb.vvp"), "-I", dir, filepath.Join(dir, "regmux_tb.v"))
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("iverilog: %v\n%s", err, out)
	} else if strings.Contains(string(out), "warning") {
		t.Fatalf("iverilog warnings:\n%s", out)
	}
	sim := exec.Command("vvp", "-n", filepath.Join(dir, "tb.vvp"))
	out, err := sim.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "REGMUX PASS") || strings.Contains(string(out), "FAIL") {
		t.Fatalf("simulation failed: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// A broken include must make the testbench fail (proves the bench is not vacuous).
func TestRegmuxTestbenchCatchesDrift(t *testing.T) {
	iv, err := exec.LookPath("iverilog")
	if err != nil {
		t.Skip("iverilog not on PATH")
	}
	i := ifacedef.Default()
	regs, _ := Regs(i)
	regmux, _ := Regmux(i)
	broken := strings.Replace(regmux, "`SEL_STATUS_A: rmux_rdata = rdata_STATUS_A;", "`SEL_STATUS_A: rmux_rdata = rdata_FILL;", 1)
	if broken == regmux {
		t.Fatal("mutation did not apply")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "regs.vh"), []byte(regs), 0o644)
	os.WriteFile(filepath.Join(dir, "regmux.vh"), []byte(broken), 0o644)
	os.WriteFile(filepath.Join(dir, "regmux_tb.v"), []byte(Testbench(i)), 0o644)
	if out, err := exec.Command(iv, "-g2012", "-o", filepath.Join(dir, "tb.vvp"), "-I", dir, filepath.Join(dir, "regmux_tb.v")).CombinedOutput(); err != nil {
		t.Fatalf("iverilog: %v\n%s", err, out)
	}
	out, err := exec.Command("vvp", "-n", filepath.Join(dir, "tb.vvp")).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "FAIL rdata STATUS_A") {
		t.Fatalf("broken mux not caught (err=%v):\n%s", err, out)
	}
}
