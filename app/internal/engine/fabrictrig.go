package engine

// In-fabric decode + trigger host driver (ITEM 3).
//
// This maps the SAME operator config the software serial trigger uses
// (SerialParams, serialtrig.go) onto the owned Cyclone's in-fabric decode +
// 4-mode trigger, and reads its results back — so the FPGA does the decoding and
// the triggering, byte-for-byte agreeing with the software oracle, instead of
// the app pulling every record and decoding on the AM3352.
//
// It is ADDITIVE and schema-invariant. The decode block (acq.v "IN-FABRIC UART
// DECODE") lives on HAND-DECODED spare selectors that are deliberately NOT in
// regs.vh / the generated iface table, so IFACE_BUILD_ID stays 0xc2f6eb5f. Those
// selectors therefore cannot go through the schema-guarded bus.Write (it refuses
// any non-schema selector); the writes go through the narrow bus.WriteSpare
// escape hatch, and the reads through bus.Read (which is unguarded). Everything
// here runs on the engine-owner goroutine only, like every other bus access.
//
// FABRIC REGISTER MAP (CS1, hand-decoded in acq.v; see dec_trigger.v for modes):
//
//	WRITE 0x04 CFG     {en[0], src/chan[1], databits/CPOL/CPHA/MSB[7:2],
//	                    trig_en[8], tg_en[9], proto[11:10], trig_mode[13:12],
//	                    seqlen[15:14]}
//	WRITE 0x08 THR     {thrA[7:0], thrB[15:8]}   (SCL/CLK lane, SDA/DATA lane)
//	WRITE 0x0c SPB_LO  samples-per-bit Q16.8 [15:0]   (UART) / gapReset (SPI)
//	WRITE 0x1c SPB_HI  samples-per-bit Q16.8 [23:16]
//	WRITE 0x48 MATCH   {mask[15:8], pattern[7:0]}  (reinterpreted per trig_mode)
//	WRITE 0x68 TESTGEN {seq2[15:8], seq1[7:0]}     (mode-2 sequence bytes)
//	READ  0x4c STATUS  {ovf[15], matched[14], busy[13], fill[10:0]}
//	READ  0x6c MATCHED {frame_err[9], parity_err[8], matched_byte[7:0]}
//	READ  0x7c BYTE    auto-inc FIFO pop {frame_err[9], parity_err[8], data[7:0]}

import (
	"math"

	"open-sds/app/internal/iface"
)

// Spare (non-schema) decode selectors, hand-decoded in acq.v against hardwired
// literals. WriteSpare permits exactly this set; Read reaches the read side.
const (
	selDecCFG     uint16 = 0x04 // W CFG
	selDecTHR     uint16 = 0x08 // W THR
	selDecSPBLo   uint16 = 0x0c // W SPB[15:0]
	selDecSPBHi   uint16 = 0x1c // W SPB[23:16]
	selDecMATCH   uint16 = 0x48 // W MATCH
	selDecTESTGEN uint16 = 0x68 // W TESTGEN
	selDecSTATUS  uint16 = 0x4c // R STATUS (non-popping)
	selDecMATCHED uint16 = 0x6c // R MATCHED (non-popping)
	selDecBYTE    uint16 = 0x7c // R BYTE (auto-inc FIFO pop)
)

// CFG (0x04) bit layout — mirrors acq.v dec_cfg[*] wiring exactly.
const (
	cfgEn       uint16 = 1 << 0 // dec_en: 0 => decode fully inert (decode-off byte-identical)
	cfgSrcCh    uint16 = 1 << 1 // UART source channel / I2C+SPI channel swap
	cfgBitsSh   uint   = 2      // UART data bits [5:2]; 0 => 8 in uart_decode
	cfgBitsMsk  uint16 = 0xF << 2
	cfgSPICPOL  uint16 = 1 << 2 // SPI clock polarity
	cfgSPICPHA  uint16 = 1 << 3 // SPI clock phase
	cfgSPIMSB   uint16 = 1 << 4 // SPI 1=MSB-first
	cfgParSh    uint   = 6      // UART parity [7:6]: 0 none, 1 even, 2 odd
	cfgParMsk   uint16 = 0x3 << 6
	cfgTrigEn   uint16 = 1 << 8 // dec_trigen: arm the trigger comparator
	cfgTgEn     uint16 = 1 << 9 // testgen inject (NEVER set when arming a real trigger)
	cfgProtoSh  uint   = 10     // proto [11:10]
	cfgProtoMsk uint16 = 0x3 << 10
	cfgModeSh   uint   = 12 // trig_mode [13:12]
	cfgModeMsk  uint16 = 0x3 << 12
	cfgSeqSh    uint   = 14 // seqlen_cfg [15:14]; N = seqlen_cfg+1
	cfgSeqMsk   uint16 = 0x3 << 14
)

// proto codes (CFG[11:10]).
const (
	fabUART = 0
	fabI2C  = 1
	fabSPI  = 2
	fabETH  = 3
)

// trig_mode codes (CFG[13:12]).
const (
	fabTrigByte = 0 // per-module byte comparator (== today's software byte match)
	fabTrigErr  = 1 // frame/parity/NAK/FCS error trigger (software trigger LACKS this)
	fabTrigSeq  = 2 // 2..4 contiguous data-byte sequence
	fabTrigAddr = 3 // I2C address+RW trigger (software trigger LACKS a HW addr arm)
)

// fabricRegs is the decode+trigger register image encoded from a SerialParams.
// SPB is intentionally excluded: it is a timing register derived at arm time
// from the band's capture interval (see fabricSPB), not from the operator's
// protocol/pattern config, so it is not part of the documented bit-layout the
// encoder round-trips.
type fabricRegs struct {
	CFG     uint16
	THR     uint16
	MATCH   uint16
	TESTGEN uint16
	Mode    int // resolved trig_mode (0..3)
	SeqLen  int // resolved N (1..4)
	Proto   int // resolved fabric proto (0..3)
}

// fabricProto maps a serialtrig proto id (serUART/serI2C/serSPI) to the fabric
// CFG proto code, reporting ok=false for protocols the in-fabric decoder does
// not implement (manchester/sent/can/... stay on the software path).
func fabricProto(proto int) (int, bool) {
	switch proto {
	case serUART:
		return fabUART, true
	case serI2C:
		return fabI2C, true
	case serSPI:
		return fabSPI, true
	}
	return 0, false
}

// parityCode maps a SerialParams parity string to the fabric parity_cfg code
// (uart_decode.v: 0 none, 1 even, 2 odd).
func parityCode(parity string) uint16 {
	switch parity {
	case "even":
		return 1
	case "odd":
		return 2
	}
	return 0
}

func clampByte(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

// encodeFabricSerial maps a SerialParams onto the fabric decode+trigger
// registers. ok=false when the protocol is not fabric-decodable (caller keeps
// the software trigger). The trigger MODE is resolved from the same fields the
// software trigger reads, plus the additive ErrTrig flag:
//
//	ErrTrig                         -> mode 1 (ERROR): MATCH.mask = err flags
//	I2C + Addr>=0 + no data bytes   -> mode 3 (ADDR):  MATCH = {addr_mask, addr_field}
//	len(Bytes) >= 2                 -> mode 2 (SEQ):   corrected packing below
//	len(Bytes) == 1                 -> mode 0 (BYTE):  MATCH = {0xFF, byte}
//	otherwise                       -> mode 0 (BYTE):  MATCH = 0 (any decoded byte)
//
// mode-2 CORRECTED sequence packing (matches dec_trigger.v seqv0..seqv3):
//
//	seq0 = MATCH[7:0]   seq1 = TESTGEN[7:0]   seq2 = TESTGEN[15:8]   seq3 = MATCH[15:8]
//
// so N=2 {AA,BB} -> MATCH=0x00AA, TESTGEN=0x00BB;
//
//	N=3 {AA,BB,CC} -> MATCH=0x00AA, TESTGEN=0xCCBB;
//	N=4 {AA,BB,CC,DD} -> MATCH=0xDDAA, TESTGEN=0xCCBB.
func encodeFabricSerial(p SerialParams) (fabricRegs, bool) {
	var r fabricRegs
	proto, ok := fabricProto(p.Proto)
	if !ok {
		return r, false
	}
	r.Proto = proto

	// THR: the fabric has no auto-threshold, so program a fixed slice code on
	// both lanes (default midscale). HaveThr overrides with the operator code.
	thr := 0x80
	if p.HaveThr {
		thr = clampByte(int(math.Round(p.Threshold)))
	}
	r.THR = uint16(thr) | uint16(thr)<<8 // thrA=[7:0], thrB=[15:8]

	// Resolve trigger mode + MATCH/TESTGEN.
	mode := fabTrigByte
	seqlen := 0 // seqlen_cfg = N-1
	var match, testgen uint16
	switch {
	case p.ErrTrig:
		mode = fabTrigErr
		em := clampByte(p.ErrMask)
		if em == 0 {
			em = 0xFF // empty mask would never fire; default to "any error flag"
		}
		match = uint16(em) << 8 // err_mask = MATCH[15:8]
	case proto == fabI2C && p.Addr >= 0 && len(p.Bytes) == 0:
		mode = fabTrigAddr
		// The I2C address symbol on the wire is {addr7, rw} = (addr<<1)|rw. Mask
		// the 7 address bits (0xFE) and, unless RW is "any", bit0 (0x01).
		addrMask, addrField := 0, 0
		addrMask |= 0xFE
		addrField |= (p.Addr & 0x7F) << 1
		if p.RW != 2 {
			addrMask |= 0x01
			addrField |= p.RW & 0x01
		}
		match = uint16(addrMask&0xFF)<<8 | uint16(addrField&0xFF)
	case len(p.Bytes) >= 2:
		mode = fabTrigSeq
		n := len(p.Bytes)
		if n > 4 {
			n = 4 // fabric sequence depth is 4
		}
		seqlen = n - 1
		match = uint16(clampByte(p.Bytes[0]))   // seq0 -> MATCH[7:0]
		testgen = uint16(clampByte(p.Bytes[1])) // seq1 -> TESTGEN[7:0]
		if n >= 3 {
			testgen |= uint16(clampByte(p.Bytes[2])) << 8 // seq2 -> TESTGEN[15:8]
		}
		if n == 4 {
			match |= uint16(clampByte(p.Bytes[3])) << 8 // seq3 -> MATCH[15:8]
		}
	case len(p.Bytes) == 1:
		mode = fabTrigByte
		match = 0xFF00 | uint16(clampByte(p.Bytes[0])) // exact byte match
	default:
		mode = fabTrigByte
		match = 0x0000 // mask=0 => the legacy comparator fires on any decoded byte
	}
	r.Mode = mode
	r.SeqLen = seqlen
	r.MATCH = match
	r.TESTGEN = testgen

	// CFG assembly.
	cfg := cfgEn | cfgTrigEn
	cfg |= uint16(proto) << cfgProtoSh
	cfg |= uint16(mode) << cfgModeSh
	cfg |= uint16(seqlen) << cfgSeqSh
	switch proto {
	case fabUART:
		cfg |= (uint16(p.Bits) << cfgBitsSh) & cfgBitsMsk
		cfg |= (parityCode(p.Parity) << cfgParSh) & cfgParMsk
		if p.ChA == 1 {
			cfg |= cfgSrcCh
		}
	case fabSPI:
		if p.CPOL {
			cfg |= cfgSPICPOL
		}
		if p.CPHA {
			cfg |= cfgSPICPHA
		}
		if p.MSB {
			cfg |= cfgSPIMSB
		}
		if p.ChA == 1 {
			cfg |= cfgSrcCh // SPI channel swap
		}
	case fabI2C:
		if p.ChA == 1 {
			cfg |= cfgSrcCh // I2C channel swap
		}
	}
	r.CFG = cfg
	return r, true
}

// fabricSPB computes the samples-per-bit timing register (Q16.8, 24-bit) from
// the operator baud and the band's capture interval. ok=false when baud is auto
// (0) or the interval is unknown — the fabric has no auto-baud, so a fabric UART
// decode needs an explicit baud (documented). Returned as {lo[15:0], hi[23:16]}.
func fabricSPB(baud int, captureIntervalS float64) (lo, hi uint16, ok bool) {
	if baud <= 0 || !(captureIntervalS > 0) {
		return 0, 0, false
	}
	spb := (1.0 / captureIntervalS) / float64(baud) // samples per bit
	fixed := int64(math.Round(spb * 256.0))         // Q16.8
	if fixed < 0 {
		fixed = 0
	}
	if fixed > 0xFFFFFF {
		fixed = 0xFFFFFF
	}
	return uint16(fixed & 0xFFFF), uint16((fixed >> 16) & 0xFF), true
}

// fabricBus is the minimal bus surface the fabric driver needs: WriteSpare for
// the hand-decoded decode selectors and Read for the (unguarded) result side.
// bus.Bus satisfies it.
type fabricBus interface {
	WriteSpare(sel, val uint16) error
	Read(plane iface.Plane, sel uint16) (uint16, error)
}

// FabricTrig drives the in-fabric decode+trigger. Owner-goroutine only.
type FabricTrig struct {
	b fabricBus
}

// NewFabricTrig wraps a bus for the fabric decode+trigger.
func NewFabricTrig(b fabricBus) *FabricTrig { return &FabricTrig{b: b} }

// Arm programs the decode+trigger registers. It writes CFG=0 FIRST — that makes
// the decoder inert, clears the sticky `matched` bit (dec_en falling), and clears
// the FIFO overflow (acq.v clr_overflow = op_reset | we_DEC_CFG) — then loads
// THR/SPB/MATCH/TESTGEN, then writes the live CFG LAST so dec_en+trig_en assert
// atomically on a fully-loaded config.
func (t *FabricTrig) Arm(r fabricRegs, spbLo, spbHi uint16) error {
	if err := t.b.WriteSpare(selDecCFG, 0); err != nil {
		return err
	}
	if err := t.b.WriteSpare(selDecTHR, r.THR); err != nil {
		return err
	}
	if err := t.b.WriteSpare(selDecSPBLo, spbLo); err != nil {
		return err
	}
	if err := t.b.WriteSpare(selDecSPBHi, spbHi); err != nil {
		return err
	}
	if err := t.b.WriteSpare(selDecMATCH, r.MATCH); err != nil {
		return err
	}
	if err := t.b.WriteSpare(selDecTESTGEN, r.TESTGEN); err != nil {
		return err
	}
	return t.b.WriteSpare(selDecCFG, r.CFG)
}

// Disarm returns the decode block to its inert reset state (byte-identical to
// decode-off) by writing CFG=0.
func (t *FabricTrig) Disarm() error { return t.b.WriteSpare(selDecCFG, 0) }

// FabricStatus is a decoded snapshot of STATUS (0x4c) + MATCHED (0x6c). These
// are non-popping reads (the 0x7c byte FIFO is untouched).
type FabricStatus struct {
	Matched     bool  // sticky: the armed pattern fired since the last arm
	MatchedByte uint8 // the anchoring byte at the match (MATCHED[7:0])
	Busy        bool  // FIFO non-empty (bytes waiting to drain)
	Overflow    bool  // the FIFO dropped bytes since the last arm
	Fill        int   // decoded bytes currently queued (0..32)
}

// Poll reads STATUS + MATCHED without popping the byte FIFO. MATCHED (0x6c) is
// data-only: acq.v hardwires its flag bits [9:8] to 0 ({6'd0,2'b00,byte}), so
// the per-byte frame/parity error flags are NOT here — they ride each drained
// 0x7c word instead (see DecodedByte / DrainBytes).
func (t *FabricTrig) Poll() (FabricStatus, error) {
	var s FabricStatus
	st, err := t.b.Read(iface.CS1, selDecSTATUS)
	if err != nil {
		return s, err
	}
	s.Overflow = st&0x8000 != 0
	s.Matched = st&0x4000 != 0
	s.Busy = st&0x2000 != 0
	s.Fill = int(st & 0x07FF)
	mb, err := t.b.Read(iface.CS1, selDecMATCHED)
	if err != nil {
		return s, err
	}
	s.MatchedByte = uint8(mb)
	return s, nil
}

// DecodedByte is one entry drained from the 0x7c byte FIFO. The two flag bits are
// the raw emit flags at that position — for UART they are frame/parity error;
// I2C reuses them as {KIND(addr), NAK} (acq.v dec_byte_hd = {…,flags[1],flags[0],byte}).
type DecodedByte struct {
	Data      uint8
	FrameErr  bool // 0x7c[9] = emit_flags[1]
	ParityErr bool // 0x7c[8] = emit_flags[0]
}

// DrainBytes pops up to len(dst) decoded bytes from the 0x7c auto-inc FIFO. It
// first reads STATUS.fill so it never pops an empty FIFO (an empty pop would
// just replay a stale head). Returns the number drained.
func (t *FabricTrig) DrainBytes(dst []DecodedByte) (int, error) {
	st, err := t.b.Read(iface.CS1, selDecSTATUS)
	if err != nil {
		return 0, err
	}
	n := int(st & 0x07FF)
	if n > len(dst) {
		n = len(dst)
	}
	for i := 0; i < n; i++ {
		w, err := t.b.Read(iface.CS1, selDecBYTE)
		if err != nil {
			return i, err
		}
		dst[i] = DecodedByte{
			Data:      uint8(w),
			FrameErr:  w&0x0200 != 0,
			ParityErr: w&0x0100 != 0,
		}
	}
	return n, nil
}

// ---- engine glue (owner-goroutine only) ----------------------------------

// serviceFabricDecode keeps the in-fabric decode+trigger armed to the current
// SerialParams + band and mirrors its status into Stats. Called once per
// main-loop iteration (engine_loop.go), NOT from the envroll mid-frame pumps, so
// a re-arm (which wipes the sticky + FIFO) never lands mid-capture. It re-arms
// only when the resolved register image or the SPB timing actually changes.
//
// When the flag is off, the protocol is not fabric-decodable, or the trigger is
// disarmed, it returns the block to its inert reset state and the engine keeps
// the software publish-gate (serialQualify) — nothing silently stops triggering.
func (e *Engine) serviceFabricDecode() {
	if e.fabTrig == nil {
		return
	}
	flag := e.serialFabric.Load()
	want := flag && e.serialMode.Load() == SerialTrigger
	if !want {
		e.fabDisarmIfArmed()
		if flag { // flag on but trigger disarmed: keep the stat honest
			e.mu.Lock()
			e.stats.SerialFabric = true
			e.stats.SerialFabArmed = false
			e.mu.Unlock()
		}
		return
	}

	e.ser.mu.Lock()
	p := e.ser.params
	e.ser.mu.Unlock()

	regs, ok := encodeFabricSerial(p)
	if !ok { // protocol not fabric-decodable → software fallback
		e.fabDisarmIfArmed()
		e.mu.Lock()
		e.stats.SerialFabric = true
		e.stats.SerialFabProtoOK = false
		e.stats.SerialFabArmed = false
		e.mu.Unlock()
		return
	}
	spbLo, spbHi, spbOK := fabricSPB(p.Baud, e.band.CaptureIntervalNs()*1e-9)
	if regs.Proto == fabUART && !spbOK {
		// Fabric UART is bit-timed by SPB (samples-per-bit), which needs an
		// explicit baud — the fabric has no auto-baud. Without one, fall back to
		// the software path (which does auto-baud) rather than arm a decoder that
		// cannot lock. I2C/SPI are edge-clocked, so they do not need SPB timing.
		e.fabDisarmIfArmed()
		e.mu.Lock()
		e.stats.SerialFabric = true
		e.stats.SerialFabProtoOK = false
		e.stats.SerialFabArmed = false
		e.mu.Unlock()
		return
	}

	if !e.fabArmed || regs != e.fabRegs || spbLo != e.fabSPBLo || spbHi != e.fabSPBHi {
		if err := e.fabTrig.Arm(regs, spbLo, spbHi); err != nil {
			e.busErr(err)
			return
		}
		e.fabRegs, e.fabSPBLo, e.fabSPBHi = regs, spbLo, spbHi
		e.fabArmed = true
		e.fabPrevMatched = false // the Arm's CFG=0 cleared the sticky
	}

	st, err := e.fabTrig.Poll()
	if err != nil {
		e.busErr(err)
		return
	}
	// The sticky `matched` stays set until the next re-arm; count the rising edge.
	if st.Matched && !e.fabPrevMatched {
		e.serialMatches.Add(1)
	}
	e.fabPrevMatched = st.Matched

	// Drain the queued decoded bytes (lossless to host) so the 32-deep FIFO never
	// overflows, and surface the batch for the UI.
	var batch []int
	if st.Fill > 0 {
		if e.fabBytes == nil {
			e.fabBytes = make([]DecodedByte, 32)
		}
		if n, derr := e.fabTrig.DrainBytes(e.fabBytes); derr != nil {
			e.busErr(derr)
		} else if n > 0 {
			batch = make([]int, n)
			for i := 0; i < n; i++ {
				batch[i] = int(e.fabBytes[i].Data)
			}
		}
	}

	e.mu.Lock()
	e.stats.SerialFabric = true
	e.stats.SerialFabProtoOK = true
	e.stats.SerialFabArmed = true
	e.stats.SerialFabMode = regs.Mode
	e.stats.SerialFabMatched = st.Matched
	e.stats.SerialFabByte = int(st.MatchedByte)
	e.stats.SerialFabFill = st.Fill
	e.stats.SerialFabOverflow = st.Overflow
	if batch != nil {
		e.stats.SerialFabBytes = batch
	}
	e.mu.Unlock()
}

// fabDisarmIfArmed returns the decode block to its inert reset state exactly once
// on the armed→idle edge (never a redundant per-frame CFG=0, which would clear a
// still-unread sticky match).
func (e *Engine) fabDisarmIfArmed() {
	if !e.fabArmed {
		return
	}
	if err := e.fabTrig.Disarm(); err != nil {
		e.busErr(err)
	}
	e.fabArmed = false
	e.fabPrevMatched = false
	e.mu.Lock()
	e.stats.SerialFabArmed = false
	e.mu.Unlock()
}
