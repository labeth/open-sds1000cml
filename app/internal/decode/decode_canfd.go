package decode

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// CANFDCfg configures the CAN / CAN-FD decoder.
//
// The input is a single, already single-ended logic line (e.g. CAN_H-CAN_L
// through a transceiver, or a pre-sliced probe). We map the sliced logic level
// to the CAN bus state: dominant = logic 0, recessive = logic 1. Because a
// physical CAN bus drives dominant LOW, DominantLow should be true for standard
// captures (the JS twin defaults it true; in Go the zero value is false so the
// caller must set it — Autodetect/menus pass DominantLow:true).
type CANFDCfg struct {
	NominalBaud int     // arbitration-phase bit rate; 0 => auto-infer from the shortest bit run
	DataBaud    int     // FD data-phase bit rate (used after a recessive BRS); 0 => same as nominal
	DominantLow bool    // true: the dominant bus state is the LOW level (standard CAN)
	Threshold   float64 // slice threshold override
	HaveThr     bool    // honour Threshold instead of the auto midpoint
}

// canCRC15 is the classic-CAN CRC-15 (generator polynomial 0x4599, i.e.
// x^15+x^14+x^10+x^8+x^7+x^4+x^3+1) over the destuffed bit stream from SOF
// through the end of the data field. Register seeded 0, MSB-first.
func canCRC15(bits []int) int {
	crc := 0
	for _, b := range bits {
		next := ((crc >> 14) & 1) ^ (b & 1)
		crc = (crc << 1) & 0x7fff
		if next != 0 {
			crc ^= 0x4599
		}
	}
	return crc & 0x7fff
}

// fdDataLen maps a CAN-FD DLC (0..15) to a byte count (classic 0..8, then the
// FD steps 12/16/20/24/32/48/64).
func fdDataLen(dlc int) int {
	switch {
	case dlc <= 8:
		return dlc
	case dlc == 9:
		return 12
	case dlc == 10:
		return 16
	case dlc == 11:
		return 20
	case dlc == 12:
		return 24
	case dlc == 13:
		return 32
	case dlc == 14:
		return 48
	default:
		return 64
	}
}

// canReader pulls destuffed CAN bits (0=dominant, 1=recessive) from a sliced
// channel. It samples at the middle of each bit cell, advancing pos by spb, and
// removes stuff bits (after 5 identical bits the next wire bit is a stuff bit)
// while stuffOn is set. bits records the destuffed stream while record is set
// (used for the CRC-15 check). All reads are bounds-checked; out of range => ok
// false and the caller aborts the frame.
type canReader struct {
	S           sliced
	dominantLow bool
	pos         float64
	spb         float64
	runVal      int
	runLen      int
	stuffOn     bool
	record      bool
	stuffed     int
	bits        []int
	li0, li1    int // sample span of the last raw bit read
}

// readRaw samples one wire bit at pos+0.5*spb and advances pos by spb.
func (r *canReader) readRaw() (int, bool) {
	center := r.pos + 0.5*r.spb
	r.li0 = int(math.Round(r.pos))
	end := int(math.Round(r.pos+r.spb)) - 1
	if end >= r.S.n {
		end = r.S.n - 1
	}
	if end < r.li0 {
		end = r.li0
	}
	r.li1 = end
	r.pos += r.spb
	lvl := logicAt(r.S, center)
	if lvl < 0 {
		return -1, false
	}
	if r.dominantLow {
		return lvl, true // low(0) = dominant(0)
	}
	return 1 - lvl, true // high = dominant
}

// next returns the next destuffed CAN bit, consuming a preceding stuff bit when
// the running same-bit count has reached 5.
func (r *canReader) next() (int, bool) {
	if r.stuffOn && r.runLen >= 5 {
		sv, ok := r.readRaw()
		if !ok {
			return -1, false
		}
		r.stuffed++
		r.runVal, r.runLen = sv, 1
	}
	v, ok := r.readRaw()
	if !ok {
		return -1, false
	}
	if v == r.runVal {
		r.runLen++
	} else {
		r.runVal, r.runLen = v, 1
	}
	if r.record {
		r.bits = append(r.bits, v)
	}
	return v, true
}

// readField reads nbits destuffed bits MSB-first into an int and reports the
// sample span [i0,i1] covered (first bit start .. last bit end).
func (r *canReader) readField(nbits int) (val, i0, i1 int, ok bool) {
	i0, i1 = -1, -1
	for k := 0; k < nbits; k++ {
		b, o := r.next()
		if !o {
			return 0, i0, i1, false
		}
		if k == 0 {
			i0 = r.li0
		}
		i1 = r.li1
		val = (val << 1) | b
	}
	return val, i0, i1, true
}

type canFrame struct {
	spans []Span
	toks  []string
	bytes []int
	endI  int
	ok    bool
}

// DecodeCANFD decodes classic CAN (fully) and CAN-FD base frames (best-effort:
// ID + control + DLC + data with dynamic destuffing) on one sliced logic line.
func DecodeCANFD(codes []uint8, colTimeS float64, cfg CANFDCfg) Result {
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "canfd", Error: S.reason}
	}

	var spb float64
	if cfg.NominalBaud > 0 {
		if colTimeS <= 0 {
			return Result{Proto: "canfd", Error: "invalid colTimeS for explicit baud"}
		}
		spb = (1.0 / float64(cfg.NominalBaud)) / colTimeS
	} else {
		s, reason := inferCANspb(S)
		if reason != "" {
			return Result{Proto: "canfd", Error: reason}
		}
		spb = s
	}
	if !(spb >= 3) {
		return Result{Proto: "canfd", Error: fmt.Sprintf("%.1f samples/bit; need >= 3", spb)}
	}
	dataSpb := spb
	if cfg.DataBaud > 0 && colTimeS > 0 {
		if ds := (1.0 / float64(cfg.DataBaud)) / colTimeS; ds >= 3 {
			dataSpb = ds
		}
	}

	// A SOF is the first dominant bit after idle (recessive): a falling edge when
	// dominant is low, a rising edge when dominant is high.
	sofDir := -1
	if !cfg.DominantLow {
		sofDir = 1
	}

	var spans []Span
	var toks []string
	var allBytes []int
	frames := 0
	nextAllowed := 0
	for _, e := range S.edges {
		if e.dir != sofDir || e.i < nextAllowed {
			continue
		}
		if frames >= 4096 { // bounded: never spin on a hostile capture
			break
		}
		fr := decodeCANOneFrame(S, cfg, e.x, spb, dataSpb)
		if !fr.ok {
			continue
		}
		frames++
		spans = append(spans, fr.spans...)
		toks = append(toks, fr.toks...)
		allBytes = append(allBytes, fr.bytes...)
		nextAllowed = fr.endI + 1
	}
	if frames == 0 {
		return Result{Proto: "canfd", Error: "no CAN frame found"}
	}
	return Result{OK: true, Proto: "canfd", Spans: spans, Text: strings.Join(toks, " "),
		Bytes: allBytes, SPB: spb, Thr: S.threshold}
}

// inferCANspb estimates the samples-per-bit from the edge-gap distribution: real
// CAN has isolated single-bit runs (bit stuffing caps a run at 5), so the short
// gaps cluster at one bit time. Robust low percentile + refine (as UART).
func inferCANspb(S sliced) (float64, string) {
	var gaps []float64
	for k := 1; k < len(S.edges); k++ {
		if g := float64(S.edges[k].i - S.edges[k-1].i); g >= 2 {
			gaps = append(gaps, g)
		}
	}
	if len(gaps) < 3 {
		return 0, "too few edges / cannot infer baud"
	}
	sort.Float64s(gaps)
	spb := gaps[len(gaps)/10]
	sum, cnt := 0.0, 0
	for _, g := range gaps {
		if math.Abs(g-spb) <= 0.35*spb {
			sum += g
			cnt++
		}
	}
	if cnt > 0 {
		spb = sum / float64(cnt)
	}
	return spb, ""
}

// decodeCANOneFrame decodes a single frame starting at sofStart (the SOF bit
// cell start, in fractional samples). Returns ok=false on any truncation/form
// problem so the caller can skip a false SOF candidate.
func decodeCANOneFrame(S sliced, cfg CANFDCfg, sofStart, spb, dataSpb float64) canFrame {
	r := &canReader{S: S, dominantLow: cfg.DominantLow, pos: sofStart, spb: spb,
		runVal: -1, runLen: 0, stuffOn: true, record: true}
	var fr canFrame
	var spans []Span
	var toks []string

	// SOF — exactly one dominant bit.
	sof, s0, s1, ok := r.readField(1)
	if !ok || sof != 0 {
		return fr
	}
	spans = append(spans, Span{s0, s1, "SOF", "sof", 0})

	// 11-bit base identifier (MSB first).
	baseID, idI0, baseI1, ok := r.readField(11)
	if !ok {
		return fr
	}

	// The bit after the base ID is RTR (standard) or SRR (extended); the next is
	// IDE which disambiguates.
	b1, _, _, ok := r.readField(1)
	if !ok {
		return fr
	}
	ide, _, _, ok := r.readField(1)
	if !ok {
		return fr
	}

	extended := ide == 1
	remote := false
	fd := false
	id := baseID
	idEnd := baseI1

	if !extended {
		// Standard frame. b1 was RTR. Next bit is r0 (classic) or FDF/EDL (FD).
		rtr := b1
		fdf, _, _, ok2 := r.readField(1)
		if !ok2 {
			return fr
		}
		if fdf == 1 {
			// CAN-FD base: res, BRS, ESI, then DLC. FD frames have no RTR.
			fd = true
			if _, _, _, ok3 := r.readField(1); !ok3 { // res (r0)
				return fr
			}
			brs, _, _, ok3 := r.readField(1)
			if !ok3 {
				return fr
			}
			if brs == 1 && dataSpb != spb {
				r.spb = dataSpb // data phase runs at the (faster) data rate
			}
			if _, _, _, ok4 := r.readField(1); !ok4 { // ESI
				return fr
			}
		} else {
			remote = rtr == 1 // r0 read and discarded
		}
	} else {
		// Extended (29-bit) classic frame. b1 was SRR (recessive).
		extID, _, extI1, ok2 := r.readField(18)
		if !ok2 {
			return fr
		}
		id = (baseID << 18) | extID
		idEnd = extI1
		rtr, _, _, ok3 := r.readField(1)
		if !ok3 {
			return fr
		}
		if _, _, _, ok4 := r.readField(1); !ok4 { // r1
			return fr
		}
		if _, _, _, ok5 := r.readField(1); !ok5 { // r0
			return fr
		}
		remote = rtr == 1
	}

	// ID + flag spans.
	idText := fmt.Sprintf("%X", id)
	spans = append(spans, Span{idI0, idEnd, idText, "id", id})
	extVal := 0
	if extended {
		extVal = 1
	}
	spans = append(spans, Span{idI0, idEnd, map[bool]string{true: "EXT", false: "STD"}[extended], "ide", extVal})
	remVal := 0
	remTxt := "DATA"
	if remote {
		remVal, remTxt = 1, "RTR"
	}
	spans = append(spans, Span{idI0, idEnd, remTxt, "rtr", remVal})
	if extended {
		toks = append(toks, "XID:"+idText)
	} else {
		toks = append(toks, "ID:"+idText)
	}
	if remote {
		toks = append(toks, "RTR")
	}
	if fd {
		spans = append(spans, Span{idI0, idEnd, "FD", "fd", 1})
		toks = append(toks, "FD")
	}

	// DLC (4 bits).
	dlc, dl0, dl1, ok := r.readField(4)
	if !ok {
		return fr
	}
	nBytes := dlc
	if fd {
		nBytes = fdDataLen(dlc)
	} else if nBytes > 8 {
		nBytes = 8
	}
	if remote {
		nBytes = 0 // remote frames carry no data regardless of DLC
	}
	spans = append(spans, Span{dl0, dl1, fmt.Sprintf("DLC:%d", dlc), "dlc", dlc})
	toks = append(toks, fmt.Sprintf("DLC:%d", dlc))

	// Data field.
	var bytes []int
	for k := 0; k < nBytes; k++ {
		bval, bi0, bi1, ok2 := r.readField(8)
		if !ok2 {
			return fr
		}
		spans = append(spans, Span{bi0, bi1, FmtByte(bval, "hex"), "data", bval})
		toks = append(toks, FmtByte(bval, "hex"))
		bytes = append(bytes, bval)
	}

	if fd {
		// CAN-FD uses a stuff-count field, a longer CRC with fixed stuff bits, and
		// a distinct CRC-delimiter/ACK form. We stop here (best-effort): ID + DLC +
		// data are recovered with correct dynamic destuffing, and the frame is
		// flagged FD. Classic CRC/ACK parsing below would misread FD trailers.
		fr.spans, fr.toks, fr.bytes = spans, toks, bytes
		fr.endI = int(math.Round(r.pos))
		fr.ok = true
		return fr
	}

	// Classic CRC-15 over the destuffed SOF..data bits.
	want := canCRC15(r.bits)
	r.record = false
	crc, c0, c1, ok := r.readField(15)
	if !ok {
		return fr
	}
	crcTxt := fmt.Sprintf("%04X", crc)
	crcKind := "crc"
	if crc != want {
		crcTxt = "!" + crcTxt // form/CRC mismatch, still emit the frame
	}
	spans = append(spans, Span{c0, c1, "CRC:" + crcTxt, crcKind, crc})
	toks = append(toks, "CRC:"+crcTxt)

	// CRC delimiter — still with stuffing on so a trailing stuff bit (when the CRC
	// ended on a 5-run) is consumed before the fixed-form delimiter. Then stuffing
	// is off for the ACK field.
	if _, _, _, ok2 := r.readField(1); !ok2 {
		// Frame body decoded; just no room for the delimiter/ACK. Keep what we have.
		fr.spans, fr.toks, fr.bytes = spans, toks, bytes
		fr.endI = int(math.Round(r.pos))
		fr.ok = true
		return fr
	}
	r.stuffOn = false
	if ack, a0, a1, ok2 := r.readField(1); ok2 {
		if ack == 0 {
			spans = append(spans, Span{a0, a1, "ACK", "ack", 0})
			toks = append(toks, "ACK")
		} else {
			spans = append(spans, Span{a0, a1, "NAK", "nak", 1})
			toks = append(toks, "NAK")
		}
	}

	fr.spans, fr.toks, fr.bytes = spans, toks, bytes
	fr.endI = int(math.Round(r.pos))
	fr.ok = true
	return fr
}
