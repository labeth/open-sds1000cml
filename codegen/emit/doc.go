// ENGMODEL-OWNER-UNIT: FU-CODEGEN-EMIT
package emit

import (
	"fmt"
	"strings"

	"open-sds/codegen/schema"
)

// TRLC-LINKS: REQ-SDS-157
func md(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

// TRLC-LINKS: REQ-SDS-157
func enumDoc(f schema.Field) string {
	if len(f.Enum) == 0 {
		return ""
	}
	var parts []string
	for _, e := range f.Enum {
		parts = append(parts, fmt.Sprintf("%d=`%s`", e.Value, e.Name))
	}
	return " Values: " + strings.Join(parts, ", ") + "."
}

// Doc renders REGISTER-MAP.md, the human reference.
// TRLC-LINKS: REQ-SDS-157
func Doc(i schema.Interface) (string, error) {
	if err := check(i); err != nil {
		return "", err
	}
	id := i.BuildID()
	g := i.Geometry
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	p("<!-- %s -->\n", Banner)
	p("# Register map — %s v%d (build-ID `0x%08x`)\n\n", i.Name, i.Version, id)
	p("Source: `codegen/ifacedef/default.go`, transcribed from `the acq2 analysis branch` §2/§2.1 (the v%d registers) and `the acq2 analysis branch` §2 (the v%d additions). ", i.Legacy.Version, i.Version)
	p("The same schema generates `fpga/default/regs.vh`, `fpga/default/regmux.vh` and `app/internal/iface/iface.go`; `make drift` in `codegen/` fails when any of them is stale.\n\n")
	p("**stub** = declared for the full v%d map but not implemented by the current fabric increment: reads 0, writes ignored (no `we_` strobe is generated); the Desc says from which increment it is live.\n\n", i.Version)
	p("## Identity\n\n| item | value |\n|---|---|\n")
	p("| build-ID (`BUILDID_HI:BUILDID_LO`) | `0x%08x` — `%s` = `0x%04x`, `%s` = `0x%04x` |\n", id, i.BuildIDLoReg, lo16(id), i.BuildIDHiReg, hi16(id))
	p("| `%s` | `0x%04x` |\n| `%s` | `0x%04x` |\n", i.VersionReg, i.VersionMagic, i.FabricIDReg, i.FabricID)
	p("| selector mask | `0x%02x` (%d selectors, %s) — %s |\n", i.SelMask, i.SelCount(), selMaskDoc(i.SelMask), md(i.SelDesc))
	if i.Legacy.Version != 0 {
		p("| legacy generation | v%d decoded `0x%02x`; its registers keep those selectors, newer registers take slots outside it |\n", i.Legacy.Version, i.Legacy.SelMask)
	}
	if len(i.Undecoded) > 0 {
		p("| undecoded selectors | %s — read 0, writes ignored, no register may take them |\n", selList(i.Undecoded))
	}
	p("| record geometry | `REC_DEPTH` = %d, `ADDR_W` = %d, `PRETRIG_MAX` = %d, `ROW_COLS` = %d, `ROWS` = %d, `DLINE_ROWS` = %d, `ACC_BITS` = %d, `STACK_N_MAX` = %d |\n",
		g.RecDepth, g.AddrW, g.PretrigMax(), g.RowCols, g.Rows, g.DlineRows, g.AccBits, g.StackNMax)
	p("| build-ID rule | %s |\n\n", BuildIDNote)

	p("## Selectors\n\nAccess: R = read, W = write, RW = both, pop = pop-on-read (every read advances the source), strobe = the written word is decoded, nothing is stored, stub = see above. Since = the schema version that introduced the register.\n\n")
	p("| sel | name | since | access | fields | meaning |\n|---|---|---|---|---|---|\n")
	for _, r := range i.Regs {
		p("| `0x%02x` | `%s` | v%d | %s | %s | %s |\n", r.Sel, r.Name, r.Since, accessDoc(r), md(fieldSummary(r.Fields)), md(r.Desc))
	}
	p("\n## %s opcodes\n\n| value | name | meaning |\n|---|---|---|\n", i.OpcodeReg)
	for _, o := range i.Opcodes {
		p("| `0x%04x` | `%s` | %s |\n", o.Value, o.Name, md(o.Desc))
	}
	p("\n## Register fields\n")
	for _, r := range i.Regs {
		if len(r.Fields) == 0 {
			continue
		}
		p("\n### `%s` (`0x%02x`, %s)\n\n%s\n\n| bits | field | meaning |\n|---|---|---|\n", r.Name, r.Sel, accessDoc(r), md(r.Desc))
		for _, f := range r.Fields {
			p("| %s | `%s` | %s%s |\n", bitsDoc(f), f.Name, md(f.Desc), md(enumDoc(f)))
		}
	}
	p("\n## DIAG window (`%s` -> `%s`)\n\nWrite the index to `%s`, then read or write `%s`. Indices from `0x%02x` are reserved (read 0, writes ignored).\n\n",
		i.DiagIdxReg, i.DiagDataReg, i.DiagIdxReg, i.DiagDataReg, i.DiagReserved)
	p("| idx | name | access | fields | meaning |\n|---|---|---|---|---|\n")
	for _, d := range i.Diag {
		idx := fmt.Sprintf("`0x%02x`", d.Idx)
		if d.Count > 1 {
			idx = fmt.Sprintf("`0x%02x`..`0x%02x`", d.Idx, d.Last())
		}
		p("| %s | `%s` | %s | %s | %s |\n", idx, d.Name, diagAccessDoc(d), md(fieldSummary(d.Fields)), md(d.Desc))
	}
	for _, d := range i.Diag {
		if len(d.Fields) == 0 {
			continue
		}
		p("\n### DIAG `%s` (`0x%02x`, %s)\n\n| bits | field | meaning |\n|---|---|---|\n", d.Name, d.Idx, diagAccessDoc(d))
		for _, f := range d.Fields {
			p("| %s | `%s` | %s%s |\n", bitsDoc(f), f.Name, md(f.Desc), md(enumDoc(f)))
		}
	}
	p("\n## Word formats\n\n")
	p("`app/internal/iface/iface.go` carries the Go models of every drained word format (spliced from `codegen/emit/wordfmt`, unit-tested there); rung R2b compares drained words against them and the fabric RTL is written to match them. Every word is `{CH1, CH2}` = `CH1<<8 | CH2` in time order (`w = ROW_COLS*row + col`); a `Tier` (dual-E1 5 ns, IL5-100 2 ns with column order E1,E3,E5,E2,E4, IL5-200 1 ns with E1..E5) gives the time base and the pair behind each column, never the word content.\n\n")
	p("| model | format | words |\n|---|---|---|\n")
	p("| `ModelRecord` | RAW record, `RUN.CHMODE` | dual: `{CH1[i], CH2[i]}`; single: `{s[2n], s[2n+1]}` of the one channel |\n")
	p("| `ModelTsrc` / `TsrcWord` / `TsrcCheck` | `IL_CTRL.TSRC` test sources, k = the writer's word index since GO | RAMP `k & 0xffff`; COLTAG `{k mod ROW_COLS, (k/ROW_COLS) & 0xff}`; GLITCH `0xffff` when `k mod 512 == 0` else 0 |\n")
	p("| `ModelDecim` | REDUCE_MODE DECIM, D = DECIM | `{CH1[kD], CH2[kD]}` |\n")
	p("| `ModelPeak` | REDUCE_MODE PEAK | per D samples `{min1, min2}` then `{max1, max2}` |\n")
	p("| `ModelBoxcar8` / `ModelBoxcar16` / `BoxcarParams` | REDUCE_MODE BOXCAR8 / BOXCAR16 | `mean_Q8.8 = ((sum >> DEC_PRE) * DEC_RECIP) >> 8` with `DEC_PRE = max(0, ceil(log2 D) - 8)`, `DEC_RECIP = round(2^(16+DEC_PRE) / D)`; BOXCAR8 `{round8(mean1), round8(mean2)}` (round half up, saturating), BOXCAR16 `mean1` then `mean2` |\n")
	p("| `ModelStack` / `StackParams` / `PhaseBin` | `RUN.STACK` batch readout | bins at `row*F + phase` (phase = top log2 F bits of the Q16 trigger fraction), `ACC_BITS`-wide sums; drained as CH1 bins (recA) then CH2 bins (recB), row-major / column-minor, each word `(sum >> STACK_CTRL.SHIFT)[15:0]`; `PHASE_CNT[8]` returned alongside |\n")
	p("\n## Generated names\n\n| artifact | what |\n|---|---|\n")
	p("| `fpga/default/regs.vh` | `` `SEL_<REG> ``, `` `<REG>_<FIELD>_MASK/_LSB ``, `` `<REG>_<FIELD>_<ENUM> ``, `` `OP_<NAME> ``, `` `DIAG_<NAME> `` (+`_BASE/_COUNT/_LAST` for arrays), `` `DIAG_<NAME>_<FIELD>_MASK/_LSB ``, `` `IFACE_BUILD_ID(_LO/_HI) ``, `` `VERSION_MAGIC ``, `` `FABRIC_ID ``, `` `REC_DEPTH ``, `` `ADDR_W ``, `` `PRETRIG_MAX ``, `` `ROW_COLS ``, `` `ROWS ``, `` `DLINE_ROWS ``, `` `ACC_BITS ``, `` `STACK_N_MAX ``, `` `SEL_MASK `` |\n")
	p("| `fpga/default/regmux.vh` | `we_<REG>` write strobes (non-stub), `op_<NAME>` opcode strobes, `pop_<REG>` pop strobes (aliases folded in), `rmux_rdata` read mux from `rdata_<REG>` (stubs and undecoded selectors read 0); selectors compared after `& SEL_MASK` |\n")
	p("| `app/internal/iface/iface.go` | `Sel<Reg>`, `<Reg><Field>Mask/Shift`, `<Reg><Field><Enum>`, `Op<Name>`, `Diag<Name>`, `BuildID`, `VersionMagic`, `FabricID`, geometry constants, `Registers()`, `Undecoded()`, `DiagWindow()`, `Opcodes()`, `CheckIdentity()`, `Tier*`, `Model*` |\n")
	return b.String(), nil
}

// TRLC-LINKS: REQ-SDS-157
func bitsDoc(f schema.Field) string {
	if f.Hi == f.Lo {
		return fmt.Sprintf("[%d]", f.Lo)
	}
	return fmt.Sprintf("[%d:%d]", f.Hi, f.Lo)
}
