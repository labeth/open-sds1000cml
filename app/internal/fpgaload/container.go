// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

import (
	"bytes"
	"fmt"
	"math/bits"
)

// .rbf container bit-order detection (fpga-specs 05 §3.2/§3.3; the mechanism
// is owned-fpga container.go, commit 5337711). Exactly one of the two byte
// orders in circulation needs a per-byte bit reversal on this loader:
//
//	Quartus quartus_cpf output    native order         MUST be bit-reversed
//	on-NAND factory image         pre-reversed order   ships raw
//
// The wrong order clocks every byte cleanly and CONF_DONE simply never
// asserts (port reads 0x0040). The container says which it is: bytes
// 0x20..0x28 are the Cyclone IV device/option header, and the two orders
// carry disjoint constants that are exact bit reversals of each other.
// Quartus global options (CRC error detection, INIT_DONE, auto-restart) move
// bits in that field; such an image is REFUSED rather than guessed, and the
// operator sets BitOrder + Force on their own authority.

// Order is the container's stored bit order.
// TRLC-LINKS: REQ-SDS-092
type Order int

const (
	OrderUnknown     Order = iota
	OrderNative            // header 6a f7 …: reverse each byte on the wire
	OrderPreReversed       // header 56 ef …: ship raw
)

// TRLC-LINKS: REQ-SDS-092
func (o Order) String() string {
	switch o {
	case OrderNative:
		return "native Quartus order (6a f7 …)"
	case OrderPreReversed:
		return "pre-reversed factory order (56 ef …)"
	}
	return "unrecognised"
}

// Reverse reports whether the loader must bit-reverse each byte.
// TRLC-LINKS: REQ-SDS-092
func (o Order) Reverse() bool { return o == OrderNative }

// BitOrder is the caller's request (Options.BitOrder). Zero = auto-detect.
// TRLC-LINKS: REQ-SDS-092
type BitOrder int

const (
	BitOrderAuto BitOrder = iota
	BitOrderReverse
	BitOrderRaw
)

// TRLC-LINKS: REQ-SDS-092
func (b BitOrder) String() string {
	switch b {
	case BitOrderReverse:
		return "reverse"
	case BitOrderRaw:
		return "raw"
	}
	return "auto"
}

const (
	rbfPreambleLen = 0x20 // all 0xFF
	rbfHeaderOff   = 0x20
	rbfHeaderLen   = 9
	hintMaxBits    = 8 // widest option-bit spread seen is 5 (CRC error detection)
)

var (
	hdrNative      = []byte{0x6A, 0xF7, 0xF7, 0xF7, 0xF7, 0xF7, 0xF7, 0xF3, 0xFB}
	hdrPreReversed = []byte{0x56, 0xEF, 0xEF, 0xEF, 0xEF, 0xEF, 0xEF, 0xCF, 0xDF}
)

// DetectOrder reports the stored bit order of a passive-serial container from
// its device header, or an error explaining why it cannot be trusted.
// TRLC-LINKS: REQ-SDS-092
func DetectOrder(rbf []byte) (Order, error) {
	if len(rbf) < rbfHeaderOff+rbfHeaderLen {
		return OrderUnknown, fmt.Errorf("image is %d bytes: too short for the device header at 0x%02x..0x%02x",
			len(rbf), rbfHeaderOff, rbfHeaderOff+rbfHeaderLen-1)
	}
	for i := 0; i < rbfPreambleLen; i++ {
		if rbf[i] != 0xFF {
			return OrderUnknown, fmt.Errorf("preamble byte 0x%02x is 0x%02x, not 0xff — not a passive-serial container", i, rbf[i])
		}
	}
	hdr := rbf[rbfHeaderOff : rbfHeaderOff+rbfHeaderLen]
	switch {
	case bytes.Equal(hdr, hdrNative):
		return OrderNative, nil
	case bytes.Equal(hdr, hdrPreReversed):
		return OrderPreReversed, nil
	}
	return OrderUnknown, fmt.Errorf("unrecognised device header at 0x%02x: % x (native % x, pre-reversed % x)%s — refusing to guess",
		rbfHeaderOff, hdr, hdrNative, hdrPreReversed, nearestHint(hdr))
}

// TRLC-LINKS: REQ-SDS-092
func nearestHint(hdr []byte) string {
	dN, dR := hamming(hdr, hdrNative), hamming(hdr, hdrPreReversed)
	var o Order
	var d int
	switch {
	case dN < dR:
		o, d = OrderNative, dN
	case dR < dN:
		o, d = OrderPreReversed, dR
	default:
		return ""
	}
	if d > hintMaxBits {
		return fmt.Sprintf("; nearest is the %v header %d bits away — likely not an EP4CE10 image", o, d)
	}
	want := BitOrderRaw
	if o.Reverse() {
		want = BitOrderReverse
	}
	return fmt.Sprintf("; %d bit(s) from the %v header — Quartus global options move bits here; if this is your own build set BitOrder=%v with Force", d, o, want)
}

// TRLC-LINKS: REQ-SDS-092
func hamming(a, b []byte) int {
	n := 0
	for i := range a {
		if i >= len(b) {
			break
		}
		n += bits.OnesCount8(a[i] ^ b[i])
	}
	return n
}

// resolveBitOrder decides the wire order: auto takes the container's word; an
// explicit request must agree with the container unless forced.
// TRLC-LINKS: REQ-SDS-092
func resolveBitOrder(rbf []byte, want BitOrder, force bool) (rev bool, why string, err error) {
	order, derr := DetectOrder(rbf)
	switch {
	case derr == nil && want == BitOrderAuto:
		return order.Reverse(), fmt.Sprintf("auto: %v ⇒ reverse=%v", order, order.Reverse()), nil
	case derr == nil && (want == BitOrderReverse) == order.Reverse():
		return order.Reverse(), fmt.Sprintf("explicit %v, agrees with the container (%v)", want, order), nil
	case derr == nil && !force:
		return false, "", fmt.Errorf("BitOrder=%v contradicts the container (%v); the wrong order clocks cleanly and leaves CONF_DONE low — leave BitOrder unset, or set Force", want, order)
	case derr == nil:
		return want == BitOrderReverse, fmt.Sprintf("explicit %v, FORCED against the container (%v)", want, order), nil
	case want != BitOrderAuto && force:
		return want == BitOrderReverse, fmt.Sprintf("explicit %v, FORCED (container not recognised: %v)", want, derr), nil
	case want != BitOrderAuto:
		return false, "", fmt.Errorf("cannot verify the bit order: %w — set Force to load anyway", derr)
	}
	return false, "", fmt.Errorf("cannot determine the bit order: %w — set BitOrder and Force together to override", derr)
}
