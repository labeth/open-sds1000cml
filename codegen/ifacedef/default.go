// Package ifacedef holds the register map of the acq2 default image
// (schema sds1000cml-default, version 3) as Go data. It is a transcription of
// the acq2 analysis branch §2 / §2.1 (the v2 registers, which keep their
// 4-aligned selectors) plus the acq2 analysis branch §2 (the v3 additions in the
// slots opened by GPMC A1/A2) — edit those tables and this file together;
// `make drift` in codegen/ then fails until every generated artifact is
// regenerated.
//
// Fabric increments (06-TIERS §7): the map below is the FULL v3 map. What the
// current increment (v2.2 = the v2.1 datapath + the C2-side additions) does
// not implement yet is declared with Stub: true and a Desc that says
// "v3.0+: reads 0 in v2.2"; the generated decode reads 0 there and emits no
// write strobe. Fields inside a partly implemented register carry the same
// mark in their Desc (the fabric masks them). Flip Stub in the increment that
// implements the register — the build-ID moves and the app reloads the fabric.
// ENGMODEL-OWNER-UNIT: FU-CODEGEN-IFACEDEF
package ifacedef

import "open-sds/codegen/schema"

// Default returns the sds1000cml-default v3 interface.
// TRLC-LINKS: REQ-SDS-156, REQ-SDS-031
func Default() schema.Interface {
	const (
		R  = schema.R
		W  = schema.W
		RW = schema.RW
		v2 = 2 // registers of the 32-selector generation (05-WORKPLAN §2)
		v3 = 3 // registers of 06-TIERS §2 (odd slots)
	)
	const stub = "v3.0+: reads 0 in v2.2"
	f := func(name string, hi, lo uint, desc string) schema.Field {
		return schema.Field{Name: name, Hi: hi, Lo: lo, Desc: desc}
	}
	bit := func(name string, b uint, desc string) schema.Field { return f(name, b, b, desc) }
	e := func(name string, v uint16, desc string) schema.EnumValue {
		return schema.EnumValue{Name: name, Value: v, Desc: desc}
	}
	enum := func(fl schema.Field, vals ...schema.EnumValue) schema.Field { fl.Enum = vals; return fl }

	return schema.Interface{
		Name:         "sds1000cml-default",
		Version:      3,
		VersionMagic: 0x00A3,
		FabricID:     0xA2F1,
		// GPMC A1..A7 reach the fabric: A1 = ball B1, A2 = ball A2 (DEFINITIVE,
		// reports/2026-09-05-R0-E1-E2), A3..A7 the five address balls of v2.
		// 128 selectors, selector N at CS1 base + 2N; bit 7 (no A8) is masked.
		SelMask: 0x7f,
		SelDesc: "GPMC A1..A7 reach the fabric (A1 = ball B1, A2 = ball A2, A3..A7 the v2 address balls): 128 selectors, selector N at CS1 byte address 2N; bit 7 of a selector is ignored",
		// The v2 generation decoded A3..A7 only (0x7c, 32 selectors, multiples
		// of 4): every v2 register keeps that selector, v3 registers take the
		// slots in between (06-TIERS §2).
		Legacy: schema.Legacy{Version: v2, SelMask: 0x7c},
		// The vendor arm/halt words: undecoded (read 0, writes ignored) so the
		// E1 experiment can still be replayed against this image.
		Undecoded: []uint8{0x21, 0x57},
		// 40 M9K = 20480 x 16 = 4096 rows x 5 columns; margin 2 = the
		// registered-write pipeline tail (PRETRIG_MAX = 20478). Stack: delay
		// line 512 rows, 18-bit accumulator cells, N <= 1024 records per batch
		// (255 x 1024 < 2^18). 06-TIERS §1.1, §1.3, §1.4.
		Geometry:     schema.Geometry{RecDepth: 20480, AddrW: 15, Margin: 2, RowCols: 5, Rows: 4096, DlineRows: 512, AccBits: 18, StackNMax: 1024},
		BuildIDLoReg: "BUILDID_LO",
		BuildIDHiReg: "BUILDID_HI",
		VersionReg:   "VERSION",
		FabricIDReg:  "FABRIC_ID",
		OpcodeReg:    "OPCODE",
		DiagIdxReg:   "DIAG_IDX",
		DiagDataReg:  "DIAG_DATA",
		DiagReserved: 0x9f,

		Regs: []schema.Register{
			// ---- the v2 generation: 4-aligned selectors, meanings kept (06-TIERS §1.7) ----
			// 0x00 is the GPMC chip-select base: the prefetch/sDMA engine reads it and
			// cannot be pointed at 0x40 (owned-fpga sel_is_burst, hand RTL there —
			// declared here so regeneration cannot lose it).
			{Name: "BURST_ALIAS", Sel: 0x00, Since: v2, Access: R, Pop: true, Source: schema.SrcAlias, AliasOf: "BURST",
				Desc: "same as BURST (DMA/prefetch-engine friendly: the CS1 base address)"},
			{Name: "CLK_STAT", Sel: 0x04, Since: v2, Access: R,
				Fields: []schema.Field{
					bit("PLLA_LOCK", 0, "PLL A (M2 -> the 200 MHz encode/capture phases) locked"),
					bit("PLLB_LOCK", 1, "PLL B (bus clock) locked"),
					f("RATIO", 15, 4, "M2-vs-C2 ratio: M2 edges per 4096 C2 cycles, /16, saturating"),
				},
				Desc: "clock status"},
			{Name: "DIAG_IDX", Sel: 0x08, Since: v2, Access: RW,
				Fields: []schema.Field{f("IDX", 7, 0, "window index (see the DIAG window table)")},
				Desc:   "index into the diagnostic window (DIAG_DATA)"},
			{Name: "DIAG_DATA", Sel: 0x0c, Since: v2, Access: RW,
				Desc: "the 16-bit diagnostic window selected by DIAG_IDX (layout per index in the DIAG window table)"},
			{Name: "BUILDID_LO", Sel: 0x10, Since: v2, Access: R, Source: schema.SrcBuildIDLo,
				Desc: "schema build-ID low word (from codegen; the app checks it against iface.BuildID)"},
			{Name: "BUILDID_HI", Sel: 0x14, Since: v2, Access: R, Source: schema.SrcBuildIDHi,
				Desc: "schema build-ID high word"},
			{Name: "VERSION", Sel: 0x18, Since: v2, Access: R, Source: schema.SrcConst, Const: 0x00A3,
				Desc: "fabric self-check magic 0x00A3 (schema version 3)"},
			{Name: "FABRIC_ID", Sel: 0x1c, Since: v2, Access: R, Source: schema.SrcConst, Const: 0xA2F1,
				Desc: "0xA2F1 = the default image"},
			{Name: "OPCODE", Sel: 0x20, Since: v2, Access: W, Strobe: true,
				Desc: "capture opcode strobe: RESET / GO (arm or re-arm, only while RUN) / HALT / REWIND / STACK_CLEAR; any other value is ignored"},
			{Name: "RUN", Sel: 0x24, Since: v2, Access: RW,
				Fields: []schema.Field{
					enum(f("MODE", 1, 0, "0=auto 1=norm"), e("AUTO", 0, "auto trigger"), e("NORM", 1, "normal trigger")),
					bit("RUN", 2, "1=running (GO is accepted only while set)"),
					bit("STREAM", 3, "1=gapless ring: the record never finalizes, the drain chases the write pointer"),
					enum(f("CHMODE", 5, 4, "0=dual 20480x2, 1=CH1 only 40960, 2=CH2 only 40960"),
						e("DUAL", 0, "both channels, word = {CH1, CH2}"), e("CH1", 1, "CH1 only, word = two consecutive CH1 samples"), e("CH2", 2, "CH2 only, word = two consecutive CH2 samples")),
					bit("STACK", 6, "stack (accumulate) enable: one batch of STACK_N records per GO, readout through BURST (06-TIERS §1.4); v3.0+ (stack, v3.1): reads 0 in v2.2"),
					bit("REDUCE", 7, "reduce unit on (IL_CTRL.REDUCE_MODE; mode 0 with REDUCE=1 is RAW) (06-TIERS §1.5); v3.0+ (reduce, v3.2): reads 0 in v2.2"),
				},
				Desc: "run / mode control"},
			{Name: "DECIM_LO", Sel: 0x28, Since: v2, Access: RW, Desc: "decimation factor D, low word (cap_tick once per D samples; the reduce unit's block length)"},
			{Name: "DECIM_HI", Sel: 0x2c, Since: v2, Access: RW, Desc: "decimation factor, high word"},
			{Name: "PRETRIG_LO", Sel: 0x30, Since: v2, Access: RW, Desc: "pre-trigger samples (words), low word (<= PRETRIG_MAX; IL5 modes use multiples of ROW_COLS)"},
			{Name: "PRETRIG_HI", Sel: 0x34, Since: v2, Access: RW, Desc: "pre-trigger samples, high word"},
			{Name: "POSTTRIG_LO", Sel: 0x38, Since: v2, Access: RW, Desc: "post-trigger samples (words), low word"},
			{Name: "POSTTRIG_HI", Sel: 0x3c, Since: v2, Access: RW, Desc: "post-trigger samples, high word"},
			{Name: "BURST", Sel: 0x40, Since: v2, Access: R, Pop: true,
				Fields: []schema.Field{
					f("CH1", 15, 8, "CH1 sample (dual mode) or the first of two consecutive samples (single-channel mode); stack readout: (sum >> STACK_CTRL.SHIFT)[15:8]"),
					f("CH2", 7, 0, "CH2 sample (dual mode) or the second consecutive sample (single-channel mode); stack readout: (sum >> STACK_CTRL.SHIFT)[7:0]"),
				},
				Desc: "pop-on-read record word w = ROW_COLS*row + col of the drain window (row-major, column-minor, time order in every mode); a pop on an empty source returns the last word and counts POP_MON.UNDERRUN"},
			{Name: "BURST_REMAIN", Sel: 0x44, Since: v2, Access: R,
				Fields: []schema.Field{
					bit("READY", 15, "1=a record (or a stack batch) is available for drain"),
					f("REMAIN", 14, 0, "words remaining to pop through BURST within the drain window (stream: words available)"),
				},
				Desc: "drain flow control"},
			{Name: "STATUS_A", Sel: 0x48, Since: v2, Access: R,
				Fields: []schema.Field{
					bit("VALID", 0, "a coherent record is present"),
					bit("TRIG", 1, "a trigger was accepted this record"),
					bit("DONE", 2, "post-trigger record complete (stack: the batch is complete, mirrors STATUS_B.STACK_DONE)"),
					bit("OVERFLOW", 3, "stream ring overflowed (drain fell behind); sticky until GO"),
					bit("ARMED", 4, "armed, waiting for a trigger"),
				},
				Desc: "acquisition status (level)"},
			{Name: "TRIGPOS_LO", Sel: 0x4c, Since: v2, Access: R,
				Fields: []schema.Field{f("FRAC", 15, 0, "sub-sample trigger position, Q16 fraction (stack: the top log2 F bits select the phase bin)")},
				Desc:   "trigger position, fractional word"},
			{Name: "TRIGPOS_HI", Sel: 0x50, Since: v2, Access: R,
				Fields: []schema.Field{f("IDX", 14, 0, "record index (word) of the trigger sample (ADDR_W bits); as built bit 15 = trig_half in single-channel mode")},
				Desc:   "trigger position, index word"},
			{Name: "FILL", Sel: 0x54, Since: v2, Access: R,
				Fields: []schema.Field{f("COUNT", 15, 0, "low 16 bits of wrote_count (stream: the live write pointer)")},
				Desc:   "fill count"},
			{Name: "ACQ_CTRL", Sel: 0x58, Since: v2, Access: RW,
				Fields: []schema.Field{
					bit("ENC_EN", 0, "encode clock enable (all five pairs; 0 = converters static)"),
					enum(f("ENC_RATE", 2, 1, "encode rate: 0=12.5 MHz 1=50 MHz 2=100 MHz (IL5-100) 3=200 MHz (IL5-200)"),
						e("R12M5", 0, "12.5 MHz"), e("R50M", 1, "50 MHz"), e("R100M", 2, "100 MHz, the IL5-100 tier"), e("R200M", 3, "200 MHz, the IL5-200 over-clock tier")),
					bit("IL_EN", 3, "interleave enable; v2.2: stored, no effect on the dual-E1 datapath; v3.0+: a read-only mirror of IL_CTRL.IL_EN"),
					f("TRIG_HYST", 7, 4, "software trigger hysteresis in sample codes (0..15) around TRIG_LEVEL"),
					bit("TRIG_SRC", 8, "trigger source: 0=CH1 1=CH2"),
					bit("TRIG_SLOPE", 9, "trigger slope: 0=rising 1=falling"),
					f("PAIR_EN", 14, 10, "per-pair encode enable E1..E5 (bit 10 = E1); ANDed with ENC_EN (freeze probe)"),
				},
				Desc: "acquisition control (reset 0x7c00: encode off, all pairs enabled)"},
			{Name: "TRIG_LEVEL", Sel: 0x5c, Since: v2, Access: RW,
				Fields: []schema.Field{
					f("LEVEL", 7, 0, "trigger level in sample codes"),
					bit("HW_SEL", 8, "1=use the A12 hardware comparator instead of the software compare"),
				},
				Desc: "trigger level / comparator select"},
			{Name: "SNOOP_SEL", Sel: 0x60, Since: v2, Access: R,
				Fields: []schema.Field{
					f("SEL", 6, 0, "the full 7-bit selector (A7..A1) of the last CS1 write"),
					bit("A2", 7, "A2 pad (= GPMC A2 = SEL[1]) level sampled at that write"),
					bit("B1", 8, "B1 pad (= GPMC A1 = SEL[0]) level sampled at that write"),
					f("COUNT", 15, 9, "CS1 write counter (wraps)"),
				},
				Desc: "last CS1 write, selector side"},
			{Name: "SNOOP_DATA", Sel: 0x64, Since: v2, Access: R, Desc: "data word of the last CS1 write"},
			{Name: "DIAG_CTRL", Sel: 0x68, Since: v2, Access: RW,
				Fields: []schema.Field{
					bit("D2", 0, "D2 (nCSO) level; reset 0 (vendor idle posture)"),
					bit("K2_EN", 1, "K2 50 MHz clock forward enable; reset 1"),
					bit("BUS_DRV_EN", 2, "27-ball bus drive master enable (ANDs every DIAG BUS_OE bit); reset 0"),
					f("F2_SEL", 4, 3, "F2 output: 0 = 0 (reset), 1 = 1; codes 2 (clk50) and 3 (clk100) are RETIRED and decode as 0 — they were the only free-running clock sources on an SRAM-clock candidate in the image, reachable from one 16-bit write, and every edge on F2/J2 now goes through the counted emitter (DIAG SRCLK_*)"),
					f("J2_SEL", 6, 5, "J2 output: 0 = 0 (reset), 1 = 1; codes 2 and 3 RETIRED, see F2_SEL"),
					bit("A11", 7, "A11 output level; reset 0"),
					bit("F1", 8, "F1 output level; reset 1"),
					bit("G1", 9, "G1 output level; reset 0"),
					bit("G2", 10, "G2 output level; reset 0"),
					bit("K1", 11, "K1 output level; reset 0"),
					bit("LANE_GATE_RST", 12, "reset the lane ever-1/ever-0 flags (LANE_LVL)"),
					f("SNAP_CLK", 14, 13, "snapshot clock select (v2.2: the 2048x16 snapshot RAM; retired in v3.0 with the snapshot on the delay line, reads 0 from then)"),
					bit("SNAP_ARM", 15, "arm the snapshot"),
				},
				Desc: "diagnostic output controls (reset 0x0102)"},
			{Name: "SNAP_REMAIN", Sel: 0x6c, Since: v2, Access: R,
				Fields: []schema.Field{
					bit("READY", 15, "1=snapshot complete"),
					f("REMAIN", 14, 0, "snapshot words remaining to pop"),
				},
				Desc: "snapshot drain flow control"},
			{Name: "SNAP_POP", Sel: 0x70, Since: v2, Access: R, Pop: true, Desc: "snapshot word (pop-on-read; v3.0+: five words per delay-line row, SNAP_CTRL2)"},
			{Name: "MISC_RD", Sel: 0x74, Since: v2, Access: R,
				Fields: []schema.Field{
					bit("P6", 0, "P6 input (mirrors D2 through the MAX V)"),
					bit("F3", 1, "F3 input level"), bit("F5", 2, "F5 input level"), bit("G5", 3, "G5 input level"),
					bit("D3", 4, "D3 input level"), bit("F7", 5, "F7 input level"),
					bit("A2", 6, "A2 input level (GPMC A2)"), bit("B1", 7, "B1 input level (GPMC A1)"), bit("D1", 8, "D1 input level"),
					bit("G2", 9, "the G2 level DIAG_CTRL asks for (G2 is a DDIO output, so the pad itself cannot be read back; equal to the pad whenever BUSGEN_AUX.G2 is 0)"), bit("B10", 10, "B10 (nWE) level"), bit("E6", 11, "E6 (nOE) level"),
				},
				Desc: "miscellaneous pad readback"},
			{Name: "ENV_DATA", Sel: 0x78, Since: v2, Access: R, Pop: true,
				Desc: "reserved (the stack readout goes through BURST); reads 0"},
			{Name: "DBG", Sel: 0x7c, Since: v2, Access: R, Desc: "fabric FSM state for bring-up (layout per fpga/default/README.md)"},

			// ---- the v3 additions (06-TIERS §2): odd slots; Stub = not in v2.2 ----
			{Name: "IL_CTRL", Sel: 0x25, Since: v3, Access: RW,
				Fields: []schema.Field{
					bit("IL_EN", 0, "interleave on (needs ENC_RATE = 2 + IL_RATE, else STATUS_B.IL_ACTIVE = 0); "+stub),
					bit("CAL_EN", 1, "fabric per-core offset/gain (CAL_OFF/CAL_GAIN) on; "+stub),
					enum(f("TSRC", 3, 2, "test source substituted for the record word at the writer input: 0 ADC, 1 RAMP (word = k & 0xffff, +1 per word), 2 COLTAG ({col, row[7:0]} = {k mod ROW_COLS, (k / ROW_COLS) & 0xff}), 3 GLITCH (0xffff when k mod 512 == 0, else 0x0000); k = the writer's word index since GO (wraps with the ring in stream mode); the iface Go models (ModelTsrc) are the reference"),
						e("ADC", 0, "the converters"), e("RAMP", 1, "ramp, +1 per word"), e("COLTAG", 2, "column tag {col, row[7:0]}"), e("GLITCH", 3, "0xffff in one word of every 512, else 0")),
					enum(f("REDUCE_MODE", 6, 4, "reduce unit mode (RUN.REDUCE): 0 RAW, 1 DECIM pick, 2 PEAK {min,max}, 3 BOXCAR8, 4 BOXCAR16 (06-TIERS §1.5); "+stub),
						e("RAW", 0, "every sample"), e("DECIM", 1, "{CH1[kD], CH2[kD]}"), e("PEAK", 2, "{min1,min2} then {max1,max2} per D"), e("BOXCAR8", 3, "{mean1, mean2} rounded to 8 bits per D"), e("BOXCAR16", 4, "{mean1 Q8.8} then {mean2 Q8.8} per D")),
					enum(bit("IL_RATE", 7, "rate tier: 0 = IL5-100 (100 MHz encode, 2 ns, one row per two c0 cycles, column order E1,E3,E5,E2,E4; the reset value), 1 = IL5-200 (200 MHz, 1 ns, one row per cycle, column order E1..E5); "+stub),
						e("IL5_100", 0, "500 MSa/s per channel"), e("IL5_200", 1, "1 GSa/s per channel (over-clock)")),
					f("DIV_PH", 12, 8, "per-pair divider start phase for IL5-100: bit 8+k = pair E(k+1) one c_k cycle (5 ns) later; reset 0x0A00 (E2, E4) = the 2 ns lattice; ignored at IL5-200; change only with ENC_EN = 0; "+stub),
				},
				Desc: "interleave / test-source / reduce control; v2.2 implements TSRC on the dual-E1 datapath, the other fields read 0 (v3.0+)"},
			{Name: "DEC_RECIP", Sel: 0x26, Since: v3, Access: RW, Stub: true,
				Fields: []schema.Field{f("RECIP", 15, 0, "round(2^(16+DEC_PRE) / D), D = 2..65536 (iface.BoxcarParams; equals the design's round(2^16 / (D >> DEC_PRE)) wherever that is exact)")},
				Desc:   "boxcar reciprocal: mean_Q8.8 = ((sum >> DEC_PRE) * RECIP) >> 8 (06-TIERS §1.5); " + stub},
			{Name: "DEC_PRE", Sel: 0x27, Since: v3, Access: RW, Stub: true,
				Fields: []schema.Field{f("PRE", 4, 0, "max(0, ceil(log2 D) - 8)")},
				Desc:   "boxcar pre-shift; " + stub},
			{Name: "DRAIN_START", Sel: 0x41, Since: v3, Access: RW,
				Fields: []schema.Field{f("IDX", 14, 0, "first logical word of the drain window (0 = the record start)")},
				Desc:   "drain window start; latched at DONE and at OP_REWIND"},
			{Name: "DRAIN_LEN", Sel: 0x42, Since: v3, Access: RW,
				Fields: []schema.Field{f("LEN", 15, 0, "words to drain (0 = to the record end / the full stack readout)")},
				Desc:   "drain window length; latched with DRAIN_START"},
			{Name: "DRAIN_STAT", Sel: 0x43, Since: v3, Access: R,
				Fields: []schema.Field{f("POPS", 15, 0, "BURST/BURST_ALIAS pops since GO / REWIND (wraps)")},
				Desc:   "drain statistics — the pointer-delta ground truth for rung R1"},
			{Name: "POP_MON", Sel: 0x45, Since: v3, Access: R,
				Fields: []schema.Field{
					f("MIN_GAP", 7, 0, "shortest nOE-rise-to-nOE-rise gap between two pops, in clk cycles, since GO (saturating at 255)"),
					f("UNDERRUN", 15, 8, "pops that found no word (the last word was returned), since GO (sticky, saturating at 255)"),
				},
				Desc: "pop monitor"},
			{Name: "STACK_N", Sel: 0x46, Since: v3, Access: RW, Stub: true,
				Fields: []schema.Field{f("N", 10, 0, "records per batch 1..STACK_N_MAX (0 = STACK_N_MAX)")},
				Desc:   "stack batch size; " + stub},
			{Name: "STACK_CTRL", Sel: 0x47, Since: v3, Access: RW, Stub: true,
				Fields: []schema.Field{
					f("PHASE_BINS", 1, 0, "log2 F: phase bins per sample, F in {1,2,4,8}; bins per channel = REC_DEPTH/2/F"),
					f("SHIFT", 3, 2, "readout shift: word = (sum >> SHIFT)[15:0]; 0/1/2 for N <= 256/512/1024"),
					f("HOLDOFF", 15, 4, "trigger holdoff after a window, in rows x 16"),
				},
				Desc: "stack geometry, readout shift, holdoff; " + stub},
			{Name: "STACK_CNT", Sel: 0x49, Since: v3, Access: R, Stub: true,
				Fields: []schema.Field{
					f("RECORDS", 10, 0, "records accumulated so far in the current/last batch"),
					f("PHASE_LAST", 13, 11, "phase bin of the last accepted record"),
				},
				Desc: "stack progress; " + stub},
			{Name: "STACK_PRE", Sel: 0x4a, Since: v3, Access: RW, Stub: true,
				Fields: []schema.Field{f("ROWS", 8, 0, "pre-trigger rows of the stack window (<= DLINE_ROWS - 64 = 448)")},
				Desc:   "stack window pre-trigger; " + stub},
			{Name: "STACK_LEN", Sel: 0x4b, Since: v3, Access: RW, Stub: true,
				Fields: []schema.Field{f("ROWS", 11, 0, "rows per stack window (<= ROWS/2/F)")},
				Desc:   "stack window length; " + stub},
			{Name: "STATUS_B", Sel: 0x4d, Since: v3, Access: R, Stub: true,
				Fields: []schema.Field{
					bit("IL_ACTIVE", 0, "interleave running (IL_EN and the matching ENC_RATE)"),
					bit("STACK_BUSY", 1, "a stack batch is accumulating"),
					bit("STACK_DONE", 2, "the batch is complete (mirrored into STATUS_A.DONE)"),
					bit("FIFO_EMPTY", 3, "the reader FIFO holds no word"),
					f("STEP_BUSY", 7, 4, "phase-step engine busy per counter c1..c4 (bit 4 = c1)"),
					bit("STEP_ERR", 8, "a step without phasedone (sticky)"),
					bit("CAL_SAT", 9, "the CAL stage saturated (any core, sticky per GO)"),
				},
				Desc: "tier status; " + stub},
			{Name: "EYE_CTRL", Sel: 0x4e, Since: v3, Access: RW, Stub: true,
				Fields: []schema.Field{
					f("CORE", 3, 0, "core to watch, 0..9 = 2*pair + ch"),
					f("CENTRE", 11, 4, "expected code"),
					f("TOL", 14, 12, "glitch = |code - CENTRE| > 2^TOL"),
					bit("EN", 15, "eye monitor enable"),
				},
				Desc: "eye monitor control; " + stub},
			{Name: "EYE_CNT", Sel: 0x4f, Since: v3, Access: R, Stub: true,
				Fields: []schema.Field{f("COUNT", 15, 0, "glitches over the last 65536 rows (windowed like LANE_TOG, saturating)")},
				Desc:   "eye monitor count; " + stub},
			{Name: "DBG2", Sel: 0x7d, Since: v3, Access: R, Stub: true,
				Fields: []schema.Field{
					f("STEPS", 7, 0, "phase steps applied on the last stepped counter"),
					f("CSEL", 10, 8, "which counter was stepped last"),
					bit("BUSY", 11, "the step engine is busy"),
					bit("ERR", 12, "the last step reported no phasedone"),
				},
				Desc: "phase-step engine debug; " + stub},
		},

		Opcodes: []schema.Opcode{
			{Name: "RESET", Value: 0x0000, Desc: "return to idle, clear the record"},
			{Name: "GO", Value: 0x0001, Desc: "arm / re-arm (only while RUN.RUN=1); clears DRAIN_STAT and POP_MON"},
			{Name: "HALT", Value: 0x0002, Desc: "freeze the record for drain (stack: end the batch with the partial count)"},
			{Name: "REWIND", Value: 0x0003, Desc: "drain pointer back to DRAIN_START with DRAIN_LEN re-latched, record untouched (the app's re-drain path)"},
			{Name: "STACK_CLEAR", Value: 0x0004, Desc: "zero the stack accumulators and PHASE_CNT without re-arming; v3.0+ (stack, v3.1): ignored in v2.2"},
		},

		// DIAG window (05-WORKPLAN §2.1 + 06-TIERS §2). Ball order for BUS_* bits:
		// 01-CONTRACT §3 row order J6 K5 L1 L2 L3 L4 N1 N2 P1 P2 R1 M6 N6 N3 N5 P3
		// (0..15), R3 R4 R5 R6 R7 T2 T3 T4 T5 T6 T7 (16..26).
		Diag: []schema.DiagEntry{
			{Name: "BUS_OE_LO", Idx: 0x00, Count: 1, Access: RW, Desc: "per-ball output enable, bus balls 0..15 (J6 K5 L1 L2 L3 L4 N1 N2 P1 P2 R1 M6 N6 N3 N5 P3); effective only with DIAG_CTRL.BUS_DRV_EN"},
			{Name: "BUS_OE_HI", Idx: 0x01, Count: 1, Access: RW,
				Fields: []schema.Field{f("OE", 10, 0, "bus balls 16..26 (R3 R4 R5 R6 R7 T2 T3 T4 T5 T6 T7)")},
				Desc:   "per-ball output enable, bus balls 16..26"},
			{Name: "BUS_DRV_LO", Idx: 0x02, Count: 1, Access: RW, Desc: "drive level, bus balls 0..15"},
			{Name: "BUS_DRV_HI", Idx: 0x03, Count: 1, Access: RW,
				Fields: []schema.Field{f("DRV", 10, 0, "bus balls 16..26")},
				Desc:   "drive level, bus balls 16..26"},
			{Name: "BUS_RD_LO", Idx: 0x04, Count: 1, Access: R, Desc: "registered pad input level, bus balls 0..15"},
			{Name: "BUS_RD_HI", Idx: 0x05, Count: 1, Access: R,
				Fields: []schema.Field{f("RD", 10, 0, "bus balls 16..26")},
				Desc:   "registered pad input level, bus balls 16..26"},
			{Name: "SINGLE_CTRL", Idx: 0x06, Count: 1, Access: RW,
				Fields: []schema.Field{
					f("OE", 4, 0, "output enable for the five single bidirectionals F3 F5 G5 D3 F7 (bit 0 = F3)"),
					f("DRV", 12, 8, "drive level for F3 F5 G5 D3 F7 (bit 8 = F3)"),
				},
				Desc: "the five single bidirectional balls"},
			{Name: "ADC_HOLD", Idx: 0x07, Count: 1, Access: RW,
				Fields: []schema.Field{
					bit("L4", 0, "hold L4 static 1 (reset 1); 0 = L4 is a bus member"),
					bit("T2", 1, "hold T2 static 1 (reset 1); 0 = T2 is a bus member"),
					bit("T7", 2, "hold T7 static 1 (reset 1); 0 = T7 is a bus member"),
				},
				Desc: "the proven ADC recipe (owned-fpga c8ec66d) vs bus membership of L4/T2/T7"},
			{Name: "LANE_IDX", Idx: 0x08, Count: 1, Access: RW,
				Fields: []schema.Field{f("IDX", 6, 0, "0x00..0x4f lanes 0..79, 0x50..0x6a bus balls 0..26, 0x70..0x74 singles F3 F5 G5 D3 F7, 0x78 P6, 0x79 A2, 0x7a B1, 0x7b K2, 0x7c B11, 0x7d J1 (listen-only input balls the factory reads; B11 is MAXV-INTERFACE.md s4's only far-end-facing input in the burst-terminator loop)")},
				Desc:   "which input LANE_TOG / LANE_LVL report"},
			{Name: "LANE_TOG", Idx: 0x09, Count: 1, Access: R, Desc: "toggle count of LANE_IDX over the last 65536 bus-clock cycles (saturating)"},
			{Name: "LANE_LVL", Idx: 0x0a, Count: 1, Access: R,
				Fields: []schema.Field{
					bit("LEVEL", 0, "current registered level"),
					bit("EVER1", 1, "seen 1 since LANE_GATE reset"),
					bit("EVER0", 2, "seen 0 since LANE_GATE reset"),
				},
				Desc: "level / ever-1 / ever-0 of LANE_IDX"},
			{Name: "SNAP_MODE", Idx: 0x0b, Count: 1, Access: RW,
				Fields: []schema.Field{f("SLICE", 2, 0, "slice recorded per clock: 0..4 = lanes 0-15..64-79, 5 = bus 0..15, 6 = {bus 16..26, P6, singles}, 7 = 5-word all-lane")},
				Desc:   "snapshot RAM source (v2.2, the 2048x16 snapshot; superseded by SNAP_CTRL2 in v3.0)"},
			// 0x0c replaces v2's PHASE_SEL (the LUT phase muxes and CAP_STEP are
			// retired in v3.0, 06-TIERS §1.7); v2.2 keeps its reset alignment and
			// reads 0 here.
			{Name: "PAIR_TRIM", Idx: 0x0c, Count: 1, Access: RW, Stub: true,
				Fields: []schema.Field{
					f("E2", 3, 0, "pair E2 trim, signed 4-bit, 125 ps steps, saturating at +-7"),
					f("E3", 7, 4, "pair E3 trim"),
					f("E4", 11, 8, "pair E4 trim"),
					f("E5", 15, 12, "pair E5 trim"),
				},
				Desc: "per-pair encode phase trim on counters c1..c4 (E1 = c0 is the reference); a write starts the step engine on the changed counters (STATUS_B.STEP_BUSY, DBG2); replaces PHASE_SEL; " + stub},
			{Name: "IL_DLY", Idx: 0x0d, Count: 1, Access: RW, Stub: true,
				Fields: []schema.Field{
					f("E1", 1, 0, "pair E1 row-alignment delay in c0 cycles"), f("E2", 3, 2, "pair E2"), f("E3", 5, 4, "pair E3"), f("E4", 7, 6, "pair E4"), f("E5", 9, 8, "pair E5"),
				},
				Desc: "per-pair row-alignment delay (reset = the hop-chain latency table; at IL5-100 the LSB selects which of the two captures of a sample is taken); " + stub},
			{Name: "SNAP_CTRL2", Idx: 0x0e, Count: 1, Access: RW, Stub: true,
				Fields: []schema.Field{
					f("ROWS", 8, 0, "rows to capture (<= DLINE_ROWS)"),
					enum(f("SRC", 10, 9, "0 = raw 80 lanes (post-hop, coherent), 1 = assembled core row (10 x 8), 2 = post-CAL row, 3 = {bus 26..0, singles, P6, K2 level, ...} (the E3 bus view)"),
						e("LANES", 0, "raw 80 lanes"), e("CORES", 1, "assembled core row"), e("CAL", 2, "post-CAL row"), e("BUS", 3, "bus view")),
					f("DECIM", 14, 11, "one row per 2^DECIM c0 cycles"),
				},
				Desc: "snapshot on the delay line: SNAP_POP pops ROW_COLS words per row, SNAP_REMAIN counts words; " + stub},
			{Name: "LANEMAP", Idx: 0x10, Count: 80, Access: R,
				Fields: []schema.Field{f("LANE", 6, 0, "source lane (0..79) feeding core (i>>4), bit (i&15) of entry i; core k = pair E(k+1), bits 7..0 CH1, 15..8 CH2")},
				Desc:   "the baked ten-core lane map (fpga/default/lanemap_seed.vh, from lanecal-2026-09-05), entry i = idx-0x10; read-only from v2.2 (a different map is a rebuild); dual mode reads CH1 from entries 0..7, CH2 from 8..15"},
			{Name: "CAL_OFF", Idx: 0x60, Count: 10, Access: RW, Stub: true,
				Fields: []schema.Field{f("OFF", 7, 0, "signed offset in codes, subtracted before the gain")},
				Desc:   "per-core offset, core = 2*pair + ch; y = sat((x - OFF) * GAIN); " + stub},
			{Name: "CAL_GAIN", Idx: 0x6a, Count: 10, Access: RW, Stub: true,
				Fields: []schema.Field{f("GAIN", 8, 0, "Q1.8 gain 0.75..1.996, reset 0x100")},
				Desc:   "per-core gain, core = 2*pair + ch; " + stub},
			{Name: "PHASE_CNT", Idx: 0x74, Count: 8, Access: R, Stub: true,
				Fields: []schema.Field{f("COUNT", 15, 0, "records accumulated into phase bin i = idx-0x74")},
				Desc:   "records per phase bin in the current/last stack batch; " + stub},
			{Name: "CAL_STAT", Idx: 0x7c, Count: 1, Access: R, Stub: true,
				Fields: []schema.Field{f("SAT", 9, 0, "saturation seen per core since GO (bit = core, sticky)")},
				Desc:   "CAL stage saturation; " + stub},
			{Name: "CAP_SEL", Idx: 0x7d, Count: 5, Access: R, Stub: true,
				Fields: []schema.Field{
					f("J", 2, 0, "capture clock of pair k = c((k+J) mod 5)"),
					bit("NEG", 3, "negative-edge capture"),
					f("HOPS", 5, 4, "hop stages into c0"),
				},
				Desc: "the static capture choice per pair k = idx-0x7d (build constant, for the calibration record and the app's sanity check); " + stub},

			// ---- the 27-ball bus probe (the acq2 analysis branch) ----
			//
			// The factory image drives that group as the read side of five M9K
			// blocks: a modulo-5 phase multiplex, a fabric register into the pad
			// output register, one level-style enable for the whole group, and a
			// clock forwarded on K2. Nothing on the die drives an address there and
			// the group is too narrow for the external memory's own protocol, so a
			// replacement reaches that memory only by reproducing this stream for
			// whatever consumes it. BUSGEN emits a programmable version of it and
			// BUSCAP watches the balls at the same rate with a departure trigger,
			// which is the observation E3's static sweeps could not make.
			{Name: "BUSGEN_CTL", Idx: 0x82, Count: 1, Access: RW,
				Fields: []schema.Field{
					bit("EN", 0, "the generator owns the 27 balls (overrides the DIAG bus drive)"),
					f("RATE", 2, 1, "phase clock: 0 = clk50, 1 = clk100, 2 = cap_clk (200 MHz), 3 = clk (C2)"),
					f("NPHASE", 5, 3, "phases per rotation minus 1 (0 = 1 phase .. 4 = the factory's 5)"),
					bit("ONESHOT", 6, "stop after one rotation instead of running free"),
					bit("RUN", 7, "start (write 1; reads back the generator's running state)"),
					f("OE_PH", 12, 8, "output enable per phase, bit k = phase k (0 = release the group)"),
					bit("OE_INV", 13, "invert every OE_PH bit"),
				},
				Desc: "27-ball bus generator: control"},
			{Name: "BUSGEN_PAT", Idx: 0x83, Count: 10, Access: RW,
				Fields: []schema.Field{f("BITS", 15, 0, "phase (idx-0x83)>>1: low word = balls 0..15, high word = balls 16..26")},
				Desc:   "27-ball bus generator: the word driven in each phase, two entries per phase, ball order J6 K5 L1 L2 L3 L4 N1 N2 P1 P2 R1 M6 N6 N3 N5 P3 R3 R4 R5 R6 R7 T2 T3 T4 T5 T6 T7"},
			{Name: "BUSGEN_AUX", Idx: 0x8d, Count: 1, Access: RW,
				Fields: []schema.Field{
					f("K2", 2, 0, "K2 source: 0 = the DIAG_CTRL 50 MHz forward, 1 = 0, 2 = 1, 3 = the phase clock, 4 = phase clock / 2, 5 = one tick per rotation, 6 = the inverse of the generator drive enable (the factory's K1 shape: low while the group is driven), 7 = the drive enable itself"),
					f("D1", 5, 3, "D1 source, same encoding (D1 is an output in the factory image; DIAG drives it constant otherwise)"),
					f("D2", 8, 6, "D2 source, same encoding (0 = the DIAG_CTRL level)"),
					f("G2", 11, 9, "G2 source, same encoding (0 = the DIAG_CTRL level)"),
					f("K1", 14, 12, "K1 source, same encoding (0 = the DIAG_CTRL level). The factory drives K1 from REG:X6Y4M2, whose inverse is the output enable of 24 of the 27 bus balls (the acq2 analysis branch), so code 6 is the posture to reproduce"),
				},
				Desc: "27-ball bus generator: what the four clock-shaped balls carry while EN"},
			{Name: "BUSCAP_CTL", Idx: 0x8e, Count: 1, Access: RW,
				Fields: []schema.Field{
					bit("EN", 0, "the snapshot RAM records the whole 32-bit bus view (two words per sample: balls 0..15, then {singles 3..0, P6, balls 26..16}, both halves from the same bus tick), 1024 samples one every other tick"),
					bit("TRIG", 1, "arm on a departure instead of immediately: the posture is latched at arm and recording starts on the first sample where a ball the generator is not driving differs from it"),
					bit("TRIGGERED", 2, "read-only: the trigger fired in this run"),
					bit("WAITING", 3, "read-only: armed and waiting for the departure"),
					f("CONFIRM", 5, 4, "how many consecutive samples a departure must hold before it counts: 0 = 1 (any single sample), 1 = 2, 2 = 4, 3 = 8. A lone sample is not evidence: with the group switching hard, a floating ball reads low for one sample often enough to fire a 1-sample trigger in 6 runs of 10, and the record then shows it already back at its idle level (the acq2 analysis branch)"),
				},
				Desc: "27-ball bus capture: control and status"},
			{Name: "BUSGEN_OE_LO", Idx: 0x8f, Count: 1, Access: RW,
				Fields: []schema.Field{f("MASK", 15, 0, "bit i enables the generator's drive on bus ball i, balls 0..15 (J6 K5 L1 L2 L3 L4 N1 N2 P1 P2 R1 M6 N6 N3 N5 P3); reset 0xffff")},
				Desc: "27-ball bus generator: per-ball drive mask, low half — ANDed with BUSGEN_CTL.OE_PH so a subset of the group can be driven while the rest is listened to (BUSCAP's departure trigger only watches the balls this mask leaves alone)"},
			{Name: "BUSGEN_OE_HI", Idx: 0x90, Count: 1, Access: RW,
				Fields: []schema.Field{f("MASK", 10, 0, "bit i enables the generator's drive on bus ball 16+i (R3 R4 R5 R6 R7 T2 T3 T4 T5 T6 T7); reset 0x7ff")},
				Desc: "27-ball bus generator: per-ball drive mask, high half"},
			// ---- SRCLK: the counted-edge SRAM-clock crank on F2 / J2 ----
			//
			// F2 and J2 are the two remaining SRAM-clock candidates (K2 is bench-measured at
			// 50.000 MHz and retired). Every null this branch has recorded on that interface was
			// taken at DIAG_CTRL = 0x0102, i.e. with both candidates held statically LOW: a
			// pipelined SyncBurst part with no clock can never present a new word.
			//
			// fpga-specs/22 §2.5.1 (tLZC 1.5 ns min, tHZC 2.6 ns max) says a clock edge can take
			// the SRAM's DQ drivers Low-Z into an AD9288 that has no output enable, and stopping
			// the clock does not undo it — the exit is a power cycle. So there is NO free-running
			// source here: an edge exists only because a keyed arm loaded a finite budget, the
			// budget IS the emitter, and the configuration has a ceiling no register write raises.
			{Name: "SRCLK_CTRL", Idx: 0x91, Count: 1, Access: RW,
				Fields: []schema.Field{
					enum(f("F2_SRC", 1, 0, "what drives ball F2; code 3 decodes as ZERO"),
						e("LEGACY", 0, "the DIAG_CTRL.F2_SEL level (reset; bit-for-bit the pre-crank image)"),
						e("ZERO", 1, "static 0 regardless of DIAG_CTRL"),
						e("CRANK", 2, "the counted-edge emitter; parked static 0 between bursts")),
					enum(f("J2_SRC", 3, 2, "what drives ball J2, same encoding"),
						e("LEGACY", 0, "the DIAG_CTRL.J2_SEL level (reset)"),
						e("ZERO", 1, "static 0"),
						e("CRANK", 2, "the counted-edge emitter")),
					bit("CTRL_G2", 4, "RESERVED AND REFUSED in this build: an arm with this bit set is rejected with REFUSE = 7. G2's DDIO runs on busclk, which can be cap_clk, so routing the clk100 emitter to it creates a 100 -> 200 MHz transfer into the cell's own input flop, measured at -0.735 ns combinational and -1.716 ns registered against a domain with 0.2 ns to spare (the SDC false-paths G2's pad, not that flop). The crosstalk control is J2 instead: a DDIO on clk100, the emitter's own domain, costing no crossing, and being the other SRAM-clock candidate it is the more useful comparison"),
					bit("SNAP_ON_ARM", 5, "RESERVED AND NOT WIRED in this build: setting it does nothing. Coupling the crank into the snapshot arm is the one new path into an existing, timing-critical block, and it put the snapshot arm downstream of an interlock that reads floating inputs — an unknown on the released bus then blocks the arm entirely. The app arms the snapshot itself immediately before arming the crank, and the edge-lock proof is the delay-shift rung, not the pre/post split"),
					enum(f("D1_SRC", 9, 8, "what drives ball D1; code 3 decodes as LEGACY"),
						e("LEGACY", 0, "the BUSGEN_AUX.D1 level (reset; D1 undriven, its acquisition rest posture)"),
						e("ZERO", 1, "static 0 regardless of BUSGEN_AUX"),
						e("CRANK", 2, "the counted-edge emitter, SDR on clk100. D1 is already bidir with a fabric OE, so this costs no pad direction change and no DDIO cell — and it is the only ball on this device with BOTH a pad readback (SRCLK_ARR_D1) and a far-end witness (SRCLK_ARR_P6, since [BENCH] P6 = (NOT D1) OR D2). Refused unless DIV >= 2, which keeps the SDR shape bit-identical to the DDIO one and the narrowest phase 2 samples wide")),
					enum(f("GUARD", 7, 6, "the hardware abort watchdog: which monitored signals abort the burst the moment one moves"),
						e("OFF", 0, "no hardware abort; only permitted with NEDGE = 1"),
						e("LANES", 1, "the 80 ADC lanes (reset)"),
						e("ALL", 2, "the 80 lanes plus the 27 bus balls, 5 singles and P6")),
				},
				Desc: "counted-edge crank: sources, control ball, snapshot coupling and the hardware abort"},
			{Name: "SRCLK_N", Idx: 0x92, Count: 1, Access: RW,
				Fields: []schema.Field{
					f("NEDGE", 9, 0, "pad rising edges this arm will emit, 0..1023. 0 emits nothing. There is no free-run encoding and no wider field: 1023 is the per-arm ceiling because the register has no more bits"),
					f("DIV", 14, 10, "burst shape: f_pad = 100 MHz / 2^DIV (0 = a 5 ns DDIO pulse per clk100 cycle, 1 = 50 MHz, 4 = 6.25 MHz, 7 = 781 kHz). High time is half the period, so DIV varies rate and pulse width together. Latched at arm; it never free-runs"),
				},
				Desc: "counted-edge crank: how many edges, and how fast"},
			{Name: "SRCLK_DLY", Idx: 0x93, Count: 1, Access: RW,
				Fields: []schema.Field{
					f("DLY", 9, 0, "clk100 ticks from the accepted arm (after the guard baseline completes) to the first edge, 0..1023 = 0..10.23 us. With SNAP_ON_ARM the record carries its own baseline. Latched at arm"),
				},
				Desc: "counted-edge crank: arm-to-first-edge delay, so one snapshot holds PRE and POST"},
			{Name: "SRCLK_ARM", Idx: 0x94, Count: 1, Access: RW,
				Fields: []schema.Field{
					f("KEY", 7, 0, "must be 0xA5 in the SAME write, or the whole word is ignored (reads back 0x00a5)"),
					bit("ARM", 8, "write-1 strobe, self-clearing, edge-triggered: load the budget and start. Ignored unless every interlock in SRCLK_STAT.REFUSE holds; a re-arm during a burst is ignored and CANNOT extend a budget"),
					bit("STOP", 9, "write-1 strobe: end the burst now, park both balls at 0, set ABORTED"),
					bit("CLR", 10, "write-1 strobe: clear ABORTED / REFUSED / the departure report. Does NOT clear SRCLK_LIFE — nothing does"),
				},
				Desc: "counted-edge crank: keyed arm / stop / clear strobe (no stored state)"},
			{Name: "SRCLK_STAT", Idx: 0x95, Count: 1, Access: R,
				Fields: []schema.Field{
					f("REMAIN", 9, 0, "edges still owed on this arm"),
					bit("BUSY", 10, "a burst is in flight (guard baseline, delay, or emitting)"),
					bit("ABORTED", 11, "sticky: the guard fired or STOP was written; ARM is refused until CLR"),
					bit("REFUSED", 12, "sticky: the last keyed ARM was rejected by an interlock"),
					f("REFUSE", 15, 13, "why: 0 = accepted; 1 = no CRANK source, or DIAG_CTRL.F2_SEL/J2_SEL not both 0; 2 = the 27-ball group is not in its released-high rest posture; 3 = NEDGE = 0; 4 = busy, or the guard baseline is not ready; 5 = ABORTED is set; 6 = would exceed the lifetime ceiling; 7 = GUARD off with NEDGE > 1, or CTRL_G2 without BUSGEN_CTL.RATE = 1"),
				},
				Desc: "counted-edge crank: status (read while BUSY = 0; the crossing is per-bit)"},
			{Name: "SRCLK_CNT_F2", Idx: 0x96, Count: 1, Access: R,
				Fields: []schema.Field{f("EMITTED", 15, 0, "rising transitions of the data presented to F2's DDIO cell since the last accepted arm, counted in clk100 — the cell's own domain — from the FINAL per-ball mux output. This witnesses EMISSION, not arrival: a DDIO output port may not feed core logic on Cyclone IV, so no register in this image can report a pad level on F2/J2. Saturating")},
				Desc: "counted-edge crank: edges emitted on F2 since arm"},
			{Name: "SRCLK_CNT_J2", Idx: 0x97, Count: 1, Access: R,
				Fields: []schema.Field{f("EMITTED", 15, 0, "as SRCLK_CNT_F2, for J2")},
				Desc: "counted-edge crank: edges emitted on J2 since arm"},
			{Name: "SRCLK_LIFE", Idx: 0x98, Count: 1, Access: R,
				Fields: []schema.Field{f("EDGES", 15, 0, "emitter edges since this fabric was configured, saturating at the build ceiling 4096. An arm is refused unless EDGES + NEDGE <= 4096, so the ceiling is exact. There is no clear path in the fabric: only a reload resets it, which is reboot-recoverable (CRAM only, no flash)")},
				Desc: "counted-edge crank: the whole-configuration edge ceiling"},
			{Name: "SRCLK_DEP", Idx: 0x99, Count: 1, Access: R,
				Fields: []schema.Field{
					f("GRP", 5, 0, "which group moved since the arm: bits 0..4 = lanes 0-15, 16-31, 32-47, 48-63, 64-79; bit 5 = the 27 bus balls / 5 singles / P6. The per-signal truth is already in LANE_IDX + LANE_LVL.EVER0/EVER1 over the same gate window, so this is a 6-bit summary"),
					f("ABORT_EDGE", 15, 6, "the edge index in flight when the guard fired (0 = none)"),
				},
				Desc: "counted-edge crank: the departure report, latched by the hardware abort"},
			{Name: "SRCLK_CNT_D1", Idx: 0x9a, Count: 1, Access: R,
				Fields: []schema.Field{f("EMITTED", 15, 0, "rising transitions of the data presented to D1's IOE output register since the last accepted arm, counted in clk100. EMISSION, exactly as SRCLK_CNT_F2. Saturating")},
				Desc: "counted-edge crank: edges emitted on D1 since arm"},
			{Name: "SRCLK_ARR_D1", Idx: 0x9b, Count: 1, Access: R,
				Fields: []schema.Field{f("ARRIVED", 15, 0, "rising transitions of D1's IOE INPUT register — the PAD NODE of the ball we are driving, sampled in clk100 while we drive it. ARRIVAL AT OUR OWN BALL: a count equal to NEDGE proves the output driver switched and the pad crossed both 3.3-V LVTTL thresholds. It does NOT prove the edge reached anything beyond the ball. Excluded from mon, moved, dep_grp and GUARD by construction. Valid for DIV >= 2 only. Saturating")},
				Desc: "counted-edge crank: edges present on the D1 pad since arm"},
			{Name: "SRCLK_ARR_P6", Idx: 0x9c, Count: 1, Access: R,
				Fields: []schema.Field{f("ARRIVED", 15, 0, "rising transitions of P6, two-flop synchronised into clk100, since the last accepted arm. ARRIVAL AT THE FAR DIE: nothing inside the Cyclone drives P6. [BENCH] P6 = (NOT D1) OR D2, 9/9, so with D1_SRC = CRANK and DIAG_CTRL.D2 = 0 this predicts ARRIVED == NEDGE exactly. With D2 = 1, P6 is constant and this predicts 0 — the negative control on the witness itself. P6 is masked out of moved/dep_grp/GUARD whenever D1_SRC = CRANK, because a D1 crank moves it by construction and would otherwise abort every burst at edge 1 and report a phantom departure. Its per-signal truth stays readable at LANE_IDX 0x78. Saturating")},
				Desc: "counted-edge crank: MAX V responses to D1 seen at P6 since arm"},
			{Name: "SRCLK_W", Idx: 0x9d, Count: 1, Access: RW,
				Fields: []schema.Field{
					f("HIGH", 9, 0, "pulse HIGH time in clk100 ticks (10 ns each), 1..1023, INDEPENDENT of the period. 0 selects the legacy shape where high time is half the period and DIV varies rate and width together. This exists because every corner measured so far is confounded: the D1 -> far end -> P6 loop returns every edge below 48.8 kHz and none above 97.7 kHz, and DIV moved the pulse width and the repetition rate at the same time, so 'the node needs N us to charge after D1 releases it' and 'the far end needs a minimum width to see the pulse' predict the identical table. Holding the period and sweeping HIGH separates them. Refused unless HIGH < the period, and DIV is capped at 23 in width mode so the period counter cannot overflow. Latched at arm"),
				},
				Desc: "counted-edge crank: pulse width, held independent of the period"},
			{Name: "BUSGEN_PRE", Idx: 0x9e, Count: 1, Access: RW,
				Fields: []schema.Field{
					f("PRE", 15, 0, "phase-advance prescaler: the generator advances one phase every PRE+1 bus-clock ticks, so a phase lasts (PRE+1) x 20 ns at RATE 0. 0 is the original behaviour, one phase per tick. This exists because EVERY experiment ever run on the 27-ball group has been fast: the generator's slowest phase was one bus-clock tick, about 20 ns, and its slowest pulse about 100 ns at five phases. The D1 loop then measured the only observable far-end path and found it passes a pulse only if it is wider than about 9.4 us (the acq2 analysis branch). If the bus group's far end filters anything like that, no stimulus this project has ever applied to those balls — 107 departure-armed conditions, the leave-one-out sweeps, roughly 4,300 static postures — was wide enough to be seen. At RATE 0 a prescale of 511 gives a 10.24 us phase, just past that threshold, and 65535 gives 1.31 ms"),
				},
				Desc: "27-ball bus generator: phase-advance prescaler, so the group can be driven slowly"},
		},
	}
}
