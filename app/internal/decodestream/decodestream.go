// Package decodestream drains the owned Cyclone fabric's in-fabric decode byte
// FIFO to the host LOSSLESSLY at line rate (ITEM 8), reusing the same CPU-free
// EDMA drain that streams the raw ADC record.
//
// The raw record drain is already byte-exact and CPU-free: a CS1 cycle-to-cycle
// gap (GPMC_CONFIG6_1 CYCLE2CYCLESAMECSEN) forces a fresh nOE strobe per read so
// each read of an auto-inc port pops exactly once, and a fresh cache-cold buffer
// (or /dev/dcinv invalidate) keeps the non-coherent EDMA reads honest. This package
// points that same machinery at the decode FIFO's drain port (DEC_BYTE, 0x7c).
//
// WHY A WIDE FRAME. The legacy 0x7c read pops one FIFO entry per read and returns
// only {flags, byte} — it drops the 24-bit start-column index (the byte's SPAN
// anchor on the timebase). To stream {flags, idx, byte} the fabric is put into
// WIDE-FRAME mode (additive, gated by the previously-FREE SPB_HI[10] bit): each FIFO
// entry is then popped as THREE consecutive 0x7c reads —
//
//	w0 = {flags[7:0], byte[7:0]}
//	w1 = idx[15:0]
//	w2 = {8'h00, idx[23:16]}
//
// and the FIFO advances only on the terminal (w2) read. The host reconstructs one
// Entry per 3 drained words. Wide-frame is OFF at power-up and is cleared by any
// trigger-path SPB_HI write, so the live serial-trigger DrainBytes path (one word
// per entry) is byte-for-byte unchanged; the two modes share the one 32-deep FIFO
// and are mutually exclusive by construction.
//
// EXACTNESS (no dropped/duplicated bytes). Drain() reads STATUS first — which both
// yields the FIFO occupancy `fill` AND re-anchors the fabric's wide-frame phase to
// 0 — then EDMA-drains EXACTLY 3*fill words. Because every read is one real CS1
// transaction (cycle-gap enforced) the fabric phase steps 0,1,2,0,1,2,… precisely
// once per read, popping exactly `fill` entries with each entry's 3 sub-words read
// once in order. Entries the decoder pushes DURING the drain are appended past the
// counted head and stay queued for the next Drain — never popped early, never lost.
// The 32-deep FIFO refuses a push-while-full (fail-visible sticky overflow); Drain
// returns that overflow so a too-slow host sees it rather than silently losing data.
package decodestream

import "open-sds/app/internal/iface"

// Hand-decoded (non-schema) decode-block selectors, matching acq.v. These live off
// the generated iface table on purpose (IFACE_BUILD_ID stays 0xc2f6eb5f), so the
// writes go through the narrow bus.WriteSpare escape hatch and the reads through the
// unguarded bus.Read.
const (
	selDecSTATUS uint16 = 0x4c // R STATUS {ovf[15],matched[14],busy[13],fill[10:0]}; the read re-anchors wide phase
	selDecSPBHi  uint16 = 0x1c // W SPB_HI: [7:0]=spb[23:16]; [10]=wide-frame enable (previously-FREE, additive)
)

// wideEnableBit is the additive SPB_HI[10] wide-frame enable. INTEGRATION NOTE:
// relocated from bit8 to bit10 because item-7 CAN claims 0x1c[8]=can_ext and
// 0x1c[9]=can_domlow; bit10 keeps wide-frame streaming and CAN proto-extend
// independent (the fabric latches dec_wide<=d_q2[10]).
const wideEnableBit uint16 = 1 << 10

// wordsPerEntry is the wide-frame beat count: {w0,w1,w2} per FIFO entry.
const wordsPerEntry = 3

// fillMask is STATUS[10:0] (the FIFO is 32-deep, so this never exceeds 32).
const fillMask uint16 = 0x07FF

// statusOverflow is STATUS[15] (sticky: the FIFO dropped a byte since the last clear).
const statusOverflow uint16 = 0x8000

// Entry is one decoded byte drained from the FIFO with its span-start column.
type Entry struct {
	Flags uint8  // per-proto flags (UART: [1]=frame_err [0]=parity_err; ETH: full 8b start/end/fcs/ok/err)
	Idx   uint32 // 24-bit column index of the byte's start symbol (the span anchor on the timebase)
	Data  uint8  // the decoded byte value
}

// Bus is the narrow register surface the streamer needs; *bus.Dev satisfies it.
// (Kept off the shared bus.Bus interface so the many test fakes need not implement
// it — the streamer depends only on this local interface.)
type Bus interface {
	// Read reads one 16-bit register (STATUS is unguarded; the read re-anchors phase).
	Read(plane iface.Plane, sel uint16) (uint16, error)
	// WriteSpare writes a hand-decoded decode-block selector (here: SPB_HI).
	WriteSpare(sel, val uint16) error
	// DrainDecodeWords drains n words from the 0x7c wide-frame port, CPU-free via EDMA.
	DrainDecodeWords(dst []uint16, n int)
}

// Streamer drains decoded bytes+spans losslessly. Owner-goroutine only (like every
// other bus access). Reuses one scratch word buffer across drains.
type Streamer struct {
	b   Bus
	buf []uint16
}

// New wraps a bus for lossless decode-byte streaming.
func New(b Bus) *Streamer { return &Streamer{b: b} }

// EnableWide switches the 0x7c drain port into 3-word wide-frame mode by setting the
// additive free bit SPB_HI[10]. spbHi23_16 is the decoder's samples-per-bit high byte
// (0 unless a UART baud is armed); it is written through unchanged so wide-frame and
// the decoder's timing coexist in the one SPB_HI register. Idempotent.
func (s *Streamer) EnableWide(spbHi23_16 uint8) error {
	return s.b.WriteSpare(selDecSPBHi, uint16(spbHi23_16)|wideEnableBit)
}

// DisableWide returns the 0x7c port to the legacy one-word-per-entry framing (clears
// SPB_HI[10]), restoring the timing high byte. After this the live serial-trigger
// DrainBytes path reads normally again.
func (s *Streamer) DisableWide(spbHi23_16 uint8) error {
	return s.b.WriteSpare(selDecSPBHi, uint16(spbHi23_16))
}

// Drain reads STATUS (re-anchoring the fabric wide-frame phase to 0 and reporting the
// FIFO occupancy), then EDMA-drains exactly 3*fill words and reconstructs `fill`
// Entries. maxEntries caps how many are drained this call (<=0 means "all queued",
// which the 32-deep FIFO bounds anyway); any beyond the cap stay queued for next time.
//
// It returns the reconstructed entries, whether the FIFO overflowed since the last
// clear (fail-visible: bytes were lost IN-FABRIC because the host drained too slowly),
// and any bus error. It pops EXACTLY the fill counted at STATUS time.
func (s *Streamer) Drain(maxEntries int) (entries []Entry, overflow bool, err error) {
	st, err := s.b.Read(iface.CS1, selDecSTATUS)
	if err != nil {
		return nil, false, err
	}
	overflow = st&statusOverflow != 0
	fill := int(st & fillMask)
	if maxEntries > 0 && fill > maxEntries {
		fill = maxEntries
	}
	if fill == 0 {
		return nil, overflow, nil
	}

	n := fill * wordsPerEntry
	if cap(s.buf) < n {
		s.buf = make([]uint16, n)
	}
	words := s.buf[:n]
	s.b.DrainDecodeWords(words, n)

	entries = make([]Entry, fill)
	for i := 0; i < fill; i++ {
		w0 := words[i*wordsPerEntry+0]
		w1 := words[i*wordsPerEntry+1]
		w2 := words[i*wordsPerEntry+2]
		entries[i] = Entry{
			Flags: uint8(w0 >> 8),
			Data:  uint8(w0),
			Idx:   uint32(w1) | uint32(w2&0x00FF)<<16,
		}
	}
	return entries, overflow, nil
}
