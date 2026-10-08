// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"open-sds/app/internal/decode"
	"open-sds/app/internal/sramcapture"
)

// PatternElem is one trigger pattern token (ADR-PROTOCOL-SEQUENCE-TRIGGER):
// Kind 2 is a decoded unit (byte, word or ARINC data field) equal to Value
// under Mask, in the decoder's own units; Mask 0 accepts any value. Kind 4 is
// a decoder-reported error (CRC, parity, framing, stuffing or coding).
// TRLC-LINKS: REQ-SDS-013
type PatternElem struct {
	Kind  int `json:"kind"`
	Value int `json:"value"`
	Mask  int `json:"mask"`
}

// Pattern token kinds.
const (
	patternData  = 2
	patternError = 4
)

// nativeLimit is the longest plain pattern the protocol's own hardware
// matcher takes; longer ones and any Pattern use the sequence trigger.
// TRLC-LINKS: REQ-SDS-013
func nativeLimit(proto int) int {
	switch proto {
	case serMIL1553, serARINC:
		return 1
	case serCAN, serFlexRay:
		return 8
	}
	return 4
}

// sequenceNeeded reports a request beyond the protocol's native matcher.
// TRLC-LINKS: REQ-SDS-013
func sequenceNeeded(p SerialParams) bool {
	return len(p.Pattern) != 0 || len(p.Bytes) > nativeLimit(p.Proto)
}

// patternOf is the request as tokens: Pattern, or Bytes as exact units.
// TRLC-LINKS: REQ-SDS-013
func patternOf(p SerialParams) []PatternElem {
	if len(p.Pattern) != 0 {
		return p.Pattern
	}
	out := make([]PatternElem, len(p.Bytes))
	for i, b := range p.Bytes {
		out[i] = PatternElem{Kind: patternData, Value: b, Mask: unitMask(p)}
	}
	return out
}

// unitMask covers one decoded unit of the protocol.
// TRLC-LINKS: REQ-SDS-013
func unitMask(p SerialParams) int {
	switch p.Proto {
	case serMIL1553:
		return 0xffff
	case serARINC:
		return 0x7ffff
	case serSENT:
		return 0xf
	case serManchester:
		bits := p.Bits
		if bits <= 0 || bits > 16 {
			bits = 8
		}
		return 1<<bits - 1
	}
	return 0xff
}

// framedProtocol decoders validate whole frames; a data sequence on them
// triggers only when its frame ends valid, as their native matchers do.
// TRLC-LINKS: REQ-SDS-013
func framedProtocol(proto int) bool {
	switch proto {
	case serCAN, serFlexRay, serUSB, serSENT, serManchester, serARINC:
		return true
	}
	return false
}

// planSequence maps the request into the hardware's event values: ARINC's
// data field sits at bits 10..28 of its word, and an I2C data unit requires
// the address flag (bit 8) clear. Length zero means unplannable.
// TRLC-LINKS: REQ-SDS-013
func planSequence(p SerialParams) sramcapture.SequenceTriggerConfig {
	var c sramcapture.SequenceTriggerConfig
	elems := patternOf(p)
	if len(elems) == 0 || len(elems) > sramcapture.SequenceMax {
		return c
	}
	dataOnly := true
	for i, e := range elems {
		switch e.Kind {
		case patternError:
			dataOnly = false
			c.Elements[i] = sramcapture.SequenceElement{Kind: sramcapture.SequenceError}
		case patternData:
			if e.Value < 0 || e.Mask < 0 || e.Value&^unitMask(p) != 0 || e.Mask&^unitMask(p) != 0 {
				return sramcapture.SequenceTriggerConfig{}
			}
			value, mask := uint32(e.Value&e.Mask), uint32(e.Mask)
			switch p.Proto {
			case serARINC:
				value, mask = value<<10, mask<<10
			case serI2C:
				mask |= 0x100
			}
			c.Elements[i] = sramcapture.SequenceElement{Kind: sramcapture.SequenceData, Value: value, Mask: mask}
		default:
			return sramcapture.SequenceTriggerConfig{}
		}
	}
	c.Length = uint8(len(elems))
	c.Qualify = dataOnly && framedProtocol(p.Proto)
	c.EndBadValue = p.Proto == serManchester || p.Proto == serARINC
	return c
}

// matchSequence is the software form of the sequence trigger over decoded
// spans, in the decoder's units: data spans are units, error spans are
// errors, and packet boundaries break contiguity. On framed protocols a data
// sequence counts only in a packet without errors, like matchPackets. I2C
// counts only data in transactions whose address and direction match.
// TRLC-LINKS: REQ-SDS-013
func matchSequence(sp []decode.Span, p SerialParams) (bool, int) {
	elems := patternOf(p)
	if len(elems) == 0 {
		return false, -1
	}
	dataOnly := true
	for _, e := range elems {
		dataOnly = dataOnly && e.Kind == patternData
	}
	qualify := dataOnly && framedProtocol(p.Proto)
	state := make([]int, len(elems)) // start index of each partial match, -1 none
	reset := func() {
		for k := range state {
			state[k] = -1
		}
	}
	reset()
	selected := p.Proto != serI2C
	bad, hit := false, -1
	finish := func() (bool, int) {
		if hit >= 0 && !bad {
			return true, hit
		}
		bad, hit = false, -1
		return false, -1
	}
	for i, s := range sp {
		kind := 0
		switch s.Kind {
		case "data":
			kind = patternData
		case "frame-error", "parity-error":
			kind, bad = patternError, true
		case "gap", "start", "stop", "sof", "sync":
			reset()
			if qualify {
				if ok, at := finish(); ok {
					return true, at
				}
			}
			if p.Proto == serI2C && s.Kind == "stop" {
				selected = false
			}
			continue
		case "addr":
			if p.Proto != serI2C {
				continue
			}
			reset()
			selected = (p.Addr < 0 || s.Val == p.Addr) && (p.RW == 2 || i+1 < len(sp) && sp[i+1].Kind == "rw" && sp[i+1].Text == map[int]string{0: "W", 1: "R"}[p.RW])
			continue
		default:
			continue
		}
		if kind == patternData && !selected {
			reset()
			continue
		}
		for k := len(elems) - 1; k >= 0; k-- {
			e := elems[k]
			ok := e.Kind == kind && (kind != patternData || (s.Val^e.Value)&e.Mask == 0)
			start := i
			if k > 0 {
				start = state[k-1]
			}
			if ok && start >= 0 {
				state[k] = start
			} else {
				state[k] = -1
			}
		}
		if at := state[len(elems)-1]; at >= 0 {
			if !qualify {
				return true, sp[at].I0
			}
			if hit < 0 {
				hit = sp[at].I0
			}
		}
	}
	if qualify {
		return finish()
	}
	return false, -1
}
