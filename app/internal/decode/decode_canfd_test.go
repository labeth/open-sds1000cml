package decode

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

// ---- CAN waveform synthesis (mirrors the decoder's stuffing + CRC exactly) ----

// canBitsMSB expands val into nbits bits, most-significant first.
func canBitsMSB(val, nbits int) []int {
	out := make([]int, nbits)
	for k := 0; k < nbits; k++ {
		out[k] = (val >> (nbits - 1 - k)) & 1
	}
	return out
}

// canStuffCore applies CAN bit stuffing (insert one opposite bit after 5
// identical) and reports the final run state, so the caller can add the trailing
// stuff bit that precedes the (unstuffed) CRC delimiter when the CRC ends on a
// 5-run. This is the inverse of the decoder's destuffing in canReader.next().
func canStuffCore(bits []int) (out []int, runVal, runLen int) {
	runVal, runLen = -1, 0
	for _, b := range bits {
		if runLen == 5 {
			sb := 1 - runVal
			out = append(out, sb)
			runVal, runLen = sb, 1
		}
		out = append(out, b)
		if b == runVal {
			runLen++
		} else {
			runVal, runLen = b, 1
		}
	}
	return
}

// canStdFrame builds a classic standard (11-bit) data frame. Returns the
// destuffed SOF..data bits (for assertions) and the full on-wire bit sequence.
func canStdFrame(id, dlc int, data []int) (crcInput, wire []int) {
	crcInput = append(crcInput, 0)                     // SOF (dominant)
	crcInput = append(crcInput, canBitsMSB(id, 11)...) // identifier
	crcInput = append(crcInput, 0)                     // RTR (data frame)
	crcInput = append(crcInput, 0)                     // IDE (standard)
	crcInput = append(crcInput, 0)                     // r0
	crcInput = append(crcInput, canBitsMSB(dlc, 4)...) // DLC
	for _, d := range data {
		crcInput = append(crcInput, canBitsMSB(d, 8)...)
	}
	crc := canCRC15(crcInput)
	stuffIn := append(append([]int{}, crcInput...), canBitsMSB(crc, 15)...)
	stuffed, rv, rl := canStuffCore(stuffIn)
	if rl == 5 {
		stuffed = append(stuffed, 1-rv) // trailing stuff before the CRC delimiter
	}
	wire = append(wire, stuffed...)
	wire = append(wire, 1) // CRC delimiter (recessive)
	wire = append(wire, 0) // ACK slot (dominant = acknowledged)
	wire = append(wire, 1) // ACK delimiter (recessive)
	for i := 0; i < 7+3; i++ {
		wire = append(wire, 1) // EOF (7) + IFS (3)
	}
	return crcInput, wire
}

// canExtFrame builds a classic extended (29-bit) data frame.
func canExtFrame(id29, dlc int, data []int) (crcInput, wire []int) {
	base := (id29 >> 18) & 0x7ff
	ext := id29 & 0x3ffff
	crcInput = append(crcInput, 0)                       // SOF
	crcInput = append(crcInput, canBitsMSB(base, 11)...) // base ID
	crcInput = append(crcInput, 1)                       // SRR (recessive)
	crcInput = append(crcInput, 1)                       // IDE (recessive => extended)
	crcInput = append(crcInput, canBitsMSB(ext, 18)...)  // extended ID
	crcInput = append(crcInput, 0)                       // RTR (data)
	crcInput = append(crcInput, 0)                       // r1
	crcInput = append(crcInput, 0)                       // r0
	crcInput = append(crcInput, canBitsMSB(dlc, 4)...)   // DLC
	for _, d := range data {
		crcInput = append(crcInput, canBitsMSB(d, 8)...)
	}
	crc := canCRC15(crcInput)
	stuffIn := append(append([]int{}, crcInput...), canBitsMSB(crc, 15)...)
	stuffed, rv, rl := canStuffCore(stuffIn)
	if rl == 5 {
		stuffed = append(stuffed, 1-rv)
	}
	wire = append(wire, stuffed...)
	wire = append(wire, 1, 0, 1) // CRC del, ACK, ACK del
	for i := 0; i < 7+3; i++ {
		wire = append(wire, 1)
	}
	return crcInput, wire
}

// canFDStdFrame builds a CAN-FD base-format frame through the data field. FD
// dynamic stuffing covers SOF..data; the trailing recessive run stands in for
// the (unparsed) stuff-count/CRC/ACK/EOF that the best-effort FD path skips.
func canFDStdFrame(id, dlc int, data []int) []int {
	var bits []int
	bits = append(bits, 0)                     // SOF
	bits = append(bits, canBitsMSB(id, 11)...) // ID
	bits = append(bits, 0)                     // RRS (dominant)
	bits = append(bits, 0)                     // IDE (dominant, base format)
	bits = append(bits, 1)                     // FDF/EDL (recessive => FD)
	bits = append(bits, 0)                     // res (r0)
	bits = append(bits, 0)                     // BRS (no rate switch)
	bits = append(bits, 0)                     // ESI
	bits = append(bits, canBitsMSB(dlc, 4)...) // DLC
	for _, d := range data {
		bits = append(bits, canBitsMSB(d, 8)...)
	}
	stuffed, _, _ := canStuffCore(bits)
	wire := append([]int{}, stuffed...)
	for i := 0; i < 24; i++ {
		wire = append(wire, 1) // recessive tail (skipped by the FD best-effort path)
	}
	return wire
}

// canRender lays a wire bit sequence out as sampled codes at spb samples/bit,
// with lead/trail idle (recessive). dominantLow maps recessive(1)->high level.
func canRender(wire []int, spb int, dominantLow bool, lead, trail int) []uint8 {
	lo, hi := uint8(40), uint8(210)
	var codes []uint8
	push := func(bit, count int) {
		v := lo
		recessiveHigh := dominantLow
		if bit == 1 { // recessive
			if recessiveHigh {
				v = hi
			} else {
				v = lo
			}
		} else { // dominant
			if recessiveHigh {
				v = lo
			} else {
				v = hi
			}
		}
		for j := 0; j < count; j++ {
			codes = append(codes, v)
		}
	}
	push(1, lead*spb)
	for _, b := range wire {
		push(b, spb)
	}
	push(1, trail*spb)
	return codes
}

func spanKinds(r Result) string {
	s := ""
	for _, sp := range r.Spans {
		s += sp.Kind + " "
	}
	return s
}

func findSpan(r Result, kind string) *Span {
	for i := range r.Spans {
		if r.Spans[i].Kind == kind {
			return &r.Spans[i]
		}
	}
	return nil
}

func TestDecodeCANFDStandardRoundTrip(t *testing.T) {
	id := 0x123
	// 0x00 then 0xFF forces long same-polarity runs so destuffing IS exercised.
	data := []int{0x00, 0xFF, 0x55, 0xAA, 0x0F, 0x3C}
	dlc := len(data)
	crcInput, wire := canStdFrame(id, dlc, data)

	// The wire must actually contain stuff bits, or the round-trip proves nothing.
	stuffIn := append(append([]int{}, crcInput...), canBitsMSB(canCRC15(crcInput), 15)...)
	stuffed, _, _ := canStuffCore(stuffIn)
	if len(stuffed) <= len(stuffIn) {
		t.Fatalf("test frame has no stuff bits (%d==%d) — not exercising destuffing", len(stuffed), len(stuffIn))
	}

	spb := 40
	colTimeS := 1e-6
	baud := int(1.0 / (float64(spb) * colTimeS))
	codes := canRender(wire, spb, true, 8, 8)

	// explicit baud
	r := DecodeCANFD(codes, colTimeS, CANFDCfg{NominalBaud: baud, DominantLow: true})
	if !r.OK {
		t.Fatalf("decode failed: %s", r.Error)
	}
	if got, want := fmt.Sprintf("%v", r.Bytes), fmt.Sprintf("%v", data); got != want {
		t.Errorf("data bytes: got %v want %v", r.Bytes, data)
	}
	if ids := findSpan(r, "id"); ids == nil || ids.Val != id {
		t.Errorf("id span: got %+v want Val=%#x", ids, id)
	}
	dataSpans := 0
	for _, s := range r.Spans {
		if s.Kind == "data" {
			dataSpans++
		}
	}
	if dataSpans != len(data) {
		t.Errorf("data spans: got %d want %d", dataSpans, len(data))
	}
	if cs := findSpan(r, "crc"); cs == nil {
		t.Errorf("missing crc span")
	} else if hasBang(cs.Text) {
		t.Errorf("crc flagged a mismatch (%q) — decoder CRC-15 disagrees with the synthesized frame", cs.Text)
	}
	kinds := spanKinds(r)
	for _, need := range []string{"sof", "id", "ide", "rtr", "dlc", "data", "crc", "ack"} {
		if !containsWord(kinds, need) {
			t.Errorf("standard frame spans missing %q; got %s", need, kinds)
		}
	}

	// auto baud (infer samples/bit from the edge gaps)
	ra := DecodeCANFD(codes, colTimeS, CANFDCfg{DominantLow: true})
	if !ra.OK {
		t.Fatalf("auto-baud decode failed: %s", ra.Error)
	}
	if got, want := fmt.Sprintf("%v", ra.Bytes), fmt.Sprintf("%v", data); got != want {
		t.Errorf("auto-baud data: got %v want %v", ra.Bytes, data)
	}
	if ra.SPB < 30 || ra.SPB > 50 {
		t.Errorf("auto SPB %.1f not near %d", ra.SPB, spb)
	}
}

func hasBang(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '!' {
			return true
		}
	}
	return false
}

func TestDecodeCANFDExtendedRoundTrip(t *testing.T) {
	id29 := (0x123 << 18) | 0x1ABCD
	data := []int{0xDE, 0xAD, 0xBE, 0xEF}
	_, wire := canExtFrame(id29, len(data), data)
	spb := 32
	colTimeS := 2e-7
	baud := int(1.0 / (float64(spb) * colTimeS))
	codes := canRender(wire, spb, true, 8, 8)

	r := DecodeCANFD(codes, colTimeS, CANFDCfg{NominalBaud: baud, DominantLow: true})
	if !r.OK {
		t.Fatalf("extended decode failed: %s", r.Error)
	}
	if got, want := fmt.Sprintf("%v", r.Bytes), fmt.Sprintf("%v", data); got != want {
		t.Errorf("ext data: got %v want %v", r.Bytes, data)
	}
	if ids := findSpan(r, "id"); ids == nil || ids.Val != id29 {
		t.Errorf("ext id: got %+v want Val=%#x", ids, id29)
	}
	if ide := findSpan(r, "ide"); ide == nil || ide.Text != "EXT" {
		t.Errorf("ide flag: got %+v want EXT", ide)
	}
}

func TestDecodeCANFDInvertedPolarity(t *testing.T) {
	// DominantLow=false: dominant is the HIGH level. Same frame must still decode.
	id := 0x2AA
	data := []int{0x11, 0x22, 0x00, 0x00, 0x00, 0x00}
	_, wire := canStdFrame(id, len(data), data)
	spb := 40
	colTimeS := 1e-6
	baud := int(1.0 / (float64(spb) * colTimeS))
	codes := canRender(wire, spb, false /*dominant high*/, 8, 8)

	r := DecodeCANFD(codes, colTimeS, CANFDCfg{NominalBaud: baud, DominantLow: false})
	if !r.OK {
		t.Fatalf("inverted-polarity decode failed: %s", r.Error)
	}
	if got, want := fmt.Sprintf("%v", r.Bytes), fmt.Sprintf("%v", data); got != want {
		t.Errorf("inverted data: got %v want %v", r.Bytes, data)
	}
}

func TestDecodeCANFDFDBestEffort(t *testing.T) {
	id := 0x0C5
	dlc := 9 // FD DLC 9 => 12 data bytes
	data := []int{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xAA, 0xFF}
	if fdDataLen(dlc) != len(data) {
		t.Fatalf("test data length %d != fdDataLen(%d)=%d", len(data), dlc, fdDataLen(dlc))
	}
	wire := canFDStdFrame(id, dlc, data)
	spb := 40
	colTimeS := 1e-6
	baud := int(1.0 / (float64(spb) * colTimeS))
	codes := canRender(wire, spb, true, 8, 8)

	r := DecodeCANFD(codes, colTimeS, CANFDCfg{NominalBaud: baud, DominantLow: true})
	if !r.OK {
		t.Fatalf("FD decode failed: %s", r.Error)
	}
	if got, want := fmt.Sprintf("%v", r.Bytes), fmt.Sprintf("%v", data); got != want {
		t.Errorf("FD data: got %v want %v", r.Bytes, data)
	}
	if fd := findSpan(r, "fd"); fd == nil {
		t.Errorf("FD frame not flagged; kinds=%s", spanKinds(r))
	}
	if ids := findSpan(r, "id"); ids == nil || ids.Val != id {
		t.Errorf("FD id: got %+v want %#x", ids, id)
	}
}

func TestDecodeCANFDNoPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(4242))
	mk := func(n, kind int) []uint8 {
		s := make([]uint8, n)
		switch kind {
		case 0:
			for i := range s {
				s[i] = uint8(rng.Intn(256))
			}
		case 1:
			for i := range s {
				s[i] = uint8((i % 2) * 255)
			}
		case 2:
			for i := range s {
				s[i] = 128
			}
		case 3:
			v := 128
			for i := range s {
				if rng.Intn(15) == 0 {
					v = rng.Intn(256)
				}
				s[i] = uint8(v)
			}
		}
		return s
	}
	colTimes := []float64{0, 1e-12, 1e-6, 1}
	bauds := []int{0, -1, 500, 25000, 1 << 28}
	for i := 0; i < 400; i++ {
		codes := mk(rng.Intn(4000), rng.Intn(4))
		ct := colTimes[rng.Intn(len(colTimes))]
		cfg := CANFDCfg{
			NominalBaud: bauds[rng.Intn(len(bauds))],
			DataBaud:    bauds[rng.Intn(len(bauds))],
			DominantLow: rng.Intn(2) == 0,
			Threshold:   rng.Float64()*300 - 30,
			HaveThr:     rng.Intn(2) == 0,
		}
		t0 := time.Now()
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("iter %d panicked: %v", i, rec)
				}
			}()
			DecodeCANFD(codes, ct, cfg)
		}()
		if d := time.Since(t0); d > 2*time.Second {
			t.Fatalf("iter %d took %v — decoder DoS on hostile input", i, d)
		}
	}
	// Explicitly exercise the empty/degenerate inputs too.
	for _, c := range [][]uint8{nil, {}, {128}, {0, 255}, make([]uint8, 3)} {
		DecodeCANFD(c, 1e-6, CANFDCfg{NominalBaud: 25000, DominantLow: true})
	}
}
