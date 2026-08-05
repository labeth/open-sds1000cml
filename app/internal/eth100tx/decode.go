package eth100tx

import "errors"

// ---------------------------------------------------------------------------
// RX — reverse the whole chain from 600 MSa/s sample codes back to a MAC frame.
// Each public step is exposed so RTL stage sims can compare against it.
// ---------------------------------------------------------------------------

// DecodeResult carries the recovered frame plus every RX-side stage boundary.
type DecodeResult struct {
	Ternary     []int8      // sliced per-sample ternary (-1/0/+1)
	Symbols     []int8      // CDR-recovered one-per-baud ternary symbols
	ScrambledBits []byte    // MLT-3-decoded scrambled bits
	PlainBits   []byte      // descrambled NRZ bits
	LockOffset  int         // bit index where descrambler declared idle-lock
	CodeGroups  []CodeGroup // aligned 5-bit code groups (from SSD to ESD)
	MIINibbles  []byte      // decoded data nibbles (incl preamble/SFD)
	Frame       []byte      // recovered MAC frame (no preamble/SFD/FCS)
	FCS         uint32      // FCS field as received
	FCSOK       bool        // FCS verified via CRC-32 residue
	Err         error
}

// Slice applies the two-threshold ternary slicer. Thresholds are +/- half the
// nominal amplitude, matching a real 3-level eye.
func Slice(samples []int) []int8 {
	const thr = AmpPos / 2
	out := make([]int8, len(samples))
	for i, s := range samples {
		switch {
		case s >= thr:
			out[i] = +1
		case s <= -thr:
			out[i] = -1
		default:
			out[i] = 0
		}
	}
	return out
}

// recoverSymbols is the CDR: it collapses runs of constant ternary level into
// baud-rate symbols. Transitions occur only at symbol boundaries, so a run of
// L samples at one level spans round(L / T) symbols, where T (samples/symbol)
// is estimated from the shortest runs (the single-symbol runs). This is the
// essential clock-recovery step; the RTL performs it in parallel across a wide
// word, but must produce the identical symbol stream on the golden vectors.
func recoverSymbols(tern []int8) []int8 {
	if len(tern) == 0 {
		return nil
	}
	// Build runs.
	type run struct {
		lvl int8
		n   int
	}
	var runs []run
	cur := tern[0]
	cnt := 1
	for i := 1; i < len(tern); i++ {
		if tern[i] == cur {
			cnt++
		} else {
			runs = append(runs, run{cur, cnt})
			cur = tern[i]
			cnt = 1
		}
	}
	runs = append(runs, run{cur, cnt})

	// Estimate T from the shortest-run cluster (single-symbol runs).
	minRun := runs[0].n
	for _, r := range runs {
		if r.n < minRun {
			minRun = r.n
		}
	}
	sum, k := 0, 0
	for _, r := range runs {
		if r.n <= minRun+1 { // single-symbol cluster (handles the 4/5 split)
			sum += r.n
			k++
		}
	}
	T := float64(sum) / float64(k)

	var syms []int8
	for _, r := range runs {
		m := int(float64(r.n)/T + 0.5)
		if m < 1 {
			m = 1
		}
		for j := 0; j < m; j++ {
			syms = append(syms, r.lvl)
		}
	}
	return syms
}

// mlt3Decode: a level CHANGE between consecutive symbols is a 1-bit, no change
// is a 0-bit. The pre-symbol reference level is 0 (encoder starts there).
func mlt3Decode(syms []int8) []byte {
	bits := make([]byte, len(syms))
	prev := int8(0)
	for i, s := range syms {
		if s != prev {
			bits[i] = 1
		}
		prev = s
	}
	return bits
}

// descramble idle-locks the receive LFSR then removes the keystream. During
// IDLE the 4B5B plaintext is all-ones (/I/ = 11111), so scrambled = key ^ 1,
// i.e. key = scrambled ^ 1. We seed the LFSR from the first 11 (assumed-idle)
// bits, run the recurrence forward, and REQUIRE the following window to
// descramble to all-ones before accepting the lock — this rejects a bad guess
// and pins the load-bearing idle-lock stage.
func descramble(scr []byte) (plain []byte, lockOff int, err error) {
	const need = 11
	const verify = 33 // idle bits that must be all-ones after lock
	if len(scr) < need+verify {
		return nil, 0, errors.New("too few bits to idle-lock the descrambler")
	}
	// Slide a lock point across the leading idle; the first offset that both
	// seeds and verifies wins. Offset 0 works for a clean lead, but sliding
	// makes the model robust to a truncated first idle group.
	for off := 0; off+need+verify <= len(scr) && off < 64; off++ {
		key := make([]byte, len(scr))
		// Seed: key[off..off+10] = scr ^ 1 (idle plaintext = 1).
		for i := 0; i < need; i++ {
			key[off+i] = scr[off+i] ^ 1
		}
		// Forward recurrence from the seed window.
		ok := true
		for i := off + need; i < len(scr); i++ {
			key[i] = key[i-9] ^ key[i-11]
		}
		// Verify the next `verify` bits descramble to idle (all ones).
		for i := off + need; i < off+need+verify; i++ {
			if scr[i]^key[i] != 1 {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		// Backfill key before `off` is impossible (no earlier idle knowledge);
		// descramble only from off onward — SSD/frame always follow the lead.
		plain = make([]byte, len(scr))
		for i := off; i < len(scr); i++ {
			plain[i] = scr[i] ^ key[i]
		}
		// bits before off are unknown idle; mark as 1 (idle) for cleanliness.
		for i := 0; i < off; i++ {
			plain[i] = 1
		}
		return plain, off, nil
	}
	return nil, 0, errors.New("descrambler failed to idle-lock")
}

// align5B finds the /J/K/ SSD (11000 10001) in the descrambled bit stream and
// returns code groups from J up to and including /T/R/ ESD.
func align5B(plain []byte) ([]CodeGroup, error) {
	// Search for J=11000 followed immediately by K=10001.
	jk := []byte{1, 1, 0, 0, 0, 1, 0, 0, 0, 1}
	start := -1
	for i := 0; i+len(jk) <= len(plain); i++ {
		match := true
		for j := range jk {
			if plain[i+j] != jk[j] {
				match = false
				break
			}
		}
		if match {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, errors.New("no /J/K/ start-of-stream delimiter found")
	}
	var cgs []CodeGroup
	for i := start; i+5 <= len(plain); i += 5 {
		var b uint8
		for j := 0; j < 5; j++ {
			b = (b << 1) | plain[i+j]
		}
		cg := decodeCG(b)
		cgs = append(cgs, cg)
		if cg.Label == "R" && len(cgs) >= 2 && cgs[len(cgs)-2].Label == "T" {
			break
		}
	}
	return cgs, nil
}

func decodeCG(b uint8) CodeGroup {
	switch b {
	case codeI:
		return CodeGroup{b, "I"}
	case codeJ:
		return CodeGroup{b, "J"}
	case codeK:
		return CodeGroup{b, "K"}
	case codeT:
		return CodeGroup{b, "T"}
	case codeR:
		return CodeGroup{b, "R"}
	case codeH:
		return CodeGroup{b, "H"}
	case codeQ:
		return CodeGroup{b, "Q"}
	}
	if n, ok := rev4b5b[b]; ok {
		return dataCG(n)
	}
	return CodeGroup{b, "?"} // invalid code group
}

// nibbleOf returns the data nibble a code group carries, mapping J/K back to
// the 0x5,0x5 they replaced in the first preamble octet.
func nibbleOf(cg CodeGroup) (int, bool) {
	switch cg.Label {
	case "J", "K":
		return 0x5, true
	case "I", "T", "R", "H", "Q", "?":
		return 0, false
	}
	if n, ok := rev4b5b[cg.Bits]; ok {
		return n, true
	}
	return 0, false
}

// DecodeSamples runs the full RX chain on 600 MSa/s codes.
func DecodeSamples(samples []int) DecodeResult {
	var r DecodeResult
	r.Ternary = Slice(samples)
	r.Symbols = recoverSymbols(r.Ternary)
	r.ScrambledBits = mlt3Decode(r.Symbols)

	plain, off, err := descramble(r.ScrambledBits)
	if err != nil {
		r.Err = err
		return r
	}
	r.PlainBits = plain
	r.LockOffset = off

	cgs, err := align5B(plain)
	if err != nil {
		r.Err = err
		return r
	}
	r.CodeGroups = cgs

	// Code groups -> nibbles (drop J/K stand-in nibbles too? no: J/K ARE the
	// first preamble octet's nibbles, so keep them as 0x5,0x5). Stop at ESD.
	var nibs []byte
	for _, cg := range cgs {
		if cg.Label == "T" || cg.Label == "R" {
			break
		}
		n, ok := nibbleOf(cg)
		if !ok {
			r.Err = errors.New("invalid code group in data stream: " + cg.Label)
			return r
		}
		nibs = append(nibs, byte(n))
	}
	r.MIINibbles = nibs

	// Nibbles -> octets (low nibble first).
	if len(nibs)%2 != 0 {
		r.Err = errors.New("odd nibble count")
		return r
	}
	octets := make([]byte, 0, len(nibs)/2)
	for i := 0; i+1 < len(nibs); i += 2 {
		octets = append(octets, nibs[i]|(nibs[i+1]<<4))
	}

	// Strip preamble (0x55 x7) + SFD (0xD5).
	pre := 0
	for pre < len(octets) && octets[pre] == 0x55 {
		pre++
	}
	if pre < 7 || pre >= len(octets) || octets[pre] != 0xD5 {
		r.Err = errors.New("preamble/SFD not found")
		return r
	}
	body := octets[pre+1:]
	if len(body) < 4 {
		r.Err = errors.New("frame shorter than FCS")
		return r
	}
	frame := body[:len(body)-4]
	fcsField := body[len(body)-4:]
	r.Frame = frame
	r.FCS = uint32(fcsField[0]) | uint32(fcsField[1])<<8 | uint32(fcsField[2])<<16 | uint32(fcsField[3])<<24

	// FCS check via the published residue: CRC-32/ISO-HDLC (== 802.3 FCS) over
	// (frame||FCS) yields the catalogue residue 0x2144DF1C when the FCS is
	// correct (CRC RevEng catalogue, CRC-32/ISO-HDLC "residue").
	r.FCSOK = CRC32(body) == 0x2144DF1C
	return r
}
