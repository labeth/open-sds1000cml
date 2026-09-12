package emit

import (
	"fmt"
	"strings"

	"open-sds/codegen/schema"
)

func vFields(p func(string, ...any), prefix string, fs []schema.Field) {
	for _, f := range fs {
		p("`define %s_%s_MASK 16'h%04x\n", prefix, f.Name, f.Mask())
		p("`define %s_%s_LSB %d\n", prefix, f.Name, f.Lo)
		for _, e := range f.Enum {
			p("`define %s_%s_%s %d'd%d  // %s\n", prefix, f.Name, e.Name, f.Width(), e.Value, e.Desc)
		}
	}
}

// Regs renders regs.vh: Verilog-2001 `define macros for every selector, field
// mask/LSB, enum value, identity value, opcode, geometry constant and DIAG index.
func Regs(i schema.Interface) (string, error) {
	if err := check(i); err != nil {
		return "", err
	}
	id := i.BuildID()
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	p("// %s\n", Banner)
	p("// iface %s v%d  build-ID 0x%08x\n", i.Name, i.Version, id)
	p("// %s\n", BuildIDNote)
	p("//\n// Verilog-2001 `define macros (a localparam at global scope is SystemVerilog-only).\n")
	p("// `include this header at global scope, then use `SEL_<REG>, `<REG>_<FIELD>_MASK,\n")
	p("// `<REG>_<FIELD>_LSB, `<REG>_<FIELD>_<ENUM>, `OP_<NAME>, `DIAG_<NAME>, `REC_DEPTH, `ROW_COLS, `IFACE_BUILD_ID ...\n")
	p("`ifndef IFACE_REGS_VH\n`define IFACE_REGS_VH\n\n")
	p("// ---- identity ------------------------------------------------------------\n")
	p("`define IFACE_NAME \"%s\"\n", i.Name)
	p("`define IFACE_VERSION 16'd%d\n", i.Version)
	p("`define IFACE_BUILD_ID 32'h%08x\n", id)
	p("`define IFACE_BUILD_ID_LO 16'h%04x\n", lo16(id))
	p("`define IFACE_BUILD_ID_HI 16'h%04x\n", hi16(id))
	p("`define VERSION_MAGIC 16'h%04x\n", i.VersionMagic)
	p("`define FABRIC_ID 16'h%04x\n", i.FabricID)
	p("`define SEL_MASK 8'h%02x  // selector bits the fabric decodes (%d selectors): %s\n", i.SelMask, i.SelCount(), selMaskDoc(i.SelMask))
	if i.Legacy.Version != 0 {
		p("`define SEL_MASK_V%d 8'h%02x  // the v%d generation's mask: its registers keep those selectors\n", i.Legacy.Version, i.Legacy.SelMask, i.Legacy.Version)
	}
	if len(i.Undecoded) > 0 {
		p("// undecoded selectors (read 0, writes ignored; no register may take them): %s\n", selList(i.Undecoded))
	}
	g := i.Geometry
	p("\n// ---- record geometry (the writers/readers derive from these, no module parameter to drift) ----\n")
	p("`define REC_DEPTH %d\n", g.RecDepth)
	p("`define ADDR_W %d\n", g.AddrW)
	p("`define PRETRIG_MAX %d\n", g.PretrigMax())
	p("`define ROW_COLS %d  // words per row; the reader emits row-major, column-minor\n", g.RowCols)
	p("`define ROWS %d  // rows of the record (REC_DEPTH / ROW_COLS)\n", g.Rows)
	p("`define DLINE_ROWS %d  // stack delay line / snapshot rows\n", g.DlineRows)
	p("`define ACC_BITS %d  // stack accumulator cell width\n", g.AccBits)
	p("`define STACK_N_MAX %d  // records per stack batch without overflow\n", g.StackNMax)
	p("\n// ---- %s strobe payloads (the RTL compares the whole write word; other values are ignored) ----\n", i.OpcodeReg)
	for _, o := range i.Opcodes {
		p("`define OP_%s 16'h%04x  // %s\n", o.Name, o.Value, o.Desc)
	}
	p("\n// ---- selectors and fields ----------------------------------------------------\n")
	for _, r := range i.Regs {
		p("// SEL_%s: %s — %s\n", r.Name, accessDoc(r), r.Desc)
		p("`define SEL_%s 8'h%02x\n", r.Name, r.Sel)
		vFields(p, r.Name, r.Fields)
	}
	p("\n// ---- DIAG window: %s index -> %s layout ----------------------------------\n", i.DiagIdxReg, i.DiagDataReg)
	for _, d := range i.Diag {
		if d.Count == 1 {
			p("// DIAG_%s: %s — %s\n", d.Name, diagAccessDoc(d), d.Desc)
			p("`define DIAG_%s 8'h%02x\n", d.Name, d.Idx)
		} else {
			p("// DIAG_%s[0..%d]: %s — %s\n", d.Name, d.Count-1, diagAccessDoc(d), d.Desc)
			p("`define DIAG_%s_BASE 8'h%02x\n", d.Name, d.Idx)
			p("`define DIAG_%s_COUNT %d\n", d.Name, d.Count)
			p("`define DIAG_%s_LAST 8'h%02x\n", d.Name, d.Last())
		}
		vFields(p, "DIAG_"+d.Name, d.Fields)
	}
	p("`define DIAG_RESERVED_BASE 8'h%02x  // indices from here up read 0 and ignore writes\n", i.DiagReserved)
	p("\n`endif\n")
	return b.String(), nil
}
