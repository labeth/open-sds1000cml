package decode

import (
	"fmt"
	"math/rand"
	"testing"
)

// decode_break_canfd_test.go — adversarial red-team suite for DecodeCANFD.
//
// It reuses the waveform synthesizer already in decode_canfd_test.go
// (canStdFrame / canExtFrame / canFDStdFrame / canRender / canBitsMSB /
// canStuffCore / canCRC15 / findSpan / hasBang) and adds a few builders of its
// own for the CAN-FD BRS rate switch and classic remote frames.
//
// Three attack classes, each >= 50 iterations, seeded for determinism:
//   1. FALSE NEGATIVES  — valid frames that must decode byte-exact.
//   2. FALSE POSITIVES  — garbage / corrupted frames that must NOT be reported
//                         as a confident (unflagged-CRC) valid frame.
//   3. EDGE CASES       — extreme bit rates, boundary sample counts, degenerate
//                         config; must not panic and must stay sane.
//
// The one FAILING assertion in this file (TestBreakCanfd/false_positive) pins a
// REAL decoder bug: a Nyquist / aliased-constant "signal" that reads dominant at
// every bit centre is accepted as a clean all-zeros classic frame, because the
// decoder never validates stuff-bit polarity or the fixed-form (delimiter/EOF)
// bits, and CRC-15 of all zeros is trivially 0. It is a t.Errorf (non-fatal) so
// every other assertion in the suite still runs.

// ---- extra synthesizers (distinct names; the rest are reused) ----------------

// canStuffTrackedB mirrors canStuffCore but records realIdx[k] = the wire index
// of the k-th input (real, non-stuff) bit, so a two-rate FD renderer can find
// the exact wire position of the BRS bit.
func canStuffTrackedB(bits []int) (out []int, realIdx []int) {
	runVal, runLen := -1, 0
	for _, b := range bits {
		if runLen == 5 {
			sb := 1 - runVal
			out = append(out, sb)
			runVal, runLen = sb, 1
		}
		realIdx = append(realIdx, len(out))
		out = append(out, b)
		if b == runVal {
			runLen++
		} else {
			runVal, runLen = b, 1
		}
	}
	return
}

// canFDBRSFrameB builds a CAN-FD base frame with BRS=1 (data-phase rate switch)
// through the data field. split = wire index of the last nominal-rate bit (BRS).
func canFDBRSFrameB(id, dlc int, data []int) (wire []int, split int) {
	var bits []int
	bits = append(bits, 0)                     // SOF
	bits = append(bits, canBitsMSB(id, 11)...) // ID
	bits = append(bits, 0)                     // RRS
	bits = append(bits, 0)                     // IDE (base)
	bits = append(bits, 1)                     // FDF/EDL => FD
	bits = append(bits, 0)                     // res (r0)
	bits = append(bits, 1)                     // BRS = 1  (input index 16)
	bits = append(bits, 0)                     // ESI
	bits = append(bits, canBitsMSB(dlc, 4)...) // DLC
	for _, d := range data {
		bits = append(bits, canBitsMSB(d, 8)...)
	}
	stuffed, realIdx := canStuffTrackedB(bits)
	split = realIdx[16] // BRS is the 17th input bit; last one at nominal rate
	wire = append([]int{}, stuffed...)
	for i := 0; i < 30; i++ {
		wire = append(wire, 1) // recessive tail (FD best-effort path stops at data)
	}
	return
}

// canRenderTwoRateB lays wire out with wire[0..split] at spb1 and wire[split+1:]
// at spb2 (the faster data-phase rate), plus recessive idle.
func canRenderTwoRateB(wire []int, split, spb1, spb2 int, dominantLow bool, lead, trail int) []uint8 {
	lo, hi := uint8(40), uint8(210)
	var codes []uint8
	push := func(bit, count int) {
		var v uint8
		if bit == 1 { // recessive
			if dominantLow {
				v = hi
			} else {
				v = lo
			}
		} else { // dominant
			if dominantLow {
				v = lo
			} else {
				v = hi
			}
		}
		for j := 0; j < count; j++ {
			codes = append(codes, v)
		}
	}
	push(1, lead*spb1)
	for i, b := range wire {
		s := spb1
		if i > split {
			s = spb2
		}
		push(b, s)
	}
	push(1, trail*spb2)
	return codes
}

// canStdRemoteFrameB builds a classic standard REMOTE (RTR) frame — no data.
func canStdRemoteFrameB(id, dlc int) []int {
	var crcInput []int
	crcInput = append(crcInput, 0)                     // SOF
	crcInput = append(crcInput, canBitsMSB(id, 11)...) // ID
	crcInput = append(crcInput, 1)                     // RTR (remote)
	crcInput = append(crcInput, 0)                     // IDE (standard)
	crcInput = append(crcInput, 0)                     // r0
	crcInput = append(crcInput, canBitsMSB(dlc, 4)...) // DLC (carries no data)
	crc := canCRC15(crcInput)
	stuffIn := append(append([]int{}, crcInput...), canBitsMSB(crc, 15)...)
	stuffed, rv, rl := canStuffCore(stuffIn)
	if rl == 5 {
		stuffed = append(stuffed, 1-rv)
	}
	wire := append([]int{}, stuffed...)
	wire = append(wire, 1, 0, 1) // CRC del, ACK, ACK del
	for i := 0; i < 10; i++ {
		wire = append(wire, 1) // EOF + IFS
	}
	return wire
}

// ---- helpers -----------------------------------------------------------------

func fmtInts(v []int) string { return fmt.Sprintf("%v", v) }

// confidentValidCAN reports whether the result is a *confident* classic CAN frame
// by the protocol's own integrity: OK, with a CRC span whose text is NOT flagged
// with '!' (i.e. the on-wire CRC matched the recomputed CRC-15).
func confidentValidCAN(r Result) bool {
	if !r.OK {
		return false
	}
	cs := findSpan(r, "crc")
	return cs != nil && !hasBang(cs.Text)
}

// safeDecodeCAN runs the decoder under a recover so a panic becomes a test error
// rather than crashing the run.
func safeDecodeCAN(t *testing.T, tag string, codes []uint8, ct float64, cfg CANFDCfg) (r Result) {
	t.Helper()
	defer func() {
		if rec := recover(); rec != nil {
			t.Errorf("PANIC [%s]: %v (n=%d ct=%g cfg=%+v)", tag, rec, len(codes), ct, cfg)
		}
	}()
	return DecodeCANFD(codes, ct, cfg)
}

// exactBaudCfg returns a cfg+ct pair that makes the decoder's samples/bit equal
// exactly `spb` (NominalBaud=1, colTimeS=1/spb  =>  spb = (1/1)/(1/spb)).
func exactBaudCfg(spb int, dominantLow bool) (float64, CANFDCfg) {
	return 1.0 / float64(spb), CANFDCfg{NominalBaud: 1, DominantLow: dominantLow}
}

// ---- the suite ---------------------------------------------------------------

func TestBreakCanfd(t *testing.T) {
	t.Run("false_negative", func(t *testing.T) { breakCANFalseNeg(t) })
	t.Run("false_positive", func(t *testing.T) { breakCANFalsePos(t) })
	t.Run("edge", func(t *testing.T) { breakCANEdge(t) })
}

// 1. FALSE NEGATIVES: >= 50 fully valid frames must decode byte-exact.
func breakCANFalseNeg(t *testing.T) {
	rng := rand.New(rand.NewSource(0xCA11FD))

	// --- single valid frames, explicit exact baud, strict assertions ---------
	const N = 60
	for i := 0; i < N; i++ {
		spb := 3 + rng.Intn(60) // decoder floor is spb >= 3
		dom := rng.Intn(2) == 0
		class := rng.Intn(4) // 0 std, 1 ext, 2 fd, 3 fd+brs
		dlc := rng.Intn(9)
		data := make([]int, dlc)
		for k := range data {
			data[k] = rng.Intn(256)
		}
		lead := 3 + rng.Intn(20)
		trail := 3 + rng.Intn(20)

		switch class {
		case 0: // classic standard
			id := rng.Intn(1 << 11)
			_, wire := canStdFrame(id, dlc, data)
			ct, cfg := exactBaudCfg(spb, dom)
			codes := canRender(wire, spb, dom, lead, trail)
			r := safeDecodeCAN(t, "fn/std", codes, ct, cfg)
			if !r.OK {
				t.Errorf("FALSE-NEG std: ok=false spb=%d dom=%v id=%X dlc=%d err=%q", spb, dom, id, dlc, r.Error)
				continue
			}
			if fmtInts(r.Bytes) != fmtInts(data) {
				t.Errorf("FALSE-NEG std bytes: spb=%d id=%X got=%v want=%v", spb, id, r.Bytes, data)
			}
			if s := findSpan(r, "id"); s == nil || s.Val != id {
				t.Errorf("FALSE-NEG std id: spb=%d got=%+v want=%#x", spb, s, id)
			}
			if cs := findSpan(r, "crc"); cs == nil || hasBang(cs.Text) {
				t.Errorf("FALSE-NEG std crc flagged/absent: spb=%d id=%X crc=%+v", spb, id, cs)
			}

		case 1: // classic extended (29-bit)
			id := rng.Intn(1 << 29)
			_, wire := canExtFrame(id, dlc, data)
			ct, cfg := exactBaudCfg(spb, dom)
			codes := canRender(wire, spb, dom, lead, trail)
			r := safeDecodeCAN(t, "fn/ext", codes, ct, cfg)
			if !r.OK {
				t.Errorf("FALSE-NEG ext: ok=false spb=%d dom=%v id=%X dlc=%d err=%q", spb, dom, id, dlc, r.Error)
				continue
			}
			if fmtInts(r.Bytes) != fmtInts(data) {
				t.Errorf("FALSE-NEG ext bytes: spb=%d id=%X got=%v want=%v", spb, id, r.Bytes, data)
			}
			if s := findSpan(r, "id"); s == nil || s.Val != id {
				t.Errorf("FALSE-NEG ext id: spb=%d got=%+v want=%#x", spb, s, id)
			}
			if s := findSpan(r, "ide"); s == nil || s.Text != "EXT" {
				t.Errorf("FALSE-NEG ext ide flag: spb=%d got=%+v", spb, s)
			}
			if cs := findSpan(r, "crc"); cs == nil || hasBang(cs.Text) {
				t.Errorf("FALSE-NEG ext crc flagged/absent: spb=%d id=%X crc=%+v", spb, id, cs)
			}

		case 2: // CAN-FD, single rate
			id := rng.Intn(1 << 11)
			fdDLC := rng.Intn(16)
			n := fdDataLen(fdDLC)
			fdData := make([]int, n)
			for k := range fdData {
				fdData[k] = rng.Intn(256)
			}
			wire := canFDStdFrame(id, fdDLC, fdData)
			ct, cfg := exactBaudCfg(spb, dom)
			codes := canRender(wire, spb, dom, lead, trail)
			r := safeDecodeCAN(t, "fn/fd", codes, ct, cfg)
			if !r.OK {
				t.Errorf("FALSE-NEG fd: ok=false spb=%d dom=%v id=%X dlc=%d n=%d err=%q", spb, dom, id, fdDLC, n, r.Error)
				continue
			}
			if fmtInts(r.Bytes) != fmtInts(fdData) {
				t.Errorf("FALSE-NEG fd bytes: spb=%d dlc=%d n=%d got=%v want=%v", spb, fdDLC, n, r.Bytes, fdData)
			}
			if findSpan(r, "fd") == nil {
				t.Errorf("FALSE-NEG fd not flagged: spb=%d id=%X kinds=%s", spb, id, spanKinds(r))
			}

		case 3: // CAN-FD with BRS data-rate switch (integer rate ratio)
			id := rng.Intn(1 << 11)
			ratio := []int{2, 4, 5, 8}[rng.Intn(4)]
			spb2 := 4 + rng.Intn(6) // data-phase spb
			spb1 := spb2 * ratio    // slower nominal
			fdDLC := rng.Intn(16)
			n := fdDataLen(fdDLC)
			fdData := make([]int, n)
			for k := range fdData {
				fdData[k] = rng.Intn(256)
			}
			wire, split := canFDBRSFrameB(id, fdDLC, fdData)
			codes := canRenderTwoRateB(wire, split, spb1, spb2, dom, lead, trail)
			ct := 1.0 / float64(spb1) // nominal spb = spb1
			cfg := CANFDCfg{NominalBaud: 1, DataBaud: spb1 / spb2, DominantLow: dom}
			r := safeDecodeCAN(t, "fn/fdbrs", codes, ct, cfg)
			if !r.OK {
				t.Errorf("FALSE-NEG fd-brs: ok=false spb1=%d spb2=%d dlc=%d n=%d err=%q", spb1, spb2, fdDLC, n, r.Error)
				continue
			}
			if fmtInts(r.Bytes) != fmtInts(fdData) {
				t.Errorf("FALSE-NEG fd-brs bytes: spb1=%d spb2=%d dlc=%d n=%d got=%v want=%v", spb1, spb2, fdDLC, n, r.Bytes, fdData)
			}
		}
	}

	// --- auto-baud (the default menu path): must still recover the payload ----
	for i := 0; i < 15; i++ {
		spb := 8 + rng.Intn(40)
		dlc := rng.Intn(9)
		data := make([]int, dlc)
		for k := range data {
			data[k] = rng.Intn(256)
		}
		ext := rng.Intn(2) == 0
		var wire []int
		if ext {
			_, wire = canExtFrame(rng.Intn(1<<29), dlc, data)
		} else {
			_, wire = canStdFrame(rng.Intn(1<<11), dlc, data)
		}
		codes := canRender(wire, spb, true, 6+rng.Intn(16), 6+rng.Intn(16))
		r := safeDecodeCAN(t, "fn/auto", codes, 1e-6, CANFDCfg{DominantLow: true})
		if !r.OK {
			t.Errorf("FALSE-NEG auto: ok=false spb=%d ext=%v dlc=%d err=%q", spb, ext, dlc, r.Error)
			continue
		}
		if fmtInts(r.Bytes) != fmtInts(data) {
			t.Errorf("FALSE-NEG auto bytes: spb=%d ext=%v got=%v want=%v spbOut=%.2f", spb, ext, r.Bytes, data, r.SPB)
		}
	}

	// --- back-to-back frames, no external gap between them --------------------
	for i := 0; i < 12; i++ {
		spb := 8 + rng.Intn(24)
		nf := 2 + rng.Intn(3)
		var wire []int
		var allData []int
		for f := 0; f < nf; f++ {
			dlc := rng.Intn(9)
			data := make([]int, dlc)
			for k := range data {
				data[k] = rng.Intn(256)
			}
			_, w := canStdFrame(rng.Intn(1<<11), dlc, data)
			wire = append(wire, w...)
			allData = append(allData, data...)
		}
		ct, cfg := exactBaudCfg(spb, true)
		codes := canRender(wire, spb, true, 8, 8)
		r := safeDecodeCAN(t, "fn/b2b", codes, ct, cfg)
		if !r.OK {
			t.Errorf("FALSE-NEG b2b: ok=false nf=%d spb=%d err=%q", nf, spb, r.Error)
			continue
		}
		if fmtInts(r.Bytes) != fmtInts(allData) {
			t.Errorf("FALSE-NEG b2b bytes: nf=%d spb=%d got=%v want=%v", nf, spb, r.Bytes, allData)
		}
	}
}

// 2. FALSE POSITIVES: garbage / corrupted frames must not be reported as a
// confident (unflagged-CRC) valid classic frame.
func breakCANFalsePos(t *testing.T) {
	rng := rand.New(rand.NewSource(0xDEAD))

	// --- pure garbage shapes: noise / ramp / flat / wrong-square / step ------
	garbageFP := 0
	for i := 0; i < 60; i++ {
		n := 300 + rng.Intn(3000)
		kind := i % 5
		codes := make([]uint8, n)
		switch kind {
		case 0: // white noise
			for k := range codes {
				codes[k] = uint8(rng.Intn(256))
			}
		case 1: // slow ramp
			for k := range codes {
				codes[k] = uint8((k * 255) / n)
			}
		case 2: // flat / DC (should be gated out by amplitude)
			v := uint8(rng.Intn(256))
			for k := range codes {
				codes[k] = v
			}
		case 3: // wrong-protocol square wave, random period != bit time
			p := 5 + rng.Intn(50)
			for k := range codes {
				if (k/p)%2 == 0 {
					codes[k] = 40
				} else {
					codes[k] = 210
				}
			}
		case 4: // random low-frequency steps
			v := 128
			for k := range codes {
				if rng.Intn(18) == 0 {
					v = rng.Intn(256)
				}
				codes[k] = uint8(v)
			}
		}
		for _, baud := range []int{0, 250000, 1000000} {
			r := safeDecodeCAN(t, "fp/garbage", codes, 1e-7, CANFDCfg{NominalBaud: baud, DominantLow: true})
			if confidentValidCAN(r) {
				garbageFP++
				if garbageFP <= 8 {
					t.Logf("garbage confident frame kind=%d baud=%d text=%q", kind, baud, r.Text)
				}
			}
		}
	}
	if garbageFP > 0 {
		t.Errorf("FALSE-POSITIVE: %d garbage inputs decoded as a confident valid CAN frame", garbageFP)
	}

	// --- corrupted CRC: a flipped CRC MUST be flagged ('!'), never accepted ---
	crcFP := 0
	for i := 0; i < 60; i++ {
		spb := 8 + rng.Intn(24)
		dlc := 1 + rng.Intn(8)
		data := make([]int, dlc)
		for k := range data {
			data[k] = rng.Intn(256)
		}
		id := rng.Intn(1 << 11)
		crcInput, _ := canStdFrame(id, dlc, data)
		correct := canCRC15(crcInput)
		bad := correct ^ (1 + rng.Intn(0x7fff))
		if bad == correct {
			continue
		}
		stuffIn := append(append([]int{}, crcInput...), canBitsMSB(bad, 15)...)
		stuffed, rv, rl := canStuffCore(stuffIn)
		if rl == 5 {
			stuffed = append(stuffed, 1-rv)
		}
		wire := append([]int{}, stuffed...)
		wire = append(wire, 1, 0, 1)
		for j := 0; j < 10; j++ {
			wire = append(wire, 1)
		}
		codes := canRender(wire, spb, true, 8, 8)
		ct, cfg := exactBaudCfg(spb, true)
		r := safeDecodeCAN(t, "fp/crc", codes, ct, cfg)
		// The decoder may still emit the frame, but the corrupted CRC must be
		// flagged: it must never reproduce the original payload with a clean CRC.
		if confidentValidCAN(r) && fmtInts(r.Bytes) == fmtInts(data) {
			crcFP++
			if crcFP <= 8 {
				cs := findSpan(r, "crc")
				t.Logf("corrupted CRC accepted: spb=%d id=%X correct=%04X bad=%04X crc=%q", spb, id, correct, bad, cs.Text)
			}
		}
	}
	if crcFP > 0 {
		t.Errorf("FALSE-POSITIVE: %d corrupted-CRC frames silently accepted with a clean CRC", crcFP)
	}

	// --- truncation: a frame cut before/inside the CRC must not be confident --
	truncFP := 0
	for i := 0; i < 60; i++ {
		spb := 8 + rng.Intn(20)
		dlc := 3 + rng.Intn(6)
		data := make([]int, dlc)
		for k := range data {
			data[k] = rng.Intn(256)
		}
		_, wire := canStdFrame(rng.Intn(1<<11), dlc, data)
		codes := canRender(wire, spb, true, 8, 8)
		cut := len(codes) * (30 + rng.Intn(40)) / 100 // 30%..70%
		codes = codes[:cut]
		ct, cfg := exactBaudCfg(spb, true)
		r := safeDecodeCAN(t, "fp/trunc", codes, ct, cfg)
		if confidentValidCAN(r) && fmtInts(r.Bytes) == fmtInts(data) {
			truncFP++
			if truncFP <= 8 {
				t.Logf("truncated frame accepted whole: spb=%d cut=%d text=%q", spb, cut, r.Text)
			}
		}
	}
	if truncFP > 0 {
		t.Errorf("FALSE-POSITIVE: %d truncated frames decoded as the full valid payload", truncFP)
	}

	// --- KNOWN BUG (documented): a Nyquist / aliased-constant "signal" that
	// reads dominant at every bit centre is accepted as a clean all-zeros
	// classic frame. Root cause: the decoder validates neither stuff-bit
	// polarity nor the fixed-form (CRC-delimiter/ACK-delimiter/EOF) bits, and
	// CRC-15 of an all-zero stream is 0, so the all-zero "CRC field" matches.
	// This is a FALSE POSITIVE by the protocol's own integrity definition.
	nyqFP := 0
	var nyqSample string
	for _, spb := range []int{4, 8, 12, 16, 20, 24, 40} { // even spb => stable alias
		codes := make([]uint8, 2000)
		for k := range codes {
			if k%2 == 0 {
				codes[k] = 40 // dominant-low level at even samples
			} else {
				codes[k] = 210
			}
		}
		ct := 1e-7
		baud := int(1.0 / (float64(spb) * ct))
		r := safeDecodeCAN(t, "fp/nyquist", codes, ct, CANFDCfg{NominalBaud: baud, DominantLow: true})
		if confidentValidCAN(r) {
			nyqFP++
			if nyqSample == "" {
				nyqSample = fmt.Sprintf("spb=%d baud=%d text=%q", spb, baud, r.Text)
			}
		}
	}
	if nyqFP > 0 {
		// Non-fatal: pins the real bug but lets the rest of the suite report too.
		t.Errorf("FALSE-POSITIVE (known decoder bug): Nyquist/aliased-constant input accepted as a clean CAN frame in %d/7 even-spb cases; e.g. %s", nyqFP, nyqSample)
	}
}

// 3. EDGE CASES: extreme rates, boundary sample counts, degenerate config.
func breakCANEdge(t *testing.T) {
	// minimum legal bit rate (spb == 3) must still round-trip.
	{
		data := []int{0x00, 0xFF, 0x55, 0xAA}
		_, wire := canStdFrame(0x123, len(data), data)
		spb := 3
		ct, cfg := exactBaudCfg(spb, true)
		codes := canRender(wire, spb, true, 6, 6)
		r := safeDecodeCAN(t, "edge/min-rate", codes, ct, cfg)
		if !r.OK || fmtInts(r.Bytes) != fmtInts(data) {
			t.Errorf("EDGE min-rate spb=3: ok=%v bytes=%v want=%v err=%q", r.OK, r.Bytes, data, r.Error)
		}
	}

	// very high spb (slow bit rate) with a single frame in a long record.
	{
		data := []int{0xDE, 0xAD}
		_, wire := canStdFrame(0x7A5, len(data), data)
		spb := 200
		ct, cfg := exactBaudCfg(spb, true)
		codes := canRender(wire, spb, true, 4, 4)
		r := safeDecodeCAN(t, "edge/max-spb", codes, ct, cfg)
		if !r.OK || fmtInts(r.Bytes) != fmtInts(data) {
			t.Errorf("EDGE max-spb=200: ok=%v bytes=%v want=%v err=%q", r.OK, r.Bytes, data, r.Error)
		}
	}

	// exactly one frame, all-0x00 and all-0xFF payloads.
	for _, fill := range []int{0x00, 0xFF} {
		data := []int{fill, fill, fill, fill, fill, fill, fill, fill}
		_, wire := canStdFrame(0x0F0, len(data), data)
		spb := 20
		ct, cfg := exactBaudCfg(spb, true)
		codes := canRender(wire, spb, true, 8, 8)
		r := safeDecodeCAN(t, "edge/fill", codes, ct, cfg)
		if !r.OK || fmtInts(r.Bytes) != fmtInts(data) {
			t.Errorf("EDGE fill=%02X: ok=%v bytes=%v want=%v err=%q", fill, r.OK, r.Bytes, data, r.Error)
		}
		if cs := findSpan(r, "crc"); cs == nil || hasBang(cs.Text) {
			t.Errorf("EDGE fill=%02X crc flagged/absent: %+v", fill, cs)
		}
	}

	// classic REMOTE (RTR) frame: no data bytes, RTR flagged, clean CRC.
	{
		wire := canStdRemoteFrameB(0x2AA, 5)
		spb := 20
		ct, cfg := exactBaudCfg(spb, true)
		codes := canRender(wire, spb, true, 8, 8)
		r := safeDecodeCAN(t, "edge/rtr", codes, ct, cfg)
		if !r.OK {
			t.Errorf("EDGE rtr: ok=false err=%q", r.Error)
		} else {
			if len(r.Bytes) != 0 {
				t.Errorf("EDGE rtr carries data: %v", r.Bytes)
			}
			if s := findSpan(r, "rtr"); s == nil || s.Text != "RTR" {
				t.Errorf("EDGE rtr flag: got %+v", s)
			}
			if cs := findSpan(r, "crc"); cs == nil || hasBang(cs.Text) {
				t.Errorf("EDGE rtr crc flagged/absent: %+v", cs)
			}
		}
	}

	// shortest possible record + boundary sample counts must not panic and must
	// not confidently decode.
	for _, n := range []int{0, 1, 2, 3, 7, 8} {
		codes := make([]uint8, n)
		for i := range codes {
			codes[i] = uint8(i%2) * 210 // whatever; too short to be a frame
		}
		r := safeDecodeCAN(t, fmt.Sprintf("edge/short-%d", n), codes, 1e-6, CANFDCfg{NominalBaud: 250000, DominantLow: true})
		if confidentValidCAN(r) {
			t.Errorf("EDGE short n=%d decoded as confident frame: %q", n, r.Text)
		}
		// auto-baud too
		safeDecodeCAN(t, fmt.Sprintf("edge/short-auto-%d", n), codes, 1e-6, CANFDCfg{DominantLow: true})
	}

	// degenerate colTimeS / baud combinations must not panic (fuzz-style).
	degCT := []float64{0, -1e-6, 1e-12, 1, 1e6}
	degBaud := []int{0, -1, -1000000, 1, 250000, 1 << 30}
	var wire []int
	{
		_, w := canStdFrame(0x111, 2, []int{0x12, 0x34})
		wire = w
	}
	base := canRender(wire, 20, true, 8, 8)
	for _, ct := range degCT {
		for _, nb := range degBaud {
			for _, db := range degBaud {
				cfg := CANFDCfg{NominalBaud: nb, DataBaud: db, DominantLow: true}
				safeDecodeCAN(t, fmt.Sprintf("edge/deg ct=%g nb=%d db=%d", ct, nb, db), base, ct, cfg)
				// with HaveThr overrides at extremes too
				cfg2 := CANFDCfg{NominalBaud: nb, DominantLow: false, Threshold: -50, HaveThr: true}
				safeDecodeCAN(t, "edge/deg-thr", base, ct, cfg2)
			}
		}
	}

	// a very long record with many back-to-back frames must stay bounded/sane.
	{
		var big []int
		var want []int
		for f := 0; f < 40; f++ {
			data := []int{byte8(f), byte8(f * 7), byte8(f * 13)}
			_, w := canStdFrame(f&0x7ff, 3, data)
			big = append(big, w...)
			want = append(want, data...)
		}
		spb := 8
		ct, cfg := exactBaudCfg(spb, true)
		codes := canRender(big, spb, true, 8, 8)
		r := safeDecodeCAN(t, "edge/long", codes, ct, cfg)
		if !r.OK {
			t.Errorf("EDGE long: ok=false err=%q", r.Error)
		} else if fmtInts(r.Bytes) != fmtInts(want) {
			t.Errorf("EDGE long bytes mismatch: got %d bytes want %d", len(r.Bytes), len(want))
		}
	}
}

func byte8(v int) int { return v & 0xff }
