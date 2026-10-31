// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"math/bits"
	"unsafe"
)

// The slicer's run scans test a machine word of codes at a time: a line
// holds its level for many samples (a stream's idle line for most of them),
// and a compare and branch per sample was half of slicing it. The word is
// loaded aligned (the device's compiler joins no byte loads into one) and
// read little-endian, as both targets are.

const (
	scanWord = bits.UintSize / 8
	scanLow7 = ^uint(0) / 255 * 0x7f // 0x7f in every byte
	scanHigh = ^uint(0) / 255 * 0x80 // 0x80 in every byte
	scanOnes = ^uint(0) / 255        // 0x01 in every byte
)

// scanAtLeast is the first index from from on whose code is k or more,
// len(codes) when none is.
// TRLC-LINKS: REQ-SDS-018
func scanAtLeast(codes []uint8, from, k int) int {
	n := len(codes)
	if k <= 0 {
		return min(from, n)
	}
	if k > 255 {
		return n
	}
	j := from
	for j < n && (uintptr(unsafe.Pointer(unsafe.SliceData(codes)))+uintptr(j))&(scanWord-1) != 0 {
		if int(codes[j]) >= k {
			return j
		}
		j++
	}
	// A byte's 7 low bits plus 128-k (k < 128) carry into its top bit when
	// they reach k, and so do they plus 256-k (k >= 128) when they reach
	// k-128; no sum leaves its byte.
	if k < 128 {
		add := uint(128-k) * scanOnes
		for ; j+scanWord <= n; j += scanWord {
			x := *(*uint)(unsafe.Pointer(&codes[j]))
			if m := ((x & scanLow7) + add | x) & scanHigh; m != 0 {
				return j + bits.TrailingZeros(m)/8
			}
		}
	} else {
		add := uint(256-k) * scanOnes
		for ; j+scanWord <= n; j += scanWord {
			x := *(*uint)(unsafe.Pointer(&codes[j]))
			if m := ((x & scanLow7) + add) & x & scanHigh; m != 0 {
				return j + bits.TrailingZeros(m)/8
			}
		}
	}
	for ; j < n; j++ {
		if int(codes[j]) >= k {
			return j
		}
	}
	return n
}

// scanAtMost is the first index from from on whose code is k or less,
// len(codes) when none is: scanAtLeast on the codes' complements.
// TRLC-LINKS: REQ-SDS-018
func scanAtMost(codes []uint8, from, k int) int {
	n := len(codes)
	if k >= 255 {
		return min(from, n)
	}
	if k < 0 {
		return n
	}
	c := 255 - k // ^v >= c exactly when v <= k
	j := from
	for j < n && (uintptr(unsafe.Pointer(unsafe.SliceData(codes)))+uintptr(j))&(scanWord-1) != 0 {
		if int(codes[j]) <= k {
			return j
		}
		j++
	}
	if c < 128 {
		add := uint(128-c) * scanOnes
		for ; j+scanWord <= n; j += scanWord {
			x := ^*(*uint)(unsafe.Pointer(&codes[j]))
			if m := ((x & scanLow7) + add | x) & scanHigh; m != 0 {
				return j + bits.TrailingZeros(m)/8
			}
		}
	} else {
		add := uint(256-c) * scanOnes
		for ; j+scanWord <= n; j += scanWord {
			x := ^*(*uint)(unsafe.Pointer(&codes[j]))
			if m := ((x & scanLow7) + add) & x & scanHigh; m != 0 {
				return j + bits.TrailingZeros(m)/8
			}
		}
	}
	for ; j < n; j++ {
		if int(codes[j]) <= k {
			return j
		}
	}
	return n
}
