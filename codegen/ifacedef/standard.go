// Package ifacedef holds the owned FPGA<->app interface as data. Standard() is
// the one acquisition interface this project ships: the register map, result
// channels, DMA descriptor, and the four frozen contracts (stream, capture /
// pre-trigger, channel overflow, access-semantics), written from the behavioral
// spec (specs/03) — never from vendor code.
//
// There is exactly one owned interface; versions grow INSIDE its reserved blocks
// (0x80-0xdf on CS1) rather than as a second map. Selectors are OUR choice and
// deliberately do not echo any vendor layout. The Version field is a schema
// string; a fabric/app mismatch is a build-ID rejection, not a mode.
package ifacedef

import "open-sds/codegen/schema"

func u16(v uint16) *uint16 { return &v }

// Standard returns the owned acquisition interface schema (DESIGN §3.2).
func Standard() schema.Interface {
	const (
		S = schema.SemNormal
	)
	return schema.Interface{
		Name:      "sds1000cml-iface",
		Version:   "1",
		BuildIDLo: "BUILDID_LO",
		BuildIDHi: "BUILDID_HI",

		// C1 — canonical stream: 18-bit lane (headroom), 2 bypassable transform
		// stages (stage 0 = the live decimator §4.2, stage 1 = reserved identity).
		Stream: schema.Stream{
			SampleLaneBits:  18,
			TransformStages: 2,
			Desc:            "{ch1,ch2,valid,idx,trig_mark}; >=18-bit lane; stage 0 = programmable decimator, stage 1 = reserved identity slot for v3 (deglitch/ERES/math)",
		},
		// C2 — capture: circular programmable pre/post-trigger + trig_mark, with the
		// geometry (depth/addr/margin) that the RTL circular writer also derives.
		Capture: schema.Capture{
			RecordDepth:         20480,
			AddrBits:            15, // 2^15 = 32768 >= 20480
			Margin:              2,  // registered-write pipeline tail; PRETRIG_MAX = 20478
			PreTrigProgrammable: true,
			TrigMark:            true,
			Desc:                "circular writer, programmable pre/post depth, trig_mark; exact window pre+post <= REC_DEPTH-MARGIN (DESIGN §4.4)",
		},

		Blocks: []schema.Block{
			// ================= CS1 (acquisition / read plane) =================

			// ---- meta 0x10-0x17 -----------------------------------------
			{Name: "meta", Plane: schema.CS1, Base: 0x10, Span: 0x08,
				Desc: "identity / build-ID handshake",
				Regs: []schema.Register{
					{Name: "BUILDID_LO", Sel: 0x10, Plane: schema.CS1, Access: schema.R, Sem: S,
						Desc: "low 16 bits of the schema build-ID (app checks vs compiled-in iface.BuildID)"},
					{Name: "BUILDID_HI", Sel: 0x11, Plane: schema.CS1, Access: schema.R, Sem: S,
						Desc: "high 16 bits of the schema build-ID"},
					{Name: "VERSION", Sel: 0x12, Plane: schema.CS1, Access: schema.R, Sem: S, Expect: u16(0x0052),
						Desc: "fabric self-check magic (a cheap addressing sanity check the app already performs)"},
				}},

			// ---- capture 0x20-0x2f --------------------------------------
			{Name: "capture", Plane: schema.CS1, Base: 0x20, Span: 0x10,
				Desc: "arm / halt / re-arm + stream decimation + programmable pre/post-trigger depth",
				Regs: []schema.Register{
					{Name: "OPCODE", Sel: 0x20, Plane: schema.CS1, Access: schema.W, Sem: schema.SemStrobe,
						Desc: "capture opcode strobe: GO (arm/re-arm) / HALT (freeze) / RESET (idle)"},
					{Name: "RUN", Sel: 0x21, Plane: schema.CS1, Access: schema.RW, Sem: S,
						Fields: []schema.Field{
							{Name: "MODE", Hi: 1, Lo: 0, Desc: "0=auto 1=norm 2=single"},
							{Name: "RUN", Hi: 2, Lo: 2, Desc: "1=running"},
						},
						Desc: "run/mode control"},
					{Name: "DECIM_LO", Sel: 0x22, Plane: schema.CS1, Access: schema.RW, Sem: S,
						Desc: "stream decimation factor, low word (transform stage 0; cap_tick once per DECIM samples, §4.2)"},
					{Name: "DECIM_HI", Sel: 0x23, Plane: schema.CS1, Access: schema.RW, Sem: S,
						Desc: "stream decimation factor, high word"},
					{Name: "PRETRIG_LO", Sel: 0x24, Plane: schema.CS1, Access: schema.RW, Sem: S, Desc: "pre-trigger depth, low word"},
					{Name: "PRETRIG_HI", Sel: 0x25, Plane: schema.CS1, Access: schema.RW, Sem: S, Desc: "pre-trigger depth, high word"},
					{Name: "POSTTRIG_LO", Sel: 0x26, Plane: schema.CS1, Access: schema.RW, Sem: S, Desc: "post-trigger depth, low word"},
					{Name: "POSTTRIG_HI", Sel: 0x27, Plane: schema.CS1, Access: schema.RW, Sem: S, Desc: "post-trigger depth, high word"},
				}},

			// ---- drain 0x30-0x3f ----------------------------------------
			{Name: "drain", Plane: schema.CS1, Base: 0x30, Span: 0x10,
				Desc: "frozen-record readout: a single fixed auto-inc BURST port (1-D DMA source) + remaining",
				Regs: []schema.Register{
					{Name: "BURST", Sel: 0x30, Plane: schema.CS1, Access: schema.R, Sem: schema.SemAutoIncPort | schema.SemReadAfterHalt,
						Desc: "single fixed-address auto-inc raw-record port (reads 0..N-1 in order; hi byte C1, lo byte C2)"},
					{Name: "BURST_REMAIN", Sel: 0x3E, Plane: schema.CS1, Access: schema.R, Sem: schema.SemLevelStatus | schema.SemReadAfterHalt,
						Fields: []schema.Field{
							{Name: "READY", Hi: 15, Lo: 15, Desc: "1=coherent frozen record present"},
							{Name: "REMAIN", Hi: 14, Lo: 0, Desc: "words not yet popped through BURST (flow-controls a self-paced drain)"},
						},
						Desc: "words-remaining / DMA-ready for the single BURST port (live count)"},
				}},

			// ---- status 0x40-0x4f ---------------------------------------
			{Name: "status", Plane: schema.CS1, Base: 0x40, Span: 0x10,
				Desc: "clean level acquisition status + interpolating trigger position + fill",
				Regs: []schema.Register{
					{Name: "STATUS_A", Sel: 0x41, Plane: schema.CS1, Access: schema.R, Sem: schema.SemLevelStatus,
						Fields: []schema.Field{
							{Name: "VALID", Hi: 0, Lo: 0, Desc: "a coherent record is present"},
							{Name: "TRIG", Hi: 1, Lo: 1, Desc: "a comparator crossing was accepted this frame"},
							{Name: "DONE", Hi: 2, Lo: 2, Desc: "post-trigger record complete and drain open (means done — gate on it directly)"},
						},
						Desc: "primary status (clean live level)"},
					{Name: "TRIGPOS_LO", Sel: 0x42, Plane: schema.CS1, Access: schema.R, Sem: schema.SemLevelStatus | schema.SemReadAfterHalt,
						Fields: []schema.Field{
							{Name: "FRAC", Hi: 15, Lo: 0, Desc: "sub-sample interpolation fraction (Q16): frac=(lvl-s[k-1])/(s[k]-s[k-1])"},
						},
						Desc: "interpolating trigger position, fractional word (§4.3)"},
					{Name: "TRIGPOS_HI", Sel: 0x43, Plane: schema.CS1, Access: schema.R, Sem: schema.SemLevelStatus | schema.SemReadAfterHalt,
						Fields: []schema.Field{
							{Name: "IDX", Hi: 14, Lo: 0, Desc: "physical mem index of the trigger sample (locates the trigger in the drained array)"},
						},
						Desc: "interpolating trigger position, physical-index word (§4.3)"},
					{Name: "FILL", Sel: 0x44, Plane: schema.CS1, Access: schema.R, Sem: schema.SemLevelStatus,
						Fields: []schema.Field{{Name: "COUNT", Hi: 10, Lo: 0, Desc: "fill counter (11-bit); frozen after halt (coherence telemetry)"}},
						Desc: "fill progress"},
				}},

			// ---- spine 0x50-0x5f ----------------------------------------
			{Name: "spine", Plane: schema.CS1, Base: 0x50, Span: 0x10,
				Desc: "streaming-spine control: transform-stage bypass + envelope config",
				Regs: []schema.Register{
					{Name: "XFORM_CTRL", Sel: 0x50, Plane: schema.CS1, Access: schema.RW, Sem: S,
						Fields: []schema.Field{
							{Name: "BYPASS0", Hi: 0, Lo: 0, Desc: "1=bypass transform stage 0 (the decimator)"},
							{Name: "BYPASS1", Hi: 1, Lo: 1, Desc: "1=bypass transform stage 1 (reserved identity slot)"},
						},
						Desc: "in-line transform-stage bypass"},
					{Name: "ENV_COLS", Sel: 0x51, Plane: schema.CS1, Access: schema.RW, Sem: S,
						Desc: "envelope reducer column count (min/max folded on the live stream, §4.5)"},
				}},

			// ---- channels 0x60-0x7f -------------------------------------
			{Name: "channels", Plane: schema.CS1, Base: 0x60, Span: 0x20,
				Desc: "result/event channel ports (the uniform DATA/COUNT/RESET triad); envelope is the first instance of the reusable result_fifo contract",
				Regs: []schema.Register{
					{Name: "ENV_DATA", Sel: 0x60, Plane: schema.CS1, Access: schema.R, Sem: schema.SemAutoIncPort | schema.SemReadAfterHalt,
						Desc: "envelope channel DATA: successive 16-bit words of packed records (pops one word/read)"},
					{Name: "ENV_COUNT", Sel: 0x61, Plane: schema.CS1, Access: schema.R, Sem: schema.SemLevelStatus,
						Fields: []schema.Field{
							{Name: "COUNT", Hi: 14, Lo: 0, Desc: "envelope records available"},
							{Name: "OVERFLOW", Hi: 15, Lo: 15, Desc: "records dropped (ENV_COLS too large for the FIFO); never a silent drop"},
						},
						Desc: "envelope channel COUNT: record count + overflow (level-status)"},
					{Name: "ENV_RESET", Sel: 0x62, Plane: schema.CS1, Access: schema.W, Sem: schema.SemStrobe,
						Desc: "envelope channel RESET: clears the envelope FIFO (strobe)"},
				}},

			// ---- reserved v1/v2 blocks (ranges claimed now, no registers yet) ----
			{Name: "trigger", Plane: schema.CS1, Base: 0x80, Span: 0x20,
				Desc: "RESERVED (v1): HW trigger-discrimination taps — no registers yet"},
			{Name: "measure", Plane: schema.CS1, Base: 0xA0, Span: 0x20,
				Desc: "RESERVED (v1): HW measurement result ports — no registers yet"},
			{Name: "decode", Plane: schema.CS1, Base: 0xC0, Span: 0x20,
				Desc: "RESERVED (v2): serial-decode result ports — no registers yet"},

			// ================= CS3 (config / control plane) =================

			// ---- config 0x00-0x08 (nCONFIG / CONF_DONE, never written) ----
			// NOTE: DESIGN §3.2 groups the LED latch (0x09-0x0b) under "frontend"
			// but states config as 0x00-0x0f and frontend as 0x10-0x3f — those
			// selectors fall in config's range. Resolved by moving the CS3 block
			// boundary to 0x09 so the LED-latch selectors live in the frontend
			// block; every selector stays exactly as §3.2 specifies.
			{Name: "config", Plane: schema.CS3, Base: 0x00, Span: 0x09,
				Desc: "config plane: nCONFIG / CONF_DONE (NEVER written by the app)",
				Regs: []schema.Register{
					{Name: "CONF_DONE", Sel: 0x07, Plane: schema.CS3, Access: schema.R, Sem: schema.SemLevelStatus,
						Fields: []schema.Field{{Name: "DONE", Hi: 7, Lo: 7, Desc: "1=FPGA configured"}},
						Desc: "config-status / nCONFIG port; read-only here (a write collapses the engine)"},
				}},

			// ---- frontend 0x09-0x3f -------------------------------------
			{Name: "frontend", Plane: schema.CS3, Base: 0x09, Span: 0x37,
				Desc: "analog front-end writes: LED latch, per-channel offset DACs, trigger-level DAC (hi write self-latches + loads the serializer)",
				Regs: []schema.Register{
					// LED latch
					{Name: "LED_LO", Sel: 0x09, Plane: schema.CS3, Access: schema.W, Sem: S, Desc: "front-panel LED latch, low byte"},
					{Name: "LED_HI", Sel: 0x0A, Plane: schema.CS3, Access: schema.W, Sem: S, Desc: "front-panel LED latch, high byte"},
					{Name: "LED_STROBE", Sel: 0x0B, Plane: schema.CS3, Access: schema.W, Sem: schema.SemStrobe, Desc: "commit the LED latch (strobe)"},
					// offset DACs (C1/C2 lo/hi)
					{Name: "OFF_C1_LO", Sel: 0x10, Plane: schema.CS3, Access: schema.W, Sem: S, Desc: "channel 1 offset DAC, low byte"},
					{Name: "OFF_C2_LO", Sel: 0x11, Plane: schema.CS3, Access: schema.W, Sem: S, Desc: "channel 2 offset DAC, low byte"},
					{Name: "OFF_C1_HI", Sel: 0x30, Plane: schema.CS3, Access: schema.W, Sem: S, Desc: "channel 1 offset DAC, high byte"},
					{Name: "OFF_C2_HI", Sel: 0x31, Plane: schema.CS3, Access: schema.W, Sem: S, Desc: "channel 2 offset DAC, high byte"},
					// trigger-level DAC (hi write self-latches + loads)
					{Name: "LVL_A_LO", Sel: 0x14, Plane: schema.CS3, Access: schema.W, Sem: S, Desc: "trigger level DAC lane A, low byte"},
					{Name: "LVL_B_LO", Sel: 0x15, Plane: schema.CS3, Access: schema.W, Sem: S, Desc: "trigger level DAC lane B, low byte"},
					{Name: "LVL_A_HI", Sel: 0x34, Plane: schema.CS3, Access: schema.W, Sem: schema.SemStrobe, Desc: "trigger level DAC lane A, high byte — self-latches + loads the serializer"},
					{Name: "LVL_B_HI", Sel: 0x35, Plane: schema.CS3, Access: schema.W, Sem: schema.SemStrobe, Desc: "trigger level DAC lane B, high byte — self-latches + loads the serializer"},
				}},
		},

		// Result/event channels — each carries a mandatory overflow field (C3).
		// The envelope channel is wired (its DATA/COUNT/RESET triad points at the
		// channels block); measure/trig_event/decode_result are defined now so their
		// record layout + overflow policy are inherited, not hand-rolled per bus in
		// v1/v2 (their ports arrive when the reserved blocks gain registers).
		Channels: []schema.Channel{
			{Name: "envelope", RecordBits: 40,
				Desc: "display-ready min/max per time-column (results, not raw); folded on the live stream",
				Fields: []schema.RecField{
					{Name: "col", Bits: 16, Desc: "display column index"},
					{Name: "min", Bits: 8, Desc: "column min sample"},
					{Name: "max", Bits: 8, Desc: "column max sample"},
					{Name: "ch", Bits: 4, Desc: "channel"},
					{Name: "rsvd", Bits: 3},
					{Name: "overflow", Bits: 1, Overflow: true, Desc: "columns dropped since last read"},
				},
				Ports: []schema.ChannelPort{
					{Role: schema.PortData, Reg: "ENV_DATA"},
					{Role: schema.PortCount, Reg: "ENV_COUNT"},
					{Role: schema.PortReset, Reg: "ENV_RESET"},
				}},
			{Name: "measure", RecordBits: 48,
				Desc: "RESERVED (v1): HW measurement results (Vpp/RMS/freq/period/rise/fall/duty)",
				Fields: []schema.RecField{
					{Name: "kind", Bits: 8, Desc: "measurement kind"},
					{Name: "ch", Bits: 4, Desc: "channel"},
					{Name: "rsvd", Bits: 3},
					{Name: "overflow", Bits: 1, Overflow: true, Desc: "results dropped since last read"},
					{Name: "value", Bits: 32, Desc: "measurement value (fixed-point)"},
				}},
			{Name: "trig_event", RecordBits: 48,
				Desc: "RESERVED (v1): trigger-event ring — idx + kind per fired trigger",
				Fields: []schema.RecField{
					{Name: "idx", Bits: 32, Desc: "sample index of the event"},
					{Name: "kind", Bits: 8, Desc: "trigger kind"},
					{Name: "ch", Bits: 4, Desc: "channel"},
					{Name: "rsvd", Bits: 3},
					{Name: "overflow", Bits: 1, Overflow: true, Desc: "events dropped since last read"},
				}},
			{Name: "decode_result", RecordBits: 64,
				Desc: "RESERVED (v2): serial-decode result ring — per-transaction spans",
				Fields: []schema.RecField{
					{Name: "bus", Bits: 4, Desc: "decode bus id"},
					{Name: "flags", Bits: 8, Desc: "start/stop/error flags"},
					{Name: "rsvd", Bits: 3},
					{Name: "overflow", Bits: 1, Overflow: true, Desc: "spans dropped since last read"},
					{Name: "start_idx", Bits: 32, Desc: "sample index of transaction start (retroactive anchor)"},
					{Name: "data", Bits: 16, Desc: "decoded word"},
				}},
		},

		// DMA burst-drain descriptor the app programs (self-paced EDMA / prefetch),
		// draining the single fixed BURST source into DRAM in <=16 KB passes.
		Descriptor: schema.Descriptor{
			Name: "burst_drain",
			Desc: "single fixed-source (BURST sel) -> DRAM, multi-pass",
			Fields: []schema.RecField{
				{Name: "src_sel", Bits: 8, Desc: "source selector (BURST)"},
				{Name: "plane", Bits: 4, Desc: "CS plane"},
				{Name: "rsvd", Bits: 4},
				{Name: "dst_phys", Bits: 32, Desc: "DMA-coherent destination physical addr"},
				{Name: "word_count", Bits: 16, Desc: "16-bit words this pass (<=8192)"},
			},
		},

		// OPCODE strobe payloads — the acquisition command encoding. Owned values
		// (clean-sheet, not the vendor 0xC0/0xC3/0xC8): the app writes these to
		// OPCODE (0x20) and the RTL decodes the same generated macro, so app and
		// fabric can never disagree on a command (spec 03 §5).
		Opcodes: []schema.Opcode{
			{Name: "OP_RESET", Reg: "OPCODE", Value: 0x0000, Desc: "idle the capture FSM"},
			{Name: "OP_GO", Reg: "OPCODE", Value: 0x0001, Desc: "arm / fast re-arm (honored only while RUN)"},
			{Name: "OP_HALT", Reg: "OPCODE", Value: 0x0002, Desc: "freeze the record"},
		},
	}
}
