// ENGMODEL-OWNER-UNIT: FU-CODEGEN-IFACEDEF
package ifacedef

import (
	"strings"
	"testing"

	"open-sds/codegen/schema"
)

// TRLC-LINKS: REQ-SDS-156, REQ-SDS-031
func TestDefaultValidates(t *testing.T) {
	if errs := Default().Validate(); len(errs) > 0 {
		for _, e := range errs {
			t.Error(e)
		}
	}
}

// The 32 registers of 05-WORKPLAN §2 keep their 4-aligned selectors (06-TIERS
// §2: "every v2 register keeps its selector").
// TRLC-LINKS: REQ-SDS-156
func TestV2RegistersKeepTheirSelectors(t *testing.T) {
	want := map[uint8]string{
		0x00: "BURST_ALIAS", 0x04: "CLK_STAT", 0x08: "DIAG_IDX", 0x0c: "DIAG_DATA",
		0x10: "BUILDID_LO", 0x14: "BUILDID_HI", 0x18: "VERSION", 0x1c: "FABRIC_ID",
		0x20: "OPCODE", 0x24: "RUN", 0x28: "DECIM_LO", 0x2c: "DECIM_HI",
		0x30: "PRETRIG_LO", 0x34: "PRETRIG_HI", 0x38: "POSTTRIG_LO", 0x3c: "POSTTRIG_HI",
		0x40: "BURST", 0x44: "BURST_REMAIN", 0x48: "STATUS_A", 0x4c: "TRIGPOS_LO",
		0x50: "TRIGPOS_HI", 0x54: "FILL", 0x58: "ACQ_CTRL", 0x5c: "TRIG_LEVEL",
		0x60: "SNOOP_SEL", 0x64: "SNOOP_DATA", 0x68: "DIAG_CTRL", 0x6c: "SNAP_REMAIN",
		0x70: "SNAP_POP", 0x74: "MISC_RD", 0x78: "ENV_DATA", 0x7c: "DBG",
	}
	i := Default()
	n := 0
	for _, r := range i.Regs {
		if r.Since > 2 {
			continue
		}
		n++
		if want[r.Sel] != r.Name {
			t.Errorf("sel 0x%02x is %s, 05-WORKPLAN says %s", r.Sel, r.Name, want[r.Sel])
		}
		if r.Stub {
			t.Errorf("%s: a v2 register cannot be a stub", r.Name)
		}
	}
	if n != 32 {
		t.Fatalf("got %d v2 registers, want 32", n)
	}
	pops := map[string]bool{"BURST_ALIAS": true, "BURST": true, "SNAP_POP": true, "ENV_DATA": true}
	for _, r := range i.Regs {
		if r.Pop != pops[r.Name] {
			t.Errorf("%s: pop=%t, plan says %t", r.Name, r.Pop, pops[r.Name])
		}
	}
	if a, _ := i.Reg("BURST_ALIAS"); a.Source != schema.SrcAlias || a.AliasOf != "BURST" {
		t.Errorf("BURST_ALIAS must alias BURST: %+v", a)
	}
}

// 06-TIERS §2: the v3 identity, selector space, additions and their v2.2 state.
// TRLC-LINKS: REQ-SDS-156
func TestDefaultMatchesTiers(t *testing.T) {
	i := Default()
	if i.Name != "sds1000cml-default" || i.Version != 3 || i.VersionMagic != 0x00A3 || i.FabricID != 0xA2F1 {
		t.Errorf("identity: %s v%d magic 0x%04x fabric 0x%04x", i.Name, i.Version, i.VersionMagic, i.FabricID)
	}
	if i.SelMask != 0x7f || i.SelCount() != 128 || i.Legacy != (schema.Legacy{Version: 2, SelMask: 0x7c}) {
		t.Errorf("selector space: mask 0x%02x legacy %+v", i.SelMask, i.Legacy)
	}
	if len(i.Undecoded) != 2 || i.Undecoded[0] != 0x21 || i.Undecoded[1] != 0x57 {
		t.Errorf("undecoded: %v, want 0x21 0x57", i.Undecoded)
	}
	g := i.Geometry
	if g.RecDepth != 20480 || g.AddrW != 15 || g.PretrigMax() != 20478 || g.RowCols != 5 || g.Rows != 4096 || g.DlineRows != 512 || g.AccBits != 18 || g.StackNMax != 1024 {
		t.Errorf("geometry: %+v", g)
	}
	// v3 registers: selector, access, fields, v2.2 state (stub = reads 0)
	type reg struct {
		sel    uint8
		acc    schema.Access
		fields string
		stub   bool
	}
	want := map[string]reg{
		"IL_CTRL":     {0x25, schema.RW, "IL_EN CAL_EN TSRC REDUCE_MODE IL_RATE DIV_PH", false},
		"DEC_RECIP":   {0x26, schema.RW, "RECIP", true},
		"DEC_PRE":     {0x27, schema.RW, "PRE", true},
		"DRAIN_START": {0x41, schema.RW, "IDX", false},
		"DRAIN_LEN":   {0x42, schema.RW, "LEN", false},
		"DRAIN_STAT":  {0x43, schema.R, "POPS", false},
		"POP_MON":     {0x45, schema.R, "MIN_GAP UNDERRUN", false},
		"STACK_N":     {0x46, schema.RW, "N", true},
		"STACK_CTRL":  {0x47, schema.RW, "PHASE_BINS SHIFT HOLDOFF", true},
		"STACK_CNT":   {0x49, schema.R, "RECORDS PHASE_LAST", true},
		"STACK_PRE":   {0x4a, schema.RW, "ROWS", true},
		"STACK_LEN":   {0x4b, schema.RW, "ROWS", true},
		"STATUS_B":    {0x4d, schema.R, "IL_ACTIVE STACK_BUSY STACK_DONE FIFO_EMPTY STEP_BUSY STEP_ERR CAL_SAT", true},
		"EYE_CTRL":    {0x4e, schema.RW, "CORE CENTRE TOL EN", true},
		"EYE_CNT":     {0x4f, schema.R, "COUNT", true},
		"DBG2":        {0x7d, schema.R, "STEPS CSEL BUSY ERR", true},
	}
	n := 0
	for _, r := range i.Regs {
		if r.Since < 3 {
			continue
		}
		n++
		w, ok := want[r.Name]
		if !ok {
			t.Errorf("%s: not in 06-TIERS §2", r.Name)
			continue
		}
		var names []string
		for _, f := range r.Fields {
			names = append(names, f.Name)
		}
		if r.Sel != w.sel || r.Access != w.acc || strings.Join(names, " ") != w.fields || r.Stub != w.stub {
			t.Errorf("%s: sel 0x%02x %s fields %q stub %t; want 0x%02x %s %q stub %t", r.Name, r.Sel, r.Access, strings.Join(names, " "), r.Stub, w.sel, w.acc, w.fields, w.stub)
		}
		if r.Sel&3 == 0 {
			t.Errorf("%s: a v3 register in a 4-aligned slot", r.Name)
		}
		if r.Stub && !strings.Contains(r.Desc, "v3.0+: reads 0 in v2.2") {
			t.Errorf("%s: a stub must say so in its Desc", r.Name)
		}
	}
	if n != len(want) {
		t.Errorf("%d v3 registers, want %d", n, len(want))
	}
	if len(i.Regs) != 32+len(want) {
		t.Errorf("%d registers in total", len(i.Regs))
	}
	// the partly implemented IL_CTRL: only TSRC is live in v2.2
	il, _ := i.Reg("IL_CTRL")
	for _, f := range il.Fields {
		marked := strings.Contains(f.Desc, "v3.0+: reads 0 in v2.2")
		if (f.Name == "TSRC") == marked {
			t.Errorf("IL_CTRL.%s: v2.2 mark %t", f.Name, marked)
		}
	}
	// field bit positions that the fabric and the app both hard-wire
	bits := map[string][2]uint{"IL_CTRL.TSRC": {3, 2}, "IL_CTRL.REDUCE_MODE": {6, 4}, "IL_CTRL.IL_RATE": {7, 7}, "IL_CTRL.DIV_PH": {12, 8},
		"POP_MON.MIN_GAP": {7, 0}, "POP_MON.UNDERRUN": {15, 8}, "DRAIN_START.IDX": {14, 0}, "STACK_CTRL.HOLDOFF": {15, 4},
		"STACK_CNT.PHASE_LAST": {13, 11}, "STATUS_B.STEP_BUSY": {7, 4}, "EYE_CTRL.CENTRE": {11, 4}, "DBG2.CSEL": {10, 8}, "RUN.REDUCE": {7, 7}}
	for k, hl := range bits {
		rn, fn, _ := strings.Cut(k, ".")
		r, _ := i.Reg(rn)
		found := false
		for _, f := range r.Fields {
			if f.Name == fn {
				found = true
				if f.Hi != hl[0] || f.Lo != hl[1] {
					t.Errorf("%s: [%d:%d], want [%d:%d]", k, f.Hi, f.Lo, hl[0], hl[1])
				}
			}
		}
		if !found {
			t.Errorf("%s: missing", k)
		}
	}
	// enums the Go word models depend on
	tsrc := map[string]uint16{"ADC": 0, "RAMP": 1, "COLTAG": 2, "GLITCH": 3}
	reduce := map[string]uint16{"RAW": 0, "DECIM": 1, "PEAK": 2, "BOXCAR8": 3, "BOXCAR16": 4}
	for _, f := range il.Fields {
		var want map[string]uint16
		switch f.Name {
		case "TSRC":
			want = tsrc
		case "REDUCE_MODE":
			want = reduce
		default:
			continue
		}
		if len(f.Enum) != len(want) {
			t.Errorf("IL_CTRL.%s: %d enum values, want %d", f.Name, len(f.Enum), len(want))
		}
		for _, e := range f.Enum {
			if v, ok := want[e.Name]; !ok || v != e.Value {
				t.Errorf("IL_CTRL.%s.%s = %d, want %d", f.Name, e.Name, e.Value, v)
			}
		}
	}
	ops := map[string]uint16{"RESET": 0, "GO": 1, "HALT": 2, "REWIND": 3, "STACK_CLEAR": 4}
	if len(i.Opcodes) != len(ops) {
		t.Errorf("%d opcodes, want %d", len(i.Opcodes), len(ops))
	}
	for _, o := range i.Opcodes {
		if v, ok := ops[o.Name]; !ok || v != o.Value {
			t.Errorf("opcode %s=0x%04x not in the plan", o.Name, o.Value)
		}
	}
	if run, _ := i.Reg("RUN"); len(run.Fields) != 6 || run.Fields[5].Name != "REDUCE" {
		t.Errorf("RUN fields: %+v", run.Fields)
	}
	if v, _ := i.Reg("VERSION"); v.Const != 0x00A3 {
		t.Errorf("VERSION reads 0x%04x", v.Const)
	}
}

// The DIAG window of 05-WORKPLAN §2.1 with the 06-TIERS §2 changes.
// TRLC-LINKS: REQ-SDS-156
func TestDefaultDiagWindow(t *testing.T) {
	type entry struct {
		idx   uint8
		count uint
		acc   schema.Access
		stub  bool
	}
	want := map[string]entry{
		"BUS_OE_LO": {0x00, 1, schema.RW, false}, "BUS_OE_HI": {0x01, 1, schema.RW, false},
		"BUS_DRV_LO": {0x02, 1, schema.RW, false}, "BUS_DRV_HI": {0x03, 1, schema.RW, false},
		"BUS_RD_LO": {0x04, 1, schema.R, false}, "BUS_RD_HI": {0x05, 1, schema.R, false},
		"SINGLE_CTRL": {0x06, 1, schema.RW, false}, "ADC_HOLD": {0x07, 1, schema.RW, false},
		"LANE_IDX": {0x08, 1, schema.RW, false}, "LANE_TOG": {0x09, 1, schema.R, false}, "LANE_LVL": {0x0a, 1, schema.R, false},
		"SNAP_MODE": {0x0b, 1, schema.RW, false},
		"PAIR_TRIM": {0x0c, 1, schema.RW, true}, "IL_DLY": {0x0d, 1, schema.RW, true}, "SNAP_CTRL2": {0x0e, 1, schema.RW, true},
		"LANEMAP": {0x10, 80, schema.R, false},
		"CAL_OFF": {0x60, 10, schema.RW, true}, "CAL_GAIN": {0x6a, 10, schema.RW, true},
		"PHASE_CNT": {0x74, 8, schema.R, true}, "CAL_STAT": {0x7c, 1, schema.R, true}, "CAP_SEL": {0x7d, 5, schema.R, true},
		"BUSGEN_CTL": {0x82, 1, schema.RW, false}, "BUSGEN_PAT": {0x83, 10, schema.RW, false},
		"BUSGEN_AUX": {0x8d, 1, schema.RW, false}, "BUSCAP_CTL": {0x8e, 1, schema.RW, false},
		"BUSGEN_OE_LO": {0x8f, 1, schema.RW, false}, "BUSGEN_OE_HI": {0x90, 1, schema.RW, false},
		"SRCLK_CTRL": {0x91, 1, schema.RW, false}, "SRCLK_N": {0x92, 1, schema.RW, false},
		"SRCLK_DLY": {0x93, 1, schema.RW, false}, "SRCLK_ARM": {0x94, 1, schema.RW, false},
		"SRCLK_STAT": {0x95, 1, schema.R, false}, "SRCLK_CNT_F2": {0x96, 1, schema.R, false},
		"SRCLK_CNT_J2": {0x97, 1, schema.R, false}, "SRCLK_LIFE": {0x98, 1, schema.R, false},
		"SRCLK_DEP": {0x99, 1, schema.R, false},
		"SRCLK_CNT_D1": {0x9a, 1, schema.R, false}, "SRCLK_ARR_D1": {0x9b, 1, schema.R, false},
		"SRCLK_ARR_P6": {0x9c, 1, schema.R, false}, "SRCLK_W": {0x9d, 1, schema.RW, false}, "BUSGEN_PRE": {0x9e, 1, schema.RW, false},
	}
	i := Default()
	if len(i.Diag) != len(want) {
		t.Errorf("%d DIAG entries, want %d", len(i.Diag), len(want))
	}
	for _, d := range i.Diag {
		w, ok := want[d.Name]
		if !ok {
			t.Errorf("diag %s: not in the plan", d.Name)
			continue
		}
		if d.Idx != w.idx || d.Count != w.count || d.Access != w.acc || d.Stub != w.stub {
			t.Errorf("diag %s: 0x%02x x%d %s stub %t, want 0x%02x x%d %s stub %t", d.Name, d.Idx, d.Count, d.Access, d.Stub, w.idx, w.count, w.acc, w.stub)
		}
		if d.Stub && !strings.Contains(d.Desc, "v3.0+: reads 0 in v2.2") {
			t.Errorf("diag %s: a stub must say so in its Desc", d.Name)
		}
	}
	if d, _ := i.DiagEntry("CAP_SEL"); d.Last() != 0x81 {
		t.Errorf("CAP_SEL ends at 0x%02x, want 0x81", d.Last())
	}
	if d, _ := i.DiagEntry("LANEMAP"); d.Last() != 0x5f {
		t.Errorf("LANEMAP ends at 0x%02x, want 0x5f", d.Last())
	}
	if i.DiagReserved != 0x9f {
		t.Errorf("DIAG reserved base 0x%02x, plan says 0x9f", i.DiagReserved)
	}
	if _, ok := i.DiagEntry("PHASE_SEL"); ok {
		t.Error("PHASE_SEL is retired in v3 (PAIR_TRIM at 0x0c)")
	}
}
