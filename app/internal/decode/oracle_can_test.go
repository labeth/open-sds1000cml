package decode

// CAN vs the sigrok `can` decoder. Cases cover the clean path plus the edge
// cases that historically break CAN decoders: every classic DLC 0..8, a
// 29-bit extended identifier, remote frames, payloads that maximize bit
// stuffing (0x00s / 0xFFs / 0x55s), a deliberately corrupted CRC, and
// back-to-back frames at the legal-minimum interframe space with fractional
// samples-per-bit. The generator reuses the package's bit-exact frame
// builders (canBitsMSB / canStuffCore / canCRC15 / canFDStdFrame) on the
// float timeline, so both sides see the identical capture.
//
// Where the two sides expose DIFFERENT granularity, the intersection is
// compared (rule 3):
//  - sigrok 0.7.2's is_valid_crc() is a TODO stub that always returns True,
//    so it can NEVER flag a corrupt CRC. Both sides are asserted to read the
//    same on-wire CRC value; the pass/fail verdict is asserted repo-side
//    only, and sigrok's zero-warning behaviour is pinned so a fixed PD makes
//    the test demand strengthening.
//  - sigrok computes the data-field length from the DLC without checking
//    RTR, so remote frames with DLC>0 grow phantom data bytes (ISO 11898-1:
//    remote frames carry no data field). The repo is right; the divergence
//    is pinned in its own subtest. The strict RTR cross-check uses DLC=0.
//  - sigrok applies the CAN-FD dlc2len table to CLASSIC frames too, so a
//    classic DLC>8 frame (8 data bytes on the wire per ISO 11898-1) makes it
//    wait for a data field that never arrives and drop the rest of the
//    frame. The repo caps at 8 and verifies the CRC; pinned per-side in the
//    classic-dlc12-divergence subtest.
//  - the repo exposes no stuff-bit spans, so sigrok's stuff-bit annotation
//    count is checked against the generator's insertion count instead (repo
//    destuffing correctness is implied by payload + CRC agreement).
//  - for extended frames the repo emits one assembled 29-bit id; sigrok
//    splits base/ext/full. The full-id is the intersection; the base/ext
//    pieces are checked sigrok-vs-generator only.
//  - CAN-FD: the repo is best-effort (stops after the data field), and
//    sigrok's FD trailer handling is likewise approximate (crc_len lumps in
//    stuff bits, values unchecked), so the FD intersection is ID+DLC+payload.

import (
	"fmt"
	"regexp"
	"strconv"
	"testing"
)

// canOFrame is one classic CAN frame for the oracle generator.
type canOFrame struct {
	id     int // 11-bit identifier (29-bit when ext)
	dlc    int
	data   []int // omitted on the wire when rtr (ISO 11898-1)
	rtr    bool
	ext    bool
	nack   bool // leave the ACK slot recessive (no other node ACKed the frame)
	crcXor int  // XORed into the transmitted CRC-15 (0 = clean frame)
}

// canOracleWire renders one classic frame to on-wire bits (0=dominant,
// 1=recessive) including CRC delimiter, dominant ACK, ACK delimiter and the
// legal-minimum EOF(7)+intermission(3) — so concatenated wires are minimally
// spaced back-to-back frames. Also reports the number of inserted stuff bits
// and the transmitted CRC. Stuffing is applied over SOF..CRC of the (possibly
// corrupted) stream, exactly like a real transmitter, so a bad-CRC frame is
// still perfectly destuffable on both sides.
func canOracleWire(f canOFrame) (wire []int, nStuff, txCRC int) {
	rtrBit := 0
	if f.rtr {
		rtrBit = 1
	}
	bits := []int{0} // SOF (dominant)
	if !f.ext {
		bits = append(bits, canBitsMSB(f.id, 11)...)
		bits = append(bits, rtrBit, 0, 0) // RTR, IDE (dominant: standard), r0
	} else {
		bits = append(bits, canBitsMSB((f.id>>18)&0x7ff, 11)...)
		bits = append(bits, 1, 1) // SRR, IDE (recessive: extended)
		bits = append(bits, canBitsMSB(f.id&0x3ffff, 18)...)
		bits = append(bits, rtrBit, 0, 0) // RTR, r1, r0
	}
	bits = append(bits, canBitsMSB(f.dlc, 4)...)
	if !f.rtr {
		for _, d := range f.data {
			bits = append(bits, canBitsMSB(d, 8)...)
		}
	}
	txCRC = canCRC15(bits) ^ f.crcXor
	stuffIn := append(append([]int{}, bits...), canBitsMSB(txCRC, 15)...)
	stuffed, rv, rl := canStuffCore(stuffIn)
	if rl == 5 {
		stuffed = append(stuffed, 1-rv) // trailing stuff bit before the CRC delimiter
	}
	nStuff = len(stuffed) - len(stuffIn)
	ackBit := 0 // some node on the bus drives the ACK slot dominant (the normal case)
	if f.nack {
		ackBit = 1 // nobody ACKed: the slot stays recessive
	}
	wire = append(stuffed, 1, ackBit, 1) // CRC delimiter, ACK slot, ACK delimiter
	for i := 0; i < 7+3; i++ {
		wire = append(wire, 1) // EOF(7) + intermission(3): the legal minimum gap
	}
	return wire, nStuff, txCRC
}

// canOracleBits lays wire bit sequences on the float timeline at the given
// bit rate (recessive=1=high level, i.e. the standard DominantLow mapping the
// sigrok PD also assumes), with lead idle and enough trail idle for sigrok's
// EOF annotation (which only completes 10 bit times after the ACK delimiter).
func canOracleBits(sr, baud float64, wires ...[]int) []byte {
	w := newTimeline(sr)
	bt := 1 / baud
	w.add(1, 8*bt)
	for _, wire := range wires {
		for _, b := range wire {
			w.add(byte(b), bt)
		}
	}
	w.add(1, 20*bt)
	return w.bits
}

var canAnnValRe = regexp.MustCompile(`: (0x[0-9a-fA-F]+|\d+)`)

// canAnnVals extracts the first numeric token after a colon of each sigrok
// annotation — uniform across the PD's wordy texts: "Identifier: 291 (0x123)"
// -> 291, "Data byte 0: 0xde" -> 0xde (the byte index sits BEFORE the colon),
// "Data length code: 3" -> 3, "CRC-15 sequence: 0x7dc2" -> 0x7dc2.
func canAnnVals(t *testing.T, anns []ann) []int {
	t.Helper()
	out := make([]int, 0, len(anns))
	for _, a := range anns {
		m := canAnnValRe.FindStringSubmatch(a.Text)
		if m == nil {
			t.Fatalf("annotation %q has no numeric value", a.Text)
		}
		v, err := strconv.ParseInt(m[1], 0, 64) // base 0: handles both 0x… and decimal
		if err != nil {
			t.Fatalf("annotation %q: %v", a.Text, err)
		}
		out = append(out, int(v))
	}
	return out
}

// canWantTexts asserts a sigrok annotation stream is exactly the given texts.
func canWantTexts(t *testing.T, what string, anns []ann, want ...string) {
	t.Helper()
	if len(anns) != len(want) {
		t.Fatalf("%s: sigrok emitted %d annotations, want %d: %v", what, len(anns), len(want), anns)
	}
	for i, a := range anns {
		if a.Text != want[i] {
			t.Fatalf("%s: annotation %d is %q, want %q", what, i, a.Text, want[i])
		}
	}
}

func TestOracleCAN(t *testing.T) {
	needSigrok(t)
	const sr = 1_000_000

	run := func(t *testing.T, bits []byte, baud int, class string) []ann {
		return sigrokDecode(t, sr, []string{"CANRX"}, [][]byte{bits},
			fmt.Sprintf("can:can_rx=CANRX:nominal_bitrate=%d", baud), "can="+class)
	}
	decode := func(t *testing.T, bits []byte, baud int) Result {
		res := DecodeCANFD(bitsToCodes(bits), 1.0/sr, CANFDCfg{NominalBaud: baud, DominantLow: true})
		if !res.OK {
			t.Fatalf("repo decode failed: %s", res.Error)
		}
		return res
	}
	noWarnings := func(t *testing.T, bits []byte, baud int) {
		t.Helper()
		if w := run(t, bits, baud, "warnings"); len(w) != 0 {
			t.Fatalf("sigrok warned on clean traffic: %q", w[0].Text)
		}
	}

	t.Run("std-dlc-sweep", func(t *testing.T) {
		// Nine standard-ID data frames, DLC 0..8, in ONE capture at the
		// minimum interframe space. IDs stay clear of the 0x7F0.. range
		// (bits 10..4 all recessive draws a sigrok warning by design).
		const baud = 50_000 // spb 20 at 1 MHz
		var wires [][]int
		var wantIDs, wantDLCs, wantData []int
		for dlc := 0; dlc <= 8; dlc++ {
			f := canOFrame{id: 0x0A0 + 0x10*dlc, dlc: dlc}
			for k := 0; k < dlc; k++ {
				f.data = append(f.data, (0x11*dlc+0x2F*k+7)&0xff)
			}
			wire, _, _ := canOracleWire(f)
			wires = append(wires, wire)
			wantIDs = append(wantIDs, f.id)
			wantDLCs = append(wantDLCs, dlc)
			wantData = append(wantData, f.data...)
		}
		bits := canOracleBits(sr, baud, wires...)
		res := decode(t, bits, baud)
		sofs := run(t, bits, baud, "sof")
		if n := countSpans(res, "sof"); n != 9 || len(sofs) != 9 {
			t.Fatalf("frame count: repo %d, sigrok %d, want 9", n, len(sofs))
		}
		eqBytes(t, "sweep ids", spanBytes(res, "id"), canAnnVals(t, run(t, bits, baud, "id")))
		eqBytes(t, "sweep ids (expected)", spanBytes(res, "id"), wantIDs)
		eqBytes(t, "sweep dlcs", spanBytes(res, "dlc"), canAnnVals(t, run(t, bits, baud, "dlc")))
		eqBytes(t, "sweep dlcs (expected)", spanBytes(res, "dlc"), wantDLCs)
		dataAnns := run(t, bits, baud, "data")
		eqBytes(t, "sweep payload (sigrok view)", canAnnVals(t, dataAnns), wantData) // guards against a vacuous empty==empty pass
		eqBytes(t, "sweep payload", res.Bytes, canAnnVals(t, dataAnns))
		eqAligned(t, "sweep data spans", res, "data", dataAnns, sr/baud)
		// Same destuffed CRC bits read on both sides; the repo additionally
		// verdicts them (all clean here — zero frame-error spans proves the
		// repo CRC-15 matches the generator's on 9 different frame lengths).
		eqBytes(t, "sweep crc values", spanBytes(res, "crc"), canAnnVals(t, run(t, bits, baud, "crc-sequence")))
		if n := countSpans(res, "frame-error"); n != 0 {
			t.Fatalf("repo flagged %d frame errors on clean traffic", n)
		}
		acks := run(t, bits, baud, "ack-slot")
		if n := countSpans(res, "ack"); n != 9 || countSpans(res, "nak") != 0 || len(acks) != 9 {
			t.Fatalf("ACK count: repo %d acks/%d naks, sigrok %d, want 9/0/9",
				n, countSpans(res, "nak"), len(acks))
		}
		for _, a := range acks {
			if a.Text != "ACK slot: ACK" {
				t.Fatalf("sigrok saw %q on a dominant ACK slot", a.Text)
			}
		}
		noWarnings(t, bits, baud)
	})

	t.Run("extended-id", func(t *testing.T) {
		const baud = 50_000
		id29 := (0x4D3 << 18) | 0x2F0C1
		f := canOFrame{id: id29, dlc: 4, data: []int{0xDE, 0xAD, 0xBE, 0xEF}, ext: true}
		wire, _, _ := canOracleWire(f)
		bits := canOracleBits(sr, baud, wire)
		res := decode(t, bits, baud)
		// Repo emits ONE assembled 29-bit id; sigrok's full-id is the
		// intersection. Its base/ext split is checked against the generator.
		if s := findSpan(res, "id"); s == nil || s.Val != id29 {
			t.Fatalf("repo 29-bit id: got %+v, want Val=%#x", s, id29)
		}
		eqBytes(t, "29-bit id", spanBytes(res, "id"), canAnnVals(t, run(t, bits, baud, "full-id")))
		eqBytes(t, "base id (sigrok)", canAnnVals(t, run(t, bits, baud, "id")), []int{id29 >> 18})
		eqBytes(t, "ext id (sigrok)", canAnnVals(t, run(t, bits, baud, "ext-id")), []int{id29 & 0x3ffff})
		if s := findSpan(res, "ide"); s == nil || s.Text != "EXT" {
			t.Fatalf("repo ide flag: got %+v, want EXT", s)
		}
		canWantTexts(t, "ide", run(t, bits, baud, "ide"), "Identifier extension bit: extended frame")
		dataAnns := run(t, bits, baud, "data")
		eqBytes(t, "ext payload (sigrok view)", canAnnVals(t, dataAnns), f.data)
		eqBytes(t, "ext payload", res.Bytes, canAnnVals(t, dataAnns))
		eqAligned(t, "ext data spans", res, "data", dataAnns, sr/baud)
		eqBytes(t, "ext crc value", spanBytes(res, "crc"), canAnnVals(t, run(t, bits, baud, "crc-sequence")))
		noWarnings(t, bits, baud)
	})

	t.Run("rtr-frame", func(t *testing.T) {
		// Remote frame, DLC 0 — the strict cross-check (see the divergence
		// subtest for why nonzero DLC cannot be compared strictly).
		const baud = 50_000
		wire, _, _ := canOracleWire(canOFrame{id: 0x2AA, dlc: 0, rtr: true})
		bits := canOracleBits(sr, baud, wire)
		res := decode(t, bits, baud)
		if s := findSpan(res, "rtr"); s == nil || s.Text != "RTR" {
			t.Fatalf("repo rtr flag: got %+v, want RTR", s)
		}
		canWantTexts(t, "rtr", run(t, bits, baud, "rtr"), "Remote transmission request: remote frame")
		if n := len(run(t, bits, baud, "data")); n != 0 || len(res.Bytes) != 0 {
			t.Fatalf("data on an RTR frame: repo %d bytes, sigrok %d annotations", len(res.Bytes), n)
		}
		eqBytes(t, "rtr id", spanBytes(res, "id"), canAnnVals(t, run(t, bits, baud, "id")))
		eqBytes(t, "rtr crc value", spanBytes(res, "crc"), canAnnVals(t, run(t, bits, baud, "crc-sequence")))
		if n := countSpans(res, "frame-error"); n != 0 {
			t.Fatalf("repo flagged %d frame errors on a clean RTR frame", n)
		}
		noWarnings(t, bits, baud)
	})

	t.Run("rtr-nonzero-dlc-divergence", func(t *testing.T) {
		// DIVERGENCE (sigrok PD deficiency, pinned): a remote frame carries no
		// data field regardless of its DLC (ISO 11898-1), and the repo honours
		// that (remote => zero data bytes, CRC directly after the DLC — and
		// verified clean here). sigrok 0.7.2's can PD computes last_databit
		// from the DLC without checking RTR (pd.py decode_standard_frame), so
		// it "reads" dlc2len(DLC) phantom data bytes out of the CRC/ACK/EOF
		// bits. Both true behaviours are asserted so a future PD fix trips
		// this test and the pin gets upgraded to a strict comparison.
		const baud = 50_000
		wire, _, _ := canOracleWire(canOFrame{id: 0x2AA, dlc: 3, rtr: true})
		bits := canOracleBits(sr, baud, wire)
		res := decode(t, bits, baud)
		if len(res.Bytes) != 0 {
			t.Fatalf("repo read %d data bytes from an RTR frame, want 0", len(res.Bytes))
		}
		if s := findSpan(res, "dlc"); s == nil || s.Val != 3 {
			t.Fatalf("repo dlc: got %+v, want 3", s)
		}
		if n := countSpans(res, "frame-error"); n != 0 {
			t.Fatalf("repo flagged %d frame errors on a clean RTR frame", n)
		}
		if phantom := run(t, bits, baud, "data"); len(phantom) != 3 {
			t.Fatalf("sigrok phantom-data count changed: got %d, want 3 — PD fixed? strengthen this test", len(phantom))
		}
	})

	t.Run("stuff-maximizer", func(t *testing.T) {
		// Three DLC-8 frames whose payloads force the stuffing extremes:
		// all-0x00 (dominant runs), all-0xFF (recessive runs), all-0x55
		// (no intra-data stuffing at all — the control).
		const baud = 50_000
		payloads := [][]int{
			{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
			{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
			{0x55, 0x55, 0x55, 0x55, 0x55, 0x55, 0x55, 0x55},
		}
		ids := []int{0x155, 0x2AA, 0x0F3}
		var wires [][]int
		var wantData []int
		totalStuff := 0
		for i, p := range payloads {
			wire, ns, _ := canOracleWire(canOFrame{id: ids[i], dlc: 8, data: p})
			wires = append(wires, wire)
			wantData = append(wantData, p...)
			totalStuff += ns
		}
		// The vector must actually exercise stuffing hard, or it proves nothing.
		if totalStuff < 20 {
			t.Fatalf("vector inserts only %d stuff bits — not a stuffing stress", totalStuff)
		}
		bits := canOracleBits(sr, baud, wires...)
		res := decode(t, bits, baud)
		// The repo exposes no stuff-bit spans, so the oracle's per-stuff-bit
		// annotations are checked against the GENERATOR's insertion count;
		// repo destuffing correctness follows from payload + CRC agreement.
		if anns := run(t, bits, baud, "stuff-bit"); len(anns) != totalStuff {
			t.Fatalf("sigrok saw %d stuff bits, generator inserted %d", len(anns), totalStuff)
		}
		dataAnns := run(t, bits, baud, "data")
		eqBytes(t, "stuffed payload (sigrok view)", canAnnVals(t, dataAnns), wantData)
		eqBytes(t, "stuffed payload", res.Bytes, canAnnVals(t, dataAnns))
		eqAligned(t, "stuffed data spans", res, "data", dataAnns, sr/baud)
		eqBytes(t, "stuffed ids", spanBytes(res, "id"), canAnnVals(t, run(t, bits, baud, "id")))
		eqBytes(t, "stuffed crc values", spanBytes(res, "crc"), canAnnVals(t, run(t, bits, baud, "crc-sequence")))
		if n := countSpans(res, "frame-error"); n != 0 {
			t.Fatalf("repo flagged %d frame errors on clean traffic", n)
		}
		sofs := run(t, bits, baud, "sof")
		if n := countSpans(res, "sof"); n != 3 || len(sofs) != 3 {
			t.Fatalf("frame count: repo %d, sigrok %d, want 3", n, len(sofs))
		}
		noWarnings(t, bits, baud)
	})

	t.Run("crc-corrupted", func(t *testing.T) {
		// The transmitted CRC is wrong (xor 1) but stuffing is computed over
		// the corrupted stream, so both sides destuff identically and read the
		// SAME corrupt value — the repo turns its crc span into a frame-error
		// ("CRC:!…"). sigrok 0.7.2 CANNOT flag it: its is_valid_crc() is a
		// TODO stub returning True (pd.py line 168), so the verdict is
		// repo-side only and sigrok's silence is pinned — a fixed PD makes
		// this fail and the assertion gets upgraded to both-sides-flag.
		const baud = 50_000
		f := canOFrame{id: 0x123, dlc: 2, data: []int{0x11, 0x22}, crcXor: 1}
		wire, _, txCRC := canOracleWire(f)
		bits := canOracleBits(sr, baud, wire)
		res := decode(t, bits, baud)
		if n := countSpans(res, "frame-error"); n != 1 {
			t.Fatalf("repo flagged %d frame errors on a corrupt CRC, want 1", n)
		}
		if n := countSpans(res, "crc"); n != 0 {
			t.Fatalf("repo still emitted %d clean crc spans", n)
		}
		eqBytes(t, "corrupt CRC value", spanBytes(res, "frame-error"), []int{txCRC})
		eqBytes(t, "corrupt CRC value (sigrok)", canAnnVals(t, run(t, bits, baud, "crc-sequence")), []int{txCRC})
		// The payload precedes the CRC and must still agree bit-for-bit.
		dataAnns := run(t, bits, baud, "data")
		eqBytes(t, "payload before corrupt CRC", res.Bytes, canAnnVals(t, dataAnns))
		eqBytes(t, "payload before corrupt CRC (expected)", res.Bytes, f.data)
		if w := run(t, bits, baud, "warnings"); len(w) != 0 {
			t.Fatalf("sigrok now warns on a corrupt CRC (%q) — PD fixed? strengthen this test", w[0].Text)
		}
	})

	t.Run("back-to-back-fractional-spb", func(t *testing.T) {
		// Two frames separated by exactly EOF(7)+intermission(3) — the legal
		// minimum — at 72900 baud (13.717 samples/bit at 1 MHz): per-frame
		// resync plus fractional bit widths must not break either side.
		const baud = 72_900
		w1, _, _ := canOracleWire(canOFrame{id: 0x100, dlc: 1, data: []int{0xAA}})
		w2, _, _ := canOracleWire(canOFrame{id: 0x200, dlc: 1, data: []int{0x55}})
		bits := canOracleBits(sr, baud, w1, w2)
		res := decode(t, bits, baud)
		sofs := run(t, bits, baud, "sof")
		if n := countSpans(res, "sof"); n != 2 || len(sofs) != 2 {
			t.Fatalf("frame count: repo %d, sigrok %d, want 2", n, len(sofs))
		}
		eqAligned(t, "sof positions", res, "sof", sofs, sr/baud+1)
		eqBytes(t, "b2b ids", spanBytes(res, "id"), canAnnVals(t, run(t, bits, baud, "id")))
		eqBytes(t, "b2b ids (expected)", spanBytes(res, "id"), []int{0x100, 0x200})
		dataAnns := run(t, bits, baud, "data")
		eqBytes(t, "b2b payload", res.Bytes, canAnnVals(t, dataAnns))
		eqBytes(t, "b2b payload (expected)", res.Bytes, []int{0xAA, 0x55})
		eqAligned(t, "b2b data spans", res, "data", dataAnns, sr/baud+1)
		eqBytes(t, "b2b crc values", spanBytes(res, "crc"), canAnnVals(t, run(t, bits, baud, "crc-sequence")))
		if n := countSpans(res, "frame-error"); n != 0 {
			t.Fatalf("repo flagged %d frame errors on clean traffic", n)
		}
		noWarnings(t, bits, baud)
	})

	t.Run("recessive-ack-slot", func(t *testing.T) {
		// No other node on the bus: the ACK slot stays recessive. A passive
		// monitor cannot treat that as a frame error (it has no notion of a
		// required acker), and neither side does: the repo emits a "nak" span
		// instead of "ack", sigrok annotates "ACK slot: NACK" with ZERO
		// warnings and still completes the frame (EOF). CRC and payload must
		// read clean and identical on both sides — an un-ACKed frame is still
		// a well-formed frame.
		const baud = 50_000
		f := canOFrame{id: 0x321, dlc: 2, data: []int{0xA5, 0x3C}, nack: true}
		wire, _, _ := canOracleWire(f)
		bits := canOracleBits(sr, baud, wire)
		res := decode(t, bits, baud)
		if na, nn := countSpans(res, "ack"), countSpans(res, "nak"); na != 0 || nn != 1 {
			t.Fatalf("repo saw %d acks/%d naks on a recessive ACK slot, want 0/1", na, nn)
		}
		if s := findSpan(res, "nak"); s == nil || s.Text != "NAK" || s.Val != 1 {
			t.Fatalf("repo nak span: got %+v, want Text=NAK Val=1", s)
		}
		acks := run(t, bits, baud, "ack-slot")
		canWantTexts(t, "nack slot", acks, "ACK slot: NACK")
		// The repo's nak span must sit on the same bit cell sigrok annotates.
		eqAligned(t, "nak position", res, "nak", acks, sr/baud)
		// Frame body is unaffected by the missing ACK on both sides.
		dataAnns := run(t, bits, baud, "data")
		eqBytes(t, "nack payload (sigrok view)", canAnnVals(t, dataAnns), f.data)
		eqBytes(t, "nack payload", res.Bytes, canAnnVals(t, dataAnns))
		eqBytes(t, "nack crc value", spanBytes(res, "crc"), canAnnVals(t, run(t, bits, baud, "crc-sequence")))
		if n := countSpans(res, "frame-error"); n != 0 {
			t.Fatalf("repo flagged %d frame errors on an un-ACKed clean frame", n)
		}
		if eofs := run(t, bits, baud, "eof"); len(eofs) != 1 {
			t.Fatalf("sigrok emitted %d EOF annotations, want 1 (frame must complete without ACK)", len(eofs))
		}
		noWarnings(t, bits, baud)
	})

	t.Run("auto-baud", func(t *testing.T) {
		// NominalBaud=0: the repo infers samples/bit from the edge-gap
		// distribution (inferCANspb: deterministic cluster walk with integer-
		// multiple validation). sigrok has no auto-baud, so it gets the TRUE
		// bitrate explicitly — the repo, told nothing, must still match
		// sigrok AND the generated truth. Three frames; the 0x55/0xAA payload
		// alternates every bit, anchoring the 1-bit gap cluster the inference
		// keys on.
		const baud = 50_000 // true rate: 20 samples/bit at 1 MHz
		frames := []canOFrame{
			{id: 0x0B4, dlc: 4, data: []int{0x55, 0x55, 0xAA, 0x55}},
			{id: 0x345, dlc: 3, data: []int{0xDE, 0xAD, 0xBE}},
			{id: 0x0C7, dlc: 2, data: []int{0x0F, 0xF0}},
		}
		var wires [][]int
		var wantIDs, wantDLCs, wantData []int
		for _, f := range frames {
			wire, _, _ := canOracleWire(f)
			wires = append(wires, wire)
			wantIDs = append(wantIDs, f.id)
			wantDLCs = append(wantDLCs, f.dlc)
			wantData = append(wantData, f.data...)
		}
		bits := canOracleBits(sr, baud, wires...)
		res := decode(t, bits, 0) // NominalBaud=0 => auto-infer
		// The inference must lock onto the true bit time, not merely produce
		// something decodable — a 2x/0.5x lock would also "decode" garbage.
		if truth := float64(sr) / baud; res.SPB < truth-0.5 || res.SPB > truth+0.5 {
			t.Fatalf("inferred %.2f samples/bit, true rate is %.0f", res.SPB, truth)
		}
		sofs := run(t, bits, baud, "sof")
		if n := countSpans(res, "sof"); n != 3 || len(sofs) != 3 {
			t.Fatalf("frame count: repo %d, sigrok %d, want 3", n, len(sofs))
		}
		eqBytes(t, "auto-baud ids", spanBytes(res, "id"), canAnnVals(t, run(t, bits, baud, "id")))
		eqBytes(t, "auto-baud ids (expected)", spanBytes(res, "id"), wantIDs)
		eqBytes(t, "auto-baud dlcs", spanBytes(res, "dlc"), canAnnVals(t, run(t, bits, baud, "dlc")))
		eqBytes(t, "auto-baud dlcs (expected)", spanBytes(res, "dlc"), wantDLCs)
		dataAnns := run(t, bits, baud, "data")
		eqBytes(t, "auto-baud payload (sigrok view)", canAnnVals(t, dataAnns), wantData)
		eqBytes(t, "auto-baud payload", res.Bytes, canAnnVals(t, dataAnns))
		// Span positions are built from the INFERRED spb — they must still
		// land on sigrok's true-bitrate annotations.
		eqAligned(t, "auto-baud data spans", res, "data", dataAnns, sr/baud)
		eqBytes(t, "auto-baud crc values", spanBytes(res, "crc"), canAnnVals(t, run(t, bits, baud, "crc-sequence")))
		if n := countSpans(res, "frame-error"); n != 0 {
			t.Fatalf("repo flagged %d frame errors on clean traffic", n)
		}
		noWarnings(t, bits, baud)
	})

	t.Run("auto-baud-sparse-single-bit-gaps", func(t *testing.T) {
		// The percentile-killer the sigrok oracle exposed: a perfectly legal
		// frame whose 0x33/0x99/0xCC payload leaves roughly ONE single-bit
		// gap among ~40 two-bit ones. inferCANspb's old blind 10th-percentile
		// seeded on the 2-bit cluster and HALVED the rate — auto decode then
		// returned ok=true with a hallucinated frame ("XID:1646A956 RTR
		// DLC:10" and no payload) while pinned decode and sigrok read the
		// real frame cleanly. The cluster walk must find the lone 1-bit gap:
		// auto must now match sigrok (pinned at the true rate) and the
		// generated truth exactly.
		const baud = 50_000
		fr := canOFrame{id: 0x19C, dlc: 8, data: []int{0x33, 0x99, 0xCC, 0x33, 0x33, 0xCC, 0x33, 0x33}}
		wire, _, _ := canOracleWire(fr)
		bits := canOracleBits(sr, baud, wire)
		res := decode(t, bits, 0) // NominalBaud=0 => auto-infer
		if truth := float64(sr) / baud; res.SPB < truth-0.5 || res.SPB > truth+0.5 {
			t.Fatalf("inferred %.2f samples/bit, true rate is %.0f (rate-halving regression?)", res.SPB, truth)
		}
		eqBytes(t, "sparse-gap auto id", spanBytes(res, "id"), []int{0x19C})
		eqBytes(t, "sparse-gap auto id (sigrok)", spanBytes(res, "id"), canAnnVals(t, run(t, bits, baud, "id")))
		eqBytes(t, "sparse-gap auto dlc", spanBytes(res, "dlc"), []int{8})
		dataAnns := run(t, bits, baud, "data")
		eqBytes(t, "sparse-gap auto payload (sigrok view)", canAnnVals(t, dataAnns), fr.data)
		eqBytes(t, "sparse-gap auto payload", res.Bytes, canAnnVals(t, dataAnns))
		eqAligned(t, "sparse-gap auto data spans", res, "data", dataAnns, sr/baud)
		if n := countSpans(res, "frame-error"); n != 0 {
			t.Fatalf("repo flagged %d frame errors on clean traffic", n)
		}
		noWarnings(t, bits, baud)
	})

	t.Run("classic-dlc12-divergence", func(t *testing.T) {
		// DIVERGENCE (sigrok PD deficiency, pinned): ISO 11898-1 allows a
		// classic frame to ENCODE DLC 9..15, but the data field is 8 bytes
		// regardless — the transmitter puts 8 bytes on the wire, so this
		// vector carries DLC=12 with 8 data bytes and a CRC over exactly
		// that. The repo caps nBytes at 8 (decode_canfd.go), reads the 8
		// real bytes, and the CRC verifies — proof the cap matches the wire.
		// sigrok 0.7.2 applies the CAN-FD dlc2len table to classic frames
		// too (pd.py decode_standard_frame: last_databit uses dlc2len even
		// when not fd), so it waits for a 24-byte data field that never
		// arrives: it emits the DLC>8 warning plus ID/DLC, then NO data, CRC,
		// ACK or EOF annotations at all (hand-verified). Both true behaviours
		// are pinned; a fixed PD trips the phantom checks below and this pin
		// gets upgraded to a strict comparison.
		const baud = 50_000
		f := canOFrame{id: 0x144, dlc: 12,
			data: []int{0x10, 0x32, 0x54, 0x76, 0x98, 0xBA, 0xDC, 0xFE}}
		wire, _, _ := canOracleWire(f)
		bits := canOracleBits(sr, baud, wire)
		res := decode(t, bits, baud)
		// Repo: DLC field read verbatim, data capped at the 8 on-wire bytes,
		// CRC clean (it covers SOF..8 bytes — a 24-byte reader could never
		// verify it), dominant ACK still seen.
		if s := findSpan(res, "dlc"); s == nil || s.Val != 12 {
			t.Fatalf("repo dlc: got %+v, want 12", s)
		}
		eqBytes(t, "dlc12 payload (repo vs wire truth)", res.Bytes, f.data)
		if n := countSpans(res, "data"); n != 8 {
			t.Fatalf("repo emitted %d data spans, want 8", n)
		}
		if nc, ne := countSpans(res, "crc"), countSpans(res, "frame-error"); nc != 1 || ne != 0 {
			t.Fatalf("repo crc verdict: %d clean/%d errors, want 1/0", nc, ne)
		}
		if n := countSpans(res, "ack"); n != 1 {
			t.Fatalf("repo saw %d ACKs, want 1", n)
		}
		// Both sides agree on the raw ID and DLC field bits (the divergence
		// is only in the implied data-field length).
		eqBytes(t, "dlc12 id (sigrok)", canAnnVals(t, run(t, bits, baud, "id")), []int{f.id})
		eqBytes(t, "dlc12 dlc (sigrok)", canAnnVals(t, run(t, bits, baud, "dlc")), []int{12})
		canWantTexts(t, "dlc12 warning", run(t, bits, baud, "warnings"),
			"Data length code (DLC) > 8 is not allowed")
		// Pinned sigrok deficiency: the phantom 24-byte wait swallows the
		// rest of the frame — zero data/CRC/ACK/EOF annotations.
		for _, class := range []string{"data", "crc-sequence", "ack-slot", "eof"} {
			if anns := run(t, bits, baud, class); len(anns) != 0 {
				t.Fatalf("sigrok now emits %d %s annotations on a classic DLC=12 frame (%q) — PD fixed? strengthen this test",
					len(anns), class, anns[0].Text)
			}
		}
	})

	t.Run("fd-base-frame", func(t *testing.T) {
		// CAN-FD base format, BRS=0 (constant bit rate), DLC 9 => 12 bytes on
		// both sides (repo fdDataLen == PD dlc2len). The repo decodes FD
		// best-effort — ID/DLC/data, then stops before the FD trailer — and
		// sigrok's FD trailer handling is likewise approximate, so the strict
		// intersection is ID + DLC + payload. BRS=1 is untestable against the
		// repo: real CAN-FD (and the PD) switch bit rate at the BRS *sample
		// point*, the repo at the next bit-cell boundary — no one waveform
		// satisfies both timings. FD extended frames are repo-unsupported.
		const baud = 50_000
		const dlc = 9
		data := []int{0xF0, 0x0D, 0xCA, 0xFE, 0x00, 0xFF, 0x55, 0xAA, 0x11, 0x22, 0x33, 0x44}
		if fdDataLen(dlc) != len(data) {
			t.Fatalf("test data length %d != fdDataLen(%d)=%d", len(data), dlc, fdDataLen(dlc))
		}
		wire := canFDStdFrame(0x0C5, dlc, data) // package generator: SOF..data + recessive tail
		bits := canOracleBits(sr, baud, wire)
		res := decode(t, bits, baud)
		if s := findSpan(res, "fd"); s == nil {
			t.Fatalf("repo did not flag FD; kinds=%s", spanKinds(res))
		}
		if s := findSpan(res, "id"); s == nil || s.Val != 0x0C5 {
			t.Fatalf("repo FD id: got %+v, want Val=0xC5", s)
		}
		eqBytes(t, "fd id", spanBytes(res, "id"), canAnnVals(t, run(t, bits, baud, "id")))
		eqBytes(t, "fd dlc", spanBytes(res, "dlc"), canAnnVals(t, run(t, bits, baud, "dlc")))
		eqBytes(t, "fd dlc (expected)", spanBytes(res, "dlc"), []int{dlc})
		dataAnns := run(t, bits, baud, "data")
		eqBytes(t, "fd payload (sigrok view)", canAnnVals(t, dataAnns), data)
		eqBytes(t, "fd payload", res.Bytes, canAnnVals(t, dataAnns))
		eqAligned(t, "fd data spans", res, "data", dataAnns, sr/baud)
		// sigrok reports FDF via its reserved-bit class (first of res/BRS/ESI).
		rb := run(t, bits, baud, "reserved-bit")
		if len(rb) == 0 || rb[0].Text != "Flexible data format: 1" {
			t.Fatalf("sigrok did not flag FDF: %v", rb)
		}
		noWarnings(t, bits, baud)
	})
}
