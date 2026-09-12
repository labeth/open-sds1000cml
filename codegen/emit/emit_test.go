package emit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"open-sds/codegen/emit/wordfmt"
	"open-sds/codegen/ifacedef"
	"open-sds/codegen/schema"
)

func mustContain(t *testing.T, what, out string, snippets ...string) {
	t.Helper()
	for _, s := range snippets {
		if !strings.Contains(out, s) {
			t.Errorf("%s: missing snippet %q", what, s)
		}
	}
}

func mustNotContain(t *testing.T, what, out string, snippets ...string) {
	t.Helper()
	for _, s := range snippets {
		if strings.Contains(out, s) {
			t.Errorf("%s: must not contain %q", what, s)
		}
	}
}

func idHex(i schema.Interface) (string, string, string) {
	id := i.BuildID()
	return fmt.Sprintf("%08x", id), fmt.Sprintf("%04x", id&0xffff), fmt.Sprintf("%04x", id>>16)
}

func TestRegsGolden(t *testing.T) {
	i := ifacedef.Default()
	out, err := Regs(i)
	if err != nil {
		t.Fatal(err)
	}
	full, lo, hi := idHex(i)
	mustContain(t, "regs.vh", out,
		"`ifndef IFACE_REGS_VH", "`define IFACE_REGS_VH", "`endif",
		"`define IFACE_VERSION 16'd3",
		"`define IFACE_BUILD_ID 32'h"+full, "`define IFACE_BUILD_ID_LO 16'h"+lo, "`define IFACE_BUILD_ID_HI 16'h"+hi,
		"`define VERSION_MAGIC 16'h00a3", "`define FABRIC_ID 16'ha2f1",
		"`define SEL_MASK 8'h7f  // selector bits the fabric decodes (128 selectors): decodes GPMC A1,A2,A3,A4,A5,A6,A7; selector bit(s) 7 ignored",
		"`define SEL_MASK_V2 8'h7c",
		"// undecoded selectors (read 0, writes ignored; no register may take them): 0x21, 0x57",
		"`define REC_DEPTH 20480", "`define ADDR_W 15", "`define PRETRIG_MAX 20478",
		"`define ROW_COLS 5", "`define ROWS 4096", "`define DLINE_ROWS 512", "`define ACC_BITS 18", "`define STACK_N_MAX 1024",
		"`define OP_RESET 16'h0000", "`define OP_GO 16'h0001", "`define OP_HALT 16'h0002", "`define OP_REWIND 16'h0003", "`define OP_STACK_CLEAR 16'h0004",
		"`define SEL_BURST_ALIAS 8'h00", "`define SEL_BURST 8'h40", "`define SEL_DBG 8'h7c", "`define SEL_ACQ_CTRL 8'h58",
		"`define SEL_IL_CTRL 8'h25", "`define SEL_DRAIN_START 8'h41", "`define SEL_DRAIN_LEN 8'h42", "`define SEL_DRAIN_STAT 8'h43",
		"`define SEL_POP_MON 8'h45", "`define SEL_STATUS_B 8'h4d", "`define SEL_DBG2 8'h7d",
		"// SEL_STACK_N: RW stub — ",
		"`define RUN_MODE_MASK 16'h0003\n`define RUN_MODE_LSB 0",
		"`define RUN_MODE_AUTO 2'd0", "`define RUN_MODE_NORM 2'd1",
		"`define RUN_CHMODE_MASK 16'h0030\n`define RUN_CHMODE_LSB 4",
		"`define RUN_CHMODE_DUAL 2'd0", "`define RUN_CHMODE_CH1 2'd1", "`define RUN_CHMODE_CH2 2'd2",
		"`define RUN_REDUCE_MASK 16'h0080",
		"`define IL_CTRL_TSRC_MASK 16'h000c\n`define IL_CTRL_TSRC_LSB 2",
		"`define IL_CTRL_TSRC_ADC 2'd0", "`define IL_CTRL_TSRC_RAMP 2'd1", "`define IL_CTRL_TSRC_COLTAG 2'd2", "`define IL_CTRL_TSRC_GLITCH 2'd3",
		"`define IL_CTRL_REDUCE_MODE_MASK 16'h0070", "`define IL_CTRL_REDUCE_MODE_BOXCAR16 3'd4",
		"`define IL_CTRL_IL_RATE_MASK 16'h0080", "`define IL_CTRL_IL_RATE_IL5_200 1'd1",
		"`define IL_CTRL_DIV_PH_MASK 16'h1f00\n`define IL_CTRL_DIV_PH_LSB 8",
		"`define ACQ_CTRL_ENC_RATE_R100M 2'd2",
		"`define POP_MON_MIN_GAP_MASK 16'h00ff", "`define POP_MON_UNDERRUN_MASK 16'hff00\n`define POP_MON_UNDERRUN_LSB 8",
		"`define DRAIN_START_IDX_MASK 16'h7fff", "`define STACK_CTRL_HOLDOFF_MASK 16'hfff0",
		"`define BURST_REMAIN_READY_MASK 16'h8000\n`define BURST_REMAIN_READY_LSB 15",
		"`define CLK_STAT_RATIO_MASK 16'hfff0\n`define CLK_STAT_RATIO_LSB 4",
		"`define SNOOP_SEL_SEL_MASK 16'h007f",
		"`define DIAG_BUS_OE_LO 8'h00", "`define DIAG_PAIR_TRIM 8'h0c", "`define DIAG_IL_DLY 8'h0d", "`define DIAG_SNAP_CTRL2 8'h0e",
		"`define DIAG_LANEMAP_BASE 8'h10\n`define DIAG_LANEMAP_COUNT 80\n`define DIAG_LANEMAP_LAST 8'h5f",
		"`define DIAG_CAL_OFF_BASE 8'h60\n`define DIAG_CAL_OFF_COUNT 10\n`define DIAG_CAL_OFF_LAST 8'h69",
		"`define DIAG_CAL_GAIN_BASE 8'h6a", "`define DIAG_PHASE_CNT_BASE 8'h74\n`define DIAG_PHASE_CNT_COUNT 8\n`define DIAG_PHASE_CNT_LAST 8'h7b",
		"`define DIAG_CAL_STAT 8'h7c", "`define DIAG_CAP_SEL_BASE 8'h7d\n`define DIAG_CAP_SEL_COUNT 5\n`define DIAG_CAP_SEL_LAST 8'h81",
		"`define DIAG_SNAP_CTRL2_SRC_BUS 2'd3",
		"`define DIAG_SINGLE_CTRL_DRV_MASK 16'h1f00\n`define DIAG_SINGLE_CTRL_DRV_LSB 8",
		"`define DIAG_RESERVED_BASE 8'h9f",
	)
	mustNotContain(t, "regs.vh", out, "TODO", "DIAG_PHASE_SEL")
}

func TestRegmuxGolden(t *testing.T) {
	i := ifacedef.Default()
	out, err := Regmux(i)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, "regmux.vh", out,
		"wire [7:0] rmux_wr_sel = wr_sel & `SEL_MASK;",
		"wire [7:0] rmux_rd_sel = rd_sel & `SEL_MASK;",
		"// bits 1:0 are GPMC A2/A1 (balls A2/B1): the slave presents them in sel[1:0].",
		"// undecoded by contract (read 0, writes ignored): 0x21, 0x57",
		"wire we_DIAG_IDX = we_commit & (rmux_wr_sel == `SEL_DIAG_IDX);",
		"wire we_OPCODE = we_commit & (rmux_wr_sel == `SEL_OPCODE);",
		"wire we_TRIG_LEVEL = we_commit & (rmux_wr_sel == `SEL_TRIG_LEVEL);",
		"wire we_IL_CTRL = we_commit & (rmux_wr_sel == `SEL_IL_CTRL);",
		"wire we_DRAIN_START = we_commit & (rmux_wr_sel == `SEL_DRAIN_START);",
		"wire we_DRAIN_LEN = we_commit & (rmux_wr_sel == `SEL_DRAIN_LEN);",
		"// STACK_N: stub, writes ignored",
		"wire op_GO = we_OPCODE & (wr_data == `OP_GO);",
		"wire op_REWIND = we_OPCODE & (wr_data == `OP_REWIND);",
		"wire op_STACK_CLEAR = we_OPCODE & (wr_data == `OP_STACK_CLEAR);",
		"wire pop_BURST = rd_pop & ((rmux_rd_sel == `SEL_BURST) | (rmux_rd_sel == `SEL_BURST_ALIAS));",
		"wire pop_SNAP_POP = rd_pop & ((rmux_rd_sel == `SEL_SNAP_POP));",
		"wire pop_ENV_DATA = rd_pop & ((rmux_rd_sel == `SEL_ENV_DATA));",
		"reg [15:0] rmux_rdata;",
		"`SEL_BURST_ALIAS: rmux_rdata = rdata_BURST; // alias of BURST",
		"`SEL_BUILDID_LO: rmux_rdata = `IFACE_BUILD_ID_LO;",
		"`SEL_BUILDID_HI: rmux_rdata = `IFACE_BUILD_ID_HI;",
		"`SEL_VERSION: rmux_rdata = 16'h00a3; // constant",
		"`SEL_FABRIC_ID: rmux_rdata = 16'ha2f1; // constant",
		"`SEL_STATUS_A: rmux_rdata = rdata_STATUS_A;",
		"`SEL_IL_CTRL: rmux_rdata = rdata_IL_CTRL;",
		"`SEL_DRAIN_STAT: rmux_rdata = rdata_DRAIN_STAT;",
		"`SEL_POP_MON: rmux_rdata = rdata_POP_MON;",
		"`SEL_STATUS_B: rmux_rdata = 16'h0000; // stub: reads 0 in this increment",
		"`SEL_STACK_N: rmux_rdata = 16'h0000; // stub",
		"`SEL_DBG2: rmux_rdata = 16'h0000; // stub",
		"`SEL_DBG: rmux_rdata = rdata_DBG;",
		"default: rmux_rdata = 16'h0000;",
	)
	// no strobe for read-only or stub registers, no read case for the write-only
	// strobe, no rdata wire for schema-driven or stub reads, no pop for the alias
	// itself, no decode of the raw selector, no old {1'b0, sel[6:2], 2'b00} mask
	mustNotContain(t, "regmux.vh", out,
		"we_BURST ", "we_STATUS_A", "we_BUILDID", "we_VERSION", "we_FABRIC_ID", "we_BURST_ALIAS",
		"wire we_STACK_N ", "wire we_STACK_CTRL", "wire we_DEC_RECIP", "wire we_EYE_CTRL",
		"`SEL_OPCODE: rmux_rdata", "rdata_BUILDID", "rdata_VERSION", "rdata_FABRIC_ID", "rdata_BURST_ALIAS",
		"rdata_STATUS_B", "rdata_STACK_", "rdata_EYE_", "rdata_DBG2", "rdata_DEC_",
		"pop_BURST_ALIAS", "(wr_sel ==", "(rd_sel ==", "wr_sel[6:2]", "rd_sel[6:2]",
	)
}

func TestGoBindingsGolden(t *testing.T) {
	i := ifacedef.Default()
	out, err := GoBindings(i)
	if err != nil {
		t.Fatal(err)
	}
	full, lo, hi := idHex(i)
	// gofmt column-aligns const blocks; compare on collapsed whitespace
	out = regexp.MustCompile(`[ \t]+`).ReplaceAllString(out, " ")
	mustContain(t, "iface.go", out,
		"package iface",
		"Name = \"sds1000cml-default\"", "Version uint16 = 3",
		"BuildID uint32 = 0x"+full, "BuildIDLo uint16 = 0x"+lo, "BuildIDHi uint16 = 0x"+hi,
		"VersionMagic uint16 = 0x00a3", "FabricID uint16 = 0xa2f1", "SelMask uint16 = 0x7f", "SelCount = 128", "SelMaskV2 uint16 = 0x7c",
		"RecDepth = 20480", "AddrW = 15", "PretrigMax = 20478", "RowCols = 5", "Rows = 4096", "DlineRows = 512", "AccBits = 18", "StackNMax = 1024",
		"SelBurstAlias uint16 = 0x00", "SelBurst uint16 = 0x40", "SelBurstRemain uint16 = 0x44", "SelDbg uint16 = 0x7c",
		"SelIlCtrl uint16 = 0x25", "SelDrainStart uint16 = 0x41", "SelDrainLen uint16 = 0x42", "SelDrainStat uint16 = 0x43", "SelPopMon uint16 = 0x45",
		"SelStackN uint16 = 0x46 // RW stub:", "SelStatusB uint16 = 0x4d // R stub:", "SelDbg2 uint16 = 0x7d",
		"OpReset uint16 = 0x0000", "OpGo uint16 = 0x0001", "OpHalt uint16 = 0x0002", "OpRewind uint16 = 0x0003", "OpStackClear uint16 = 0x0004",
		"RunModeMask uint16 = 0x0003", "RunModeShift = 0", "RunChmodeMask uint16 = 0x0030", "RunChmodeShift = 4", "RunReduceMask uint16 = 0x0080",
		"RunChmodeDual uint16 = 0", "RunChmodeCh1 uint16 = 1", "RunChmodeCh2 uint16 = 2",
		"IlCtrlTsrcMask uint16 = 0x000c", "IlCtrlTsrcShift = 2", "IlCtrlTsrcAdc uint16 = 0", "IlCtrlTsrcRamp uint16 = 1", "IlCtrlTsrcColtag uint16 = 2", "IlCtrlTsrcGlitch uint16 = 3",
		"IlCtrlReduceModeRaw uint16 = 0", "IlCtrlReduceModeBoxcar16 uint16 = 4", "IlCtrlIlRateIl5200 uint16 = 1", "AcqCtrlEncRateR200m uint16 = 3",
		"PopMonMinGapMask uint16 = 0x00ff", "PopMonUnderrunMask uint16 = 0xff00", "PopMonUnderrunShift = 8",
		"BurstRemainReadyMask uint16 = 0x8000", "StatusADoneMask uint16 = 0x0004", "AcqCtrlEncRateMask uint16 = 0x0006",
		"DiagBusOeLo uint16 = 0x00", "DiagPairTrim uint16 = 0x0c // RW stub:", "DiagIlDly uint16 = 0x0d", "DiagSnapCtrl2 uint16 = 0x0e",
		"DiagLanemapBase uint16 = 0x10 // R:", "DiagLanemapCount = 80", "DiagLanemapLast uint16 = 0x5f",
		"DiagCalOffBase uint16 = 0x60", "DiagCalGainBase uint16 = 0x6a", "DiagPhaseCntBase uint16 = 0x74", "DiagCalStat uint16 = 0x7c", "DiagCapSelBase uint16 = 0x7d", "DiagCapSelLast uint16 = 0x81",
		"DiagReservedBase uint16 = 0x9f", "DiagSingleCtrlOeMask uint16 = 0x001f", "DiagPairTrimE2Mask uint16 = 0x000f", "DiagSnapCtrl2SrcBus uint16 = 3",
		"func Registers() []Register", "func Undecoded() []uint16", "func IsUndecoded(sel uint16) bool", "func DiagWindow() []DiagEntry", "func Opcodes() []Opcode",
		"func CheckIdentity(buildLo, buildHi, version, fabric uint16) error", "func MaskSel(sel uint16) uint16", "func (f Field) ValueName(word uint16) string",
		`{Name: "BURST_ALIAS", Sel: SelBurstAlias, Since: 2, Access: AccR, Pop: true, Strobe: false, Const: false, ConstValue: 0x0000, AliasOf: "BURST", Stub: false`,
		`{Name: "VERSION", Sel: SelVersion, Since: 2, Access: AccR, Pop: false, Strobe: false, Const: true, ConstValue: 0x00a3`,
		`{Name: "OPCODE", Sel: SelOpcode, Since: 2, Access: AccW, Pop: false, Strobe: true`,
		`{Name: "STACK_N", Sel: SelStackN, Since: 3, Access: AccRW, Pop: false, Strobe: false, Const: false, ConstValue: 0x0000, AliasOf: "", Stub: true`,
		`Enum: []EnumValue{{"ADC", 0, "the converters"}, {"RAMP", 1, "ramp, +1 per word"}`,
		"var undecoded = []uint16{0x21, 0x57}",
		`{Name: "LANEMAP", Idx: 0x10, Count: 80, Access: AccR, Stub: false`,
		`{Name: "CAP_SEL", Idx: 0x7d, Count: 5, Access: AccR, Stub: true`,
		// the spliced models
		"type Tier struct", "TierIL5x100 = Tier{", "func ModelRecord(", "func ModelTsrc(", "func TsrcCheck(", "func ModelPeak(", "func ModelBoxcar8(", "func ModelBoxcar16(", "func BoxcarParams(", "func ModelStack(", "func PhaseBin(",
	)
	mustNotContain(t, "iface.go", out, "TODO", "package wordfmt", "DiagPhaseSel")
	if strings.Count(out, "\nimport ") != 1 {
		t.Error("the bindings must have exactly one import declaration")
	}
}

func TestDocGolden(t *testing.T) {
	i := ifacedef.Default()
	out, err := Doc(i)
	if err != nil {
		t.Fatal(err)
	}
	full, _, _ := idHex(i)
	mustContain(t, "REGISTER-MAP.md", out,
		"# Register map — sds1000cml-default v3 (build-ID `0x"+full+"`)",
		"| `0x00` | `BURST_ALIAS` | v2 | R pop |",
		"| `0x24` | `RUN` | v2 | RW | MODE[1:0] RUN[2] STREAM[3] CHMODE[5:4] STACK[6] REDUCE[7] |",
		"| `0x25` | `IL_CTRL` | v3 | RW | IL_EN[0] CAL_EN[1] TSRC[3:2] REDUCE_MODE[6:4] IL_RATE[7] DIV_PH[12:8] |",
		"| `0x46` | `STACK_N` | v3 | RW stub |",
		"| `0x7c` | `DBG` | v2 | R |",
		"| `0x0001` | `GO` |", "| `0x0003` | `REWIND` |",
		"| undecoded selectors | 0x21, 0x57 —",
		"`0x7f` (128 selectors, decodes GPMC A1,A2,A3,A4,A5,A6,A7; selector bit(s) 7 ignored)",
		"`ROW_COLS` = 5, `ROWS` = 4096, `DLINE_ROWS` = 512, `ACC_BITS` = 18, `STACK_N_MAX` = 1024",
		"### `RUN` (`0x24`, RW)",
		"Values: 0=`DUAL`, 1=`CH1`, 2=`CH2`.",
		"Values: 0=`ADC`, 1=`RAMP`, 2=`COLTAG`, 3=`GLITCH`.",
		"| `0x10`..`0x5f` | `LANEMAP` | R |",
		"| `0x0c` | `PAIR_TRIM` | RW stub |",
		"| `0x7d`..`0x81` | `CAP_SEL` | R stub |",
		"### DIAG `ADC_HOLD` (`0x07`, RW)",
		"reserved (read 0, writes ignored).",
		"`0x90`",
		"## Word formats", "`ModelStack`",
	)
	if n := strings.Count(out, "\n| `0x"); n < 48+22 {
		t.Errorf("only %d table rows", n)
	}
}

// Every artifact carries the same build-ID string.
func TestBuildIDConsistent(t *testing.T) {
	i := ifacedef.Default()
	full, _, _ := idHex(i)
	for name, fn := range map[string]func(schema.Interface) (string, error){"regs": Regs, "regmux": Regmux, "go": GoBindings, "doc": Doc} {
		out, err := fn(i)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, full) {
			t.Errorf("%s: build-ID %s missing", name, full)
		}
	}
}

func TestEmittersRejectInvalid(t *testing.T) {
	i := ifacedef.Default()
	i.Regs[1].Sel = 0x40 // collides with BURST
	for name, fn := range map[string]func(schema.Interface) (string, error){"regs": Regs, "regmux": Regmux, "go": GoBindings, "doc": Doc} {
		if _, err := fn(i); err == nil || !strings.Contains(err.Error(), "already used") {
			t.Errorf("%s: invalid schema accepted (err=%v)", name, err)
		}
	}
}

func TestCamel(t *testing.T) {
	for in, want := range map[string]string{"RUN": "Run", "BURST_REMAIN": "BurstRemain", "E1E2": "E1e2", "STATUS_A": "StatusA", "TRIGPOS_LO": "TrigposLo", "IL5_200": "Il5200"} {
		if got := camel(in); got != want {
			t.Errorf("camel(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestSelMaskDoc(t *testing.T) {
	if got := selMaskDoc(0x7c); got != "decodes GPMC A3,A4,A5,A6,A7; selector bit(s) 0,1,7 ignored" {
		t.Errorf("0x7c: %q", got)
	}
	if got := selMaskDoc(0xff); got != "decodes GPMC A1,A2,A3,A4,A5,A6,A7,A8" {
		t.Errorf("0xff: %q", got)
	}
}

func TestSpliceGoSource(t *testing.T) {
	body, err := spliceGoSource("// doc\npackage x\n\nimport \"fmt\"\n\nfunc F() { fmt.Println() }\n")
	if err != nil || body != "func F() { fmt.Println() }\n" {
		t.Errorf("%q %v", body, err)
	}
	body, err = spliceGoSource("package x\n\nconst A = 1\n")
	if err != nil || body != "const A = 1\n" {
		t.Errorf("no import: %q %v", body, err)
	}
	if _, err = spliceGoSource("package x\n\nimport (\n\t\"os\"\n)\n"); err == nil {
		t.Error("other import accepted")
	}
	if _, err = spliceGoSource("package x\n\nimport \"fmt\"\n\nimport \"os\"\n"); err == nil {
		t.Error("second import accepted")
	}
	if _, err = spliceGoSource("const A = 1\n"); err == nil {
		t.Error("no package clause accepted")
	}
	if _, err = spliceGoSource(wordfmtSrc); err != nil {
		t.Errorf("the real wordfmt source does not splice: %v", err)
	}
}

// The constants wordfmt/geom.go duplicates for its own tests must equal the
// schema's (iface.go gets them from the schema, the spliced models use them).
func TestWordfmtConstantsMatchSchema(t *testing.T) {
	i := ifacedef.Default()
	g := i.Geometry
	if wordfmt.RowCols != int(g.RowCols) || wordfmt.Rows != int(g.Rows) || wordfmt.RecDepth != int(g.RecDepth) ||
		wordfmt.DlineRows != int(g.DlineRows) || wordfmt.AccBits != int(g.AccBits) || wordfmt.StackNMax != int(g.StackNMax) {
		t.Errorf("wordfmt geometry differs from the schema's %+v", g)
	}
	enum := func(reg, field, name string) uint16 {
		r, _ := i.Reg(reg)
		for _, f := range r.Fields {
			if f.Name != field {
				continue
			}
			for _, e := range f.Enum {
				if e.Name == name {
					return e.Value
				}
			}
		}
		t.Errorf("%s.%s.%s missing from the schema", reg, field, name)
		return 0xffff
	}
	for k, v := range map[string]uint16{
		"RUN.CHMODE.DUAL": wordfmt.RunChmodeDual, "RUN.CHMODE.CH1": wordfmt.RunChmodeCh1, "RUN.CHMODE.CH2": wordfmt.RunChmodeCh2,
		"IL_CTRL.TSRC.ADC": wordfmt.IlCtrlTsrcAdc, "IL_CTRL.TSRC.RAMP": wordfmt.IlCtrlTsrcRamp, "IL_CTRL.TSRC.COLTAG": wordfmt.IlCtrlTsrcColtag, "IL_CTRL.TSRC.GLITCH": wordfmt.IlCtrlTsrcGlitch,
		"IL_CTRL.REDUCE_MODE.RAW": wordfmt.IlCtrlReduceModeRaw, "IL_CTRL.REDUCE_MODE.DECIM": wordfmt.IlCtrlReduceModeDecim, "IL_CTRL.REDUCE_MODE.PEAK": wordfmt.IlCtrlReduceModePeak,
		"IL_CTRL.REDUCE_MODE.BOXCAR8": wordfmt.IlCtrlReduceModeBoxcar8, "IL_CTRL.REDUCE_MODE.BOXCAR16": wordfmt.IlCtrlReduceModeBoxcar16,
	} {
		parts := strings.Split(k, ".")
		if got := enum(parts[0], parts[1], parts[2]); got != v {
			t.Errorf("%s: schema %d, wordfmt %d", k, got, v)
		}
	}
	// the generated Go name of each enum is the name wordfmt uses
	out, _ := GoBindings(i)
	out = regexp.MustCompile(`[ \t]+`).ReplaceAllString(out, " ")
	for _, n := range []string{"RunChmodeDual uint16 = 0", "IlCtrlTsrcGlitch uint16 = 3", "IlCtrlReduceModeBoxcar16 uint16 = 4", "RowCols = 5", "StackNMax = 1024"} {
		if !strings.Contains(out, n) {
			t.Errorf("iface.go lacks %q", n)
		}
	}
}

// The generated Go bindings must compile and behave: build a throwaway module
// around iface.go and run a small program against its tables and models.
func TestGoBindingsCompile(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}
	i := ifacedef.Default()
	out, err := GoBindings(i)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "iface"), 0o755)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.26\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "iface", "iface.go"), []byte(out), 0o644)
	stubs := 0
	for _, r := range i.Regs {
		if r.Stub {
			stubs++
		}
	}
	prog := fmt.Sprintf(`package main

import (
	"fmt"
	"os"
	"x/iface"
)

func main() {
	fail := func(f string, a ...any) { fmt.Printf(f+"\n", a...); os.Exit(1) }
	if len(iface.Registers()) != %d { fail("registers") }
	r, ok := iface.BySel(0x40 | 0x80) // bit 7 masked -> 0x40
	if !ok || r.Name != "BURST" || !r.Pop { fail("BySel mask: %%+v", r) }
	if r, ok := iface.BySel(0x43); !ok || r.Name != "DRAIN_STAT" || r.Since != 3 { fail("odd slot: %%+v", r) }
	if _, ok := iface.BySel(0x21); ok || !iface.IsUndecoded(0x21) || !iface.IsUndecoded(0x57|0x80) || iface.IsUndecoded(0x25) || len(iface.Undecoded()) != 2 { fail("undecoded") }
	a, _ := iface.ByName("BURST_ALIAS")
	if a.AliasOf != "BURST" || a.Sel != 0 { fail("alias") }
	stubs := 0
	for _, r := range iface.Registers() { if r.Stub { stubs++ } }
	if stubs != %d { fail("stubs %%d", stubs) }
	if s, _ := iface.ByName("STATUS_B"); !s.Stub { fail("STATUS_B stub") }
	if iface.CheckIdentity(iface.BuildIDLo, iface.BuildIDHi, iface.VersionMagic, iface.FabricID) != nil { fail("identity") }
	if iface.CheckIdentity(iface.BuildIDLo^1, iface.BuildIDHi, iface.VersionMagic, iface.FabricID) == nil { fail("identity build") }
	if iface.CheckIdentity(iface.BuildIDLo, iface.BuildIDHi, 0x00a2, iface.FabricID) == nil { fail("identity version") }
	if iface.CheckIdentity(iface.BuildIDLo, iface.BuildIDHi, iface.VersionMagic, 0) == nil { fail("identity fabric") }
	run, _ := iface.ByName("RUN")
	var w uint16
	for _, f := range run.Fields { if f.Name == "CHMODE" { w = f.Put(w, iface.RunChmodeCh2); if f.Get(w) != 2 || w != 0x0020 || f.ValueName(w) != "CH2" { fail("field put/get") } } }
	if (w & iface.RunChmodeMask) >> iface.RunChmodeShift != 2 { fail("mask/shift") }
	il, _ := iface.ByName("IL_CTRL")
	if il.Fields[2].Name != "TSRC" || il.Fields[2].ValueName(iface.IlCtrlTsrcRamp << iface.IlCtrlTsrcShift) != "RAMP" || il.Fields[2].ValueName(0) != "ADC" { fail("enum names") }
	if iface.DiagLanemapBase + iface.DiagLanemapCount - 1 != iface.DiagLanemapLast { fail("lanemap") }
	if iface.DiagCapSelBase + iface.DiagCapSelCount - 1 != iface.DiagCapSelLast || iface.DiagCapSelLast >= iface.DiagReservedBase { fail("cap_sel") }
	if len(iface.DiagWindow()) != %d || len(iface.Opcodes()) != %d { fail("tables") }
	if iface.RowCols*iface.Rows != iface.RecDepth { fail("geometry") }
	// the spliced models
	s, err := iface.ModelTsrc(iface.TierIL5x100, iface.IlCtrlTsrcColtag, 0, iface.RecDepth)
	if err != nil || len(s.Words) != iface.RecDepth || s.Words[7] != 0x0201 || s.TimeNs(7) != 14 || s.Tier.PairOfWord(7) != 4 { fail("ModelTsrc %%v", err) }
	if k0, bad, err := iface.TsrcCheck(iface.IlCtrlTsrcColtag, s.Words[5:]); err != nil || k0 != 5 || len(bad) != 0 { fail("TsrcCheck") }
	rec, err := iface.ModelRecord(iface.TierDualE1.Decimated(2), iface.RunChmodeDual, []uint8{1, 2}, []uint8{3, 4})
	if err != nil || rec.Words[1] != 0x0204 || rec.TimeNs(1) != 10 { fail("ModelRecord") }
	recip, pre, err := iface.BoxcarParams(1000)
	if err != nil || pre != 2 || recip != 262 { fail("BoxcarParams %%d %%d %%v", recip, pre, err) }
	st, cnt, err := iface.ModelStack(iface.TierIL5x200, iface.StackParams{PhaseBins: 2, Shift: 1, Rows: 8}, []iface.StackRecord{{Phase: iface.PhaseBin(0xc000, 2), CH1: make([]uint8, 40), CH2: make([]uint8, 40)}})
	if err != nil || len(st.Words) != 2*8*4*5 || cnt[3] != 1 { fail("ModelStack %%v", err) }
	fmt.Println("BINDINGS OK")
}
`, len(i.Regs), stubs, len(i.Diag), len(i.Opcodes))
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(prog), 0o644)
	cmd := exec.Command(gobin, "run", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	res, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(res), "BINDINGS OK") {
		t.Fatalf("generated bindings failed: %v\n%s", err, res)
	}
	vet := exec.Command(gobin, "vet", "./...")
	vet.Dir = dir
	if res, err := vet.CombinedOutput(); err != nil {
		t.Fatalf("go vet of the generated bindings: %v\n%s", err, res)
	}
}
