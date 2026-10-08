// ENGMODEL-OWNER-UNIT: FU-APP-SUPERRES
package superres

import "fmt"

// FPGAStackWords is the number of little-endian 16-bit words in one channel/bin.
// TRLC-LINKS: REQ-SDS-141
const FPGAStackWords = 20

// FPGAStackMoments preserves integer state without float64 rounding. Limbs are
// little-endian: Sum and SumOdd have 69 Q24 bits; SumSquares has 106 Q48 bits.
// The odd-hit subset supplies the independent half-stack statistics.
// TRLC-LINKS: REQ-SDS-141
type FPGAStackMoments struct {
	Sum, SumSquares, SumOdd [2]uint64
	Count, CountOdd         uint32
}

// EncodeFPGAMoments matches stack_tile_transfer's 308-bit layout. This checks
// representation bounds, not whether the statistical state is physically valid.
// TRLC-LINKS: REQ-SDS-141
func EncodeFPGAMoments(m FPGAStackMoments) ([FPGAStackWords]uint16, error) {
	var words [FPGAStackWords]uint16
	if m.Sum[1]>>5 != 0 || m.SumOdd[1]>>5 != 0 || m.SumSquares[1]>>42 != 0 {
		return words, fmt.Errorf("FPGA stack moment exceeds wire width")
	}
	putFPGAField(&words, 0, 69, m.Sum)
	putFPGAField(&words, 69, 106, m.SumSquares)
	putFPGAField(&words, 175, 32, [2]uint64{uint64(m.Count), 0})
	putFPGAField(&words, 207, 69, m.SumOdd)
	putFPGAField(&words, 276, 32, [2]uint64{uint64(m.CountOdd), 0})
	return words, nil
}

// DecodeFPGAMoments rejects truncated/extended records and reserved padding.
// Callers still need capture/tile identity and ownership checks before use.
// TRLC-LINKS: REQ-SDS-141
func DecodeFPGAMoments(words []uint16) (FPGAStackMoments, error) {
	if len(words) != FPGAStackWords {
		return FPGAStackMoments{}, fmt.Errorf("FPGA stack state has %d words, want %d", len(words), FPGAStackWords)
	}
	if words[19]&0xfff0 != 0 {
		return FPGAStackMoments{}, fmt.Errorf("FPGA stack state has nonzero reserved padding")
	}
	return FPGAStackMoments{Sum: getFPGAField(words, 0, 69), SumSquares: getFPGAField(words, 69, 106),
		Count: uint32(getFPGAField(words, 175, 32)[0]), SumOdd: getFPGAField(words, 207, 69), CountOdd: uint32(getFPGAField(words, 276, 32)[0])}, nil
}

// TRLC-LINKS: REQ-SDS-141
func putFPGAField(words *[FPGAStackWords]uint16, offset, width int, value [2]uint64) {
	for bit := 0; bit < width; bit++ {
		if value[bit/64]&(uint64(1)<<uint(bit%64)) != 0 {
			p := offset + bit
			words[p/16] |= uint16(1) << uint(p%16)
		}
	}
}

// TRLC-LINKS: REQ-SDS-141
func getFPGAField(words []uint16, offset, width int) (value [2]uint64) {
	for bit := 0; bit < width; bit++ {
		p := offset + bit
		if words[p/16]&(uint16(1)<<uint(p%16)) != 0 {
			value[bit/64] |= uint64(1) << uint(bit%64)
		}
	}
	return value
}
