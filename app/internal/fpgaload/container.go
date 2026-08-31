// SPDX-License-Identifier: MIT
//
// container.go — `.rbf` container auto-detection for the boot-time reload.
//
// WHY THIS FILE EXISTS. The passive-serial path clocks each byte MSB-first
// while Cyclone IV PS shifts configuration data in LSB-first, so exactly one of
// the two `.rbf` byte orders in circulation needs a per-byte bit reversal:
//
//	owned Quartus quartus_cpf output   -> native order   -> MUST be bit-reversed
//	on-NAND factory sds1000_fpga.rbf   -> pre-reversed   -> ships RAW
//
// Getting it wrong is silent: every byte clocks without an error and CONF_DONE
// simply never asserts (the config port reads 0x0040). Bringup used to hardcode
// BitReverse:true, which is right for today's embedded acq.rbf and wrong the
// first time anyone embeds a differently-ordered image — a bug that would look
// like dead silicon, not like a wrong constant.
//
// The container says which order it is in. Bytes 0x20..0x28 are the Cyclone IV
// device/option header, and the two orders carry disjoint constants that are
// exact per-byte bit reversals of one another (0x6A<->0x56, 0xF7<->0xEF,
// 0xF3<->0xCF, 0xFB<->0xDF):
//
//	native        6A F7 F7 F7 F7 F7 F7 F3 FB
//	pre-reversed  56 EF EF EF EF EF EF CF DF
//
// Verified on disk for this change, not taken from the spec:
//
//	reveng-sds1102cml/firmware/sds1000_fpga.rbf   368,011 B  56 ef ...  (sha256 fc6acbae...)
//	fpga/standard/acq.rbf                         368,011 B  6a f7 ...  (sha256 8fa289f4...)
//	  ... plus 530 further Quartus outputs across the workspace, all 6a f7 ...
//
// ⚑ FINDING, and the reason detection REFUSES instead of tolerating drift. Those
// 9 bytes are not a device magic: they carry Quartus *global option* bits. A
// documented option sweep in the workspace produces legitimate EP4CE10
// containers with a different header — enabling CRC error detection gives
// 6a e7 f7 f7 e7 e7 f7 e3 eb, INIT_DONE gives 6a f7 f7 f7 f5 f7 f7 f3 fb,
// auto-restart-off gives 6a f7 f7 f7 f7 f7 f7 f1 fb. Such an image is refused
// here rather than silently accepted: a false accept costs a wrong-order load
// on live silicon, a false refuse costs one explicit option.
//
// This mirrors tools/fpga_reload/container.go in the -fpga tree; the two are
// deliberately byte-for-byte comparable so a change to one is obvious in the
// other.

package fpgaload

import (
	"bytes"
	"fmt"
	"math/bits"
)

// Order is the per-byte bit order a Cyclone IV passive-serial container is
// stored in, relative to what the loader must put on the wire.
type Order int

const (
	// OrderUnknown means the container was not recognised. Never load one.
	OrderUnknown Order = iota
	// OrderNative is Quartus/codec order (header 6a f7 ...): the loader must
	// bit-reverse every byte.
	OrderNative
	// OrderPreReversed is vendor/wire order (header 56 ef ...): the loader
	// ships the bytes raw.
	OrderPreReversed
)

func (o Order) String() string {
	switch o {
	case OrderNative:
		return "native Quartus order (6a f7 …)"
	case OrderPreReversed:
		return "pre-reversed vendor order (56 ef …)"
	default:
		return "unrecognised"
	}
}

// BitRev is the loader setting this container requires.
func (o Order) BitRev() bool { return o == OrderNative }

// BitOrder is the caller's *request*, distinct from Order (the container's
// answer). The zero value is Auto, so a caller that says nothing gets detection
// rather than a guess — that is the whole point.
type BitOrder int

const (
	// BitOrderAuto reads the order out of the container. Default.
	BitOrderAuto BitOrder = iota
	// BitOrderReverse forces a per-byte bit reversal.
	BitOrderReverse
	// BitOrderRaw forces the bytes onto the wire unchanged.
	BitOrderRaw
)

func (b BitOrder) String() string {
	switch b {
	case BitOrderReverse:
		return "reverse"
	case BitOrderRaw:
		return "raw"
	default:
		return "auto"
	}
}

const (
	// rbfPreambleLen is the fixed all-0xFF preamble ahead of the header. The
	// header constant sits immediately after it; a container with a different
	// preamble is rejected, never offset-searched.
	rbfPreambleLen = 0x20
	rbfHeaderOff   = 0x20
	rbfHeaderLen   = 9
)

var (
	hdrNative      = []byte{0x6A, 0xF7, 0xF7, 0xF7, 0xF7, 0xF7, 0xF7, 0xF3, 0xFB}
	hdrPreReversed = []byte{0x56, 0xEF, 0xEF, 0xEF, 0xEF, 0xEF, 0xEF, 0xCF, 0xDF}
)

// DetectOrder reports the bit order of a passive-serial container from its
// device header, or an error describing why it cannot be trusted. It reads bytes
// only; it never guesses.
func DetectOrder(rbf []byte) (Order, error) {
	if len(rbf) < rbfHeaderOff+rbfHeaderLen {
		return OrderUnknown, fmt.Errorf("image is %d bytes: too short to be a passive-serial container "+
			"(the device header lives at 0x%02x..0x%02x, so at least %d bytes are needed) — truncated or not an .rbf",
			len(rbf), rbfHeaderOff, rbfHeaderOff+rbfHeaderLen-1, rbfHeaderOff+rbfHeaderLen)
	}
	for i := 0; i < rbfPreambleLen; i++ {
		if rbf[i] != 0xFF {
			return OrderUnknown, fmt.Errorf("byte 0x%02x of the preamble is 0x%02x, not 0xff: "+
				"a passive-serial container opens with %d 0xff bytes — not an .rbf for this device",
				i, rbf[i], rbfPreambleLen)
		}
	}
	hdr := rbf[rbfHeaderOff : rbfHeaderOff+rbfHeaderLen]
	switch {
	case bytes.Equal(hdr, hdrNative):
		return OrderNative, nil
	case bytes.Equal(hdr, hdrPreReversed):
		return OrderPreReversed, nil
	}
	return OrderUnknown, fmt.Errorf("unrecognised device header at 0x%02x: % x — expected % x (%v) "+
		"or % x (%v)%s. Refusing to guess the bit order",
		rbfHeaderOff, hdr, hdrNative, OrderNative, hdrPreReversed, OrderPreReversed, nearestHint(hdr))
}

// hintMaxBits bounds what counts as a "near miss" worth diagnosing. The widest
// option-bit spread observed in the workspace sweep is 5 bits (CRC error
// detection); 8 leaves headroom for option combinations without dignifying an
// unrelated file (the vendor's sds1000a_al.bin sits 44 bits from the nearest).
const hintMaxBits = 8

// nearestHint appends a diagnosis — never a decision — when the header is close
// to one of the two constants.
func nearestHint(hdr []byte) string {
	dNat, dRev := hammingBytes(hdr, hdrNative), hammingBytes(hdr, hdrPreReversed)
	order, want, d := OrderUnknown, BitOrderAuto, 0
	switch {
	case dNat < dRev:
		order, want, d = OrderNative, BitOrderReverse, dNat
	case dRev < dNat:
		order, want, d = OrderPreReversed, BitOrderRaw, dRev
	default:
		return ""
	}
	if d > hintMaxBits {
		return fmt.Sprintf(". It resembles neither (nearest is the %v header, %d bits away) — "+
			"this is very likely not an EP4CE10 passive-serial image at all", order, d)
	}
	return fmt.Sprintf(". It differs from the %v header in %d bit(s); Quartus global options "+
		"(CRC error detection, INIT_DONE, auto-restart) move bits in this field, so if this is "+
		"your own build set BitOrder=%v with ForceBitOrder", order, d, want)
}

// hammingBytes counts differing bits between two byte slices, over the shorter.
func hammingBytes(a, b []byte) int {
	n := 0
	for i := range a {
		if i >= len(b) {
			break
		}
		n += bits.OnesCount8(a[i] ^ b[i])
	}
	return n
}

// resolveBitOrder decides the wire bit order for rbf. BitOrderAuto takes the
// container's word. An explicit BitOrder that agrees with the container is
// accepted. An explicit BitOrder that disagrees, or any explicit BitOrder on a
// container that cannot be read, is REFUSED unless force is set: an override
// that can silently contradict the image is the trap this file exists to remove.
func resolveBitOrder(rbf []byte, want BitOrder, force bool) (bitrev bool, why string, err error) {
	order, derr := DetectOrder(rbf)
	switch {
	case derr == nil && want == BitOrderAuto:
		return order.BitRev(), fmt.Sprintf("auto — %v ⇒ bitrev=%v", order, order.BitRev()), nil

	case derr == nil && (want == BitOrderReverse) == order.BitRev():
		return order.BitRev(), fmt.Sprintf("explicit %v, agrees with the container (%v)", want, order), nil

	case derr == nil && !force:
		return false, "", fmt.Errorf("BitOrder=%v contradicts the container: this is a %v image and needs BitOrder=%v. "+
			"The wrong order clocks all %d bytes cleanly and leaves CONF_DONE low. "+
			"Leave BitOrder unset to auto-detect, or set ForceBitOrder if you really mean it",
			want, order, bitOrderFor(order), len(rbf))

	case derr == nil:
		return want == BitOrderReverse,
			fmt.Sprintf("explicit %v, FORCED against the container (%v wants %v)", want, order, bitOrderFor(order)), nil

	case want != BitOrderAuto && force:
		return want == BitOrderReverse, fmt.Sprintf("explicit %v, FORCED (container not recognised: %v)", want, derr), nil

	case want != BitOrderAuto:
		return false, "", fmt.Errorf("cannot verify the bit order: %w — set ForceBitOrder to load it anyway on your own authority", derr)

	default:
		return false, "", fmt.Errorf("cannot determine the bit order: %w — set BitOrder and ForceBitOrder together to override", derr)
	}
}

// bitOrderFor is the BitOrder an operator would have to set to match an Order.
func bitOrderFor(o Order) BitOrder {
	if o.BitRev() {
		return BitOrderReverse
	}
	return BitOrderRaw
}
