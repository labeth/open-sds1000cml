package analog

import (
	"errors"
	"fmt"
	"os"
)

// Relay-word actuators our stack had never driven, and the raw escape hatches
// for the bits the evidence does not name.
//
// Four actuators sat unreachable because the composer hardcoded their bits:
// channelByte() forced bit0=1 (bandwidth limit always OFF) and bit3=1 (coupling
// always DC), and relayWord() forced byte 2 to 0x70 (trigger coupling always
// DC). This file adds:
//
//	SetBWL          — A6, per-channel 20 MHz bandwidth limit   (byte bit0)
//	SetCouplingHW   — A4/A5, per-channel AC and GND relays     (byte bits 1/3)
//	SetTrigCoupling — A8, trigger-path DC/AC/HFREJ/LFREJ       (byte 2 nibble)
//	SetRelayRaw     — debug: emit an arbitrary absolute 24-bit word
//	SetGainRaw      — debug: emit both gain bytes verbatim
//
// (Actuator IDs per fpga-specs/takeover/13-analog-frontend.md §1.1; work item
// AF-0.4; the four are what its Phase 2 — AF-2.1…AF-2.5 — exists to measure.)
//
// EVERY one of them goes through emitLocked()'s absolute-word discipline: the
// whole 24-bit word is written, never a read-modify-write, and BOTH gain bytes
// are re-emitted from their seeded shadows afterwards. That is the invariant
// that keeps a relay change from collapsing the untouched channel's gain — the
// documented 2026-07-24 failure mode, and AF-0.4's stated hazard.
//
// What is deliberately NOT here, because the evidence does not name it:
//
//   - channel-byte bits 4 and 6, and byte 2's bits [1:0]. Unassigned in the bit
//     map and set in NO captured vendor word (takeover/13-… §2.3). SetRelayRaw
//     is how AF-2.5 walks them; guessing a name for them here would ship an
//     unfalsifiable claim.
//   - the byte 2 trigger-SOURCE field, bits [3:2]. It is HW-REFUTED as a source
//     selector on #716 (fpga-specs/40-… §8.2) and is emitted only because it is
//     part of the absolute word. relayWord() keeps emitting it; naming it would
//     re-introduce a retracted claim.
//   - the CS3 coupling companion 0x12/0x32 (A10). The vendor emits it alongside
//     a coupling change, but its role is [?] and its non-DC value is unsourced
//     (takeover/13-… §3.5). It lives on the GPMC CS3 plane, which this package
//     does not own — CS3 must be staged single-owner through the engine — so
//     SetCouplingHW drives the relay only. AF-2.6 is the step that settles it.

// SetBWL drives a channel's 20 MHz bandwidth-limit relay (per-channel byte
// bit0). on == the limit is ENGAGED, which CLEARS bit0 — the wire sense is
// inverted. Emits the full absolute word and re-emits both gain bytes, which is
// also required for its own sake: the vendor re-runs the V/div gain apply on a
// BWL change, so a BWL toggle without a gain re-emit measures a stale gain
// (spec 06 §6; takeover/13-… §3.4 AFE-4 vector 4).
//
// ⚠ Only the relay-bit WRITE is established. Whether the 20 MHz roll-off is
// electrically present on this clone has never been measured, and no captured
// vendor word anywhere in the corpus has bit0 clear (takeover/13-… §2.3, §3.4).
// This control is the instrument AF-2.1 needs to answer that, not an assertion
// that the answer is yes.
func (f *FrontEnd) SetBWL(ch int, on bool) error {
	if ch < 0 || ch > 1 {
		return fmt.Errorf("analog: bad bwl ch=%d", ch)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bwl[ch] = on
	return f.applyLocked()
}

// BWL reports whether a channel's 20 MHz limit is engaged (default false).
func (f *FrontEnd) BWL(ch int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bwl[ch&1]
}

// SetCouplingHW drives a channel's REAL coupling relay: DC = bit3, AC = bits 1
// and 3 both clear, GND = bit1 with bit3 clear. All three byte values are
// captured vendor words (takeover/13-… §2.3), and the absolute-word emit is
// what makes GND effective — the "GND relay is unpopulated" reading was a
// read-modify-write artefact that left bit3 set (fpga-specs/40-… §8.2).
//
// It is INDEPENDENT of SetCoupling, which is the software display transform:
// this call changes no display behaviour, and SetCoupling changes no relay bit.
// Driving both to the same mode would double-apply AC (a hardware high-pass and
// then a software mean-removal), so a caller that engages the hardware relay
// should normally leave the software transform at CplDC.
func (f *FrontEnd) SetCouplingHW(ch, mode int) error {
	if ch < 0 || ch > 1 {
		return fmt.Errorf("analog: bad hw coupling ch=%d", ch)
	}
	if mode < CplDC || mode > CplGND {
		return fmt.Errorf("analog: bad hw coupling ch=%d mode=%d", ch, mode)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cplHW[ch] = mode
	return f.applyLocked()
}

// CouplingHW reports a channel's hardware coupling relay state (default CplDC).
func (f *FrontEnd) CouplingHW(ch int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cplHW[ch&1]
}

// SetTrigCoupling drives the trigger-path coupling nibble (relay byte 2, bits
// [7:4]): TrigCplDC/AC/HFREJ/LFREJ → 0x7/0x5/0xf/0x4. All four are captured
// vendor words under their own TRCP command markers (takeover/13-… §2.3), and
// unlike the channel AC relay the trigger path's AC/LFREJ relays are already
// [BENCH]-proven to behave like a high-pass on this board
// (fpga-specs/26-trigger.md §2.4). It applies to the trigger comparator's input,
// not to a channel, so it takes no channel argument.
func (f *FrontEnd) SetTrigCoupling(mode int) error {
	if mode < TrigCplDC || mode > TrigCplLFREJ {
		return fmt.Errorf("analog: bad trigger coupling mode=%d", mode)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trigCpl = mode
	return f.applyLocked()
}

// TrigCoupling reports the trigger-path coupling mode (default TrigCplDC).
func (f *FrontEnd) TrigCoupling() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.trigCpl
}

// ---- raw escape hatches (debug-gated) -----------------------------------

// RawDebugEnv arms the raw escape hatches when set to "1" in the app's
// environment. They are gated because they are the one way to put a word on the
// relay latch that no named control can express — which is exactly what AF-2.5's
// unassigned-bit walk needs, and exactly what should not be reachable from a
// stray API call during an ordinary session.
const RawDebugEnv = "SCOPE_ANALOG_RAW"

// ErrRawDisabled is returned by SetRelayRaw/SetGainRaw when the hatches are not
// armed (see RawDebugEnv / SetRawDebug).
var ErrRawDisabled = errors.New("analog: raw front-end writes disabled (set " + RawDebugEnv + "=1)")

func rawDebugFromEnv() bool { return os.Getenv(RawDebugEnv) == "1" }

// SetRawDebug arms or disarms the raw escape hatches at runtime.
func (f *FrontEnd) SetRawDebug(on bool) {
	f.mu.Lock()
	f.rawDbg = on
	f.mu.Unlock()
}

// RawDebug reports whether the raw escape hatches are armed.
func (f *FrontEnd) RawDebug() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rawDbg
}

// Relay-word structural bits, used by CheckRelayRaw.
const (
	relayWordMask = 0x00ffffff // the latch is 24 bits; the 4th SPI byte is padding
	chEnableBit   = 0x20       // bit5, constant enable, set in every captured word
	chAddrBit     = 0x80       // bit7, CH2 channel-address bit
)

// CheckRelayRaw validates a raw 24-bit relay word. It rejects only the words
// that break the latch's own ADDRESSING — a word wider than 24 bits, a cleared
// bit5, or channel-address bits that would send both bytes to one channel's
// latch (which is how a channel's gain gets collapsed). It deliberately does
// NOT constrain bits 0–4, 6 or byte 2: walking those is the entire point of the
// hatch (AF-2.5), and a validator that encoded a guess about them would defeat
// the experiment it exists to enable.
func CheckRelayRaw(word uint32) error {
	if word&^uint32(relayWordMask) != 0 {
		return fmt.Errorf("analog: relayraw %#x is not a 24-bit word", word)
	}
	b0, b1 := uint8(word), uint8(word>>8)
	if b0&chEnableBit == 0 || b1&chEnableBit == 0 {
		return fmt.Errorf("analog: relayraw %#06x: bit5 (constant enable) must be set in both channel bytes", word)
	}
	if b0&chAddrBit != 0 {
		return fmt.Errorf("analog: relayraw %#06x: CH1 byte must have bit7 (CH2 address) clear", word)
	}
	if b1&chAddrBit == 0 {
		return fmt.Errorf("analog: relayraw %#06x: CH2 byte must have bit7 (CH2 address) set", word)
	}
	return nil
}

// SetRelayRaw emits one arbitrary ABSOLUTE 24-bit relay word, then the ~400 µs
// settle and BOTH gain bytes from their seeded shadows — the same emitLocked
// discipline every named control uses. The word is absolute by construction:
// the caller supplies all 24 bits, nothing is read back, and nothing is merged.
//
// It does NOT update the shadows, so the state it puts on the latch is
// transient: the next SetVdiv/SetBWL/SetCouplingHW/SetTrigCoupling recomposes
// the word from the named state and overwrites it. That is the intended
// restore path for a bit walk (emit probe word → measure → emit base word, or
// simply re-apply a named control).
//
// ⚠ The caller owns both channel bytes. A raw word must carry the OTHER
// channel's intended byte too; that is what "absolute" means here, and it is
// what CheckRelayRaw's address checks make hard to get catastrophically wrong.
func (f *FrontEnd) SetRelayRaw(word uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.rawDbg {
		return ErrRawDisabled
	}
	if err := CheckRelayRaw(word); err != nil {
		return err
	}
	return f.emitLocked(word)
}

// SetGainRaw emits BOTH gain bytes verbatim as two CS-framed single-byte
// transfers on spidev1.1, CH2 first then CH1 — the vendor's order, independently
// confirmed by the corpus (takeover/13-… §2.3). Both bytes always go out: a
// single-byte emit is the shape that collapsed the gain on 2026-07-24.
//
// Like SetRelayRaw it does not update the shadows, so the next V/div change
// restores the calibrated ladder codes. It touches no relay, so it needs no
// settle. ⚠ A zero gain byte silences that channel — the vendor itself emits one
// at start-up, and re-emitting it from an unseeded state is the documented
// collapse; that is a legitimate thing for a bench experiment to send, and it is
// the caller's job to know it.
func (f *FrontEnd) SetGainRaw(ch2, ch1 uint8) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.rawDbg {
		return ErrRawDisabled
	}
	if err := f.tr.WriteGain(ch2, ch1); err != nil {
		return err
	}
	f.emitted = true
	return nil
}
