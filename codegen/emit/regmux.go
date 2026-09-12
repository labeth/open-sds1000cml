package emit

import (
	"fmt"
	"strings"

	"open-sds/codegen/schema"
)

// Regmux renders regmux.vh: the selector decode, to be `included INSIDE the top
// module after regs.vh. Hand RTL only drives behaviour behind the named wires;
// it never writes a selector compare.
func Regmux(i schema.Interface) (string, error) {
	if err := check(i); err != nil {
		return "", err
	}
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	p("// %s\n", Banner)
	p("// iface %s v%d  build-ID 0x%08x\n", i.Name, i.Version, i.BuildID())
	p(`//
// Register-decode include. ` + "`include" + ` it INSIDE the top module, AFTER regs.vh.
// The top module provides:
//     we_commit          1-cycle "write accepted" pulse (nWE rising, data + sel settled)
//     wr_sel   [7:0]     selector of the accepted write, raw: bit b = GPMC A(b+1) as it
//                        reaches the fabric; bits outside SEL_MASK are masked here
//     wr_data  [15:0]    the written word (used by the strobe decodes below)
//     rd_pop   [1]       1-cycle "read completed" pulse (nOE rising) for pop-on-read ports
//     rd_sel   [7:0]     selector of the current read (raw; masked here)
//     rdata_<REG> [15:0] the read value the behaviour drives, one per readable register
//                        whose source is hand RTL (identity / build-ID / constant registers
//                        are driven from the schema here and need no rdata_ wire; stub
//                        registers read 0 here and need none either)
// This include produces:
//     rmux_wr_sel / rmux_rd_sel [7:0]  the masked selectors (sel & SEL_MASK)
//     we_<REG>            1-cycle write strobe, one per writable non-stub register
//     op_<NAME>           1-cycle strobe per opcode of the opcode register
//     pop_<REG>           1-cycle pop strobe, one per pop-on-read register (its aliases included)
//     rmux_rdata [15:0]   the read value for rmux_rd_sel (0 for undecoded and stub selectors)
`)
	p("\n// ---- selector masking: SEL_MASK 0x%02x %s ------------------\n", i.SelMask, selMaskDoc(i.SelMask))
	if i.SelMask&0x03 != 0 {
		p("// bits 1:0 are GPMC A2/A1 (balls A2/B1): the slave presents them in sel[1:0].\n")
	}
	p("wire [7:0] rmux_wr_sel = wr_sel & `SEL_MASK;\n")
	p("wire [7:0] rmux_rd_sel = rd_sel & `SEL_MASK;\n")
	if len(i.Undecoded) > 0 {
		p("// undecoded by contract (read 0, writes ignored): %s\n", selList(i.Undecoded))
	}

	p("\n// ---- write strobes (stub registers — not implemented in this increment — get none) ----\n")
	for _, r := range i.Regs {
		if !r.Access.CanWrite() {
			continue
		}
		if r.Stub {
			p("// %s: stub, writes ignored\n", r.Name)
			continue
		}
		p("wire we_%s = we_commit & (rmux_wr_sel == `SEL_%s);\n", r.Name, r.Name)
	}
	p("\n// ---- %s opcode strobes (full-word compare; any other value is ignored) ----\n", i.OpcodeReg)
	for _, o := range i.Opcodes {
		p("wire op_%s = we_%s & (wr_data == `OP_%s);\n", o.Name, i.OpcodeReg, o.Name)
	}

	p("\n// ---- pop-on-read strobes ---------------------------------------------------------\n")
	for _, r := range i.Regs {
		if !r.Pop || r.Source == schema.SrcAlias {
			continue
		}
		terms := []string{fmt.Sprintf("(rmux_rd_sel == `SEL_%s)", r.Name)}
		for _, a := range i.Aliases(r.Name) {
			terms = append(terms, fmt.Sprintf("(rmux_rd_sel == `SEL_%s)", a.Name))
		}
		p("wire pop_%s = rd_pop & (%s);\n", r.Name, strings.Join(terms, " | "))
	}

	p("\n// ---- read-data mux ----------------------------------------------------------------\n")
	p("reg [15:0] rmux_rdata;\nalways @* begin\n    case (rmux_rd_sel)\n")
	for _, r := range i.Regs {
		if !r.Access.CanRead() {
			continue
		}
		var src, note string
		switch {
		case r.Stub:
			src, note = "16'h0000", " // stub: reads 0 in this increment"
		case r.Source == schema.SrcWire:
			src, note = "rdata_"+r.Name, ""
		case r.Source == schema.SrcConst:
			src, note = fmt.Sprintf("16'h%04x", r.Const), " // constant"
		case r.Source == schema.SrcBuildIDLo:
			src, note = "`IFACE_BUILD_ID_LO", ""
		case r.Source == schema.SrcBuildIDHi:
			src, note = "`IFACE_BUILD_ID_HI", ""
		case r.Source == schema.SrcAlias:
			src, note = "rdata_"+r.AliasOf, " // alias of "+r.AliasOf
		}
		p("        `SEL_%s: rmux_rdata = %s;%s\n", r.Name, src, note)
	}
	p("        default: rmux_rdata = 16'h0000;\n    endcase\nend\n")
	return b.String(), nil
}
