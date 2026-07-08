package engine

// Serial / protocol trigger: publish only frames whose decoded UART/I2C/SPI
// stream contains an operator-specified byte or address pattern, anchored on the
// match. It is a publish-gate (like the zone trigger, zonemask.go) so it composes
// with whatever edge/qualifier trigger is active, and it runs in the engine — so
// it works headless (LCD-only) and for SINGLE/NORM, not just in the browser.
//
// The decoder (internal/decode) is pure — it takes raw uint8 codes + the
// per-sample interval — so the engine can call it directly (no import cycle; only
// panel imports engine). The trigger carries its OWN decode config, independent
// of the LCD decode strip's config (which lives on the panel).

import (
	"sync"

	"open-sds/app/internal/decode"
)

const (
	SerialOff     = 0
	SerialTrigger = 1

	serialFallback = 60 // AUTO liveness: publish one unmatched frame every N holds

	// protocol ids (match the web/panel decode convention: uart/i2c/spi)
	serUART = 1
	serI2C  = 2
	serSPI  = 3
)

// SerialParams is the self-contained serial-trigger config: the protocol +
// channel roles + decode params to use, and the byte/address pattern to match.
// Copied by value into the engine under ser.mu; Bytes is replaced wholesale by
// the setter (never mutated in place).
type SerialParams struct {
	// Decode settings — mirrored from the operator's live decode config so the
	// trigger decodes IDENTICALLY to the decode strip (the UI does not re-enter
	// these; it sends whatever decode is set to).
	Proto     int     `json:"proto"`  // 0 off, 1 uart, 2 i2c, 3 spi
	ChA       int     `json:"chA"`    // primary channel role: 0=C1 1=C2 (uart line / i2c SCL / spi CLK)
	ChB       int     `json:"chB"`    // secondary channel role (i2c SDA / spi DATA)
	Baud      int     `json:"baud"`   // uart: 0 = auto-infer
	Bits      int     `json:"bits"`   // uart: data bits (0 = decode default 8)
	Parity    string  `json:"parity"` // uart: "none"|"even"|"odd" ("" = none)
	CPOL      bool    `json:"cpol"`
	CPHA      bool    `json:"cpha"`
	MSB       bool    `json:"msb"`       // spi bit order (true = MSB-first)
	Threshold float64 `json:"threshold"` // slice threshold code, used only if HaveThr
	HaveThr   bool    `json:"haveThr"`   // false = decode auto-threshold
	// match spec:
	Addr  int   `json:"addr"`  // i2c 7-bit address; <0 = any
	RW    int   `json:"rw"`    // i2c: 0=write 1=read 2=any
	Bytes []int `json:"bytes"` // data byte sequence to find (empty = any transaction/byte)
}

func (p SerialParams) empty() bool { return p.Proto == 0 }

type serialState struct {
	// LOCK ORDER: e.mu is acquired BEFORE ser.mu (Snapshot nests that way), same
	// rule as zoneMaskState — never take e.mu while holding ser.mu.
	mu     sync.Mutex
	params SerialParams
}

// SetSerialParams installs the match config (copies Bytes).
func (e *Engine) SetSerialParams(p SerialParams) {
	p.Bytes = append([]int(nil), p.Bytes...)
	e.ser.mu.Lock()
	e.ser.params = p
	e.ser.mu.Unlock()
}

// SetSerialMode arms/disarms the serial trigger (SerialOff/SerialTrigger).
func (e *Engine) SetSerialMode(m int) {
	if m != SerialOff && m != SerialTrigger {
		m = SerialOff
	}
	if m != SerialOff && int(e.serialMode.Load()) == SerialOff {
		e.serialMatches.Store(0) // reset the running count on the off→on edge
	}
	e.serialMode.Store(int32(m))
}

// serialQualify decodes the captured record with the armed serial config and
// reports whether the target pattern is present, plus the record SAMPLE index to
// anchor the display on (-1 = don't re-anchor). Engine goroutine; f is the
// producer slot. It runs on EVERY publish candidate while armed — async UART in
// AUTO never edge-locks, so (unlike the zone gate) it must NOT be gated on lock.
func (e *Engine) serialQualify(f *Frame, valid int, sampleS float64) (bool, int) {
	e.ser.mu.Lock()
	p := e.ser.params
	e.ser.mu.Unlock()
	if p.empty() {
		return true, -1 // armed but unconfigured → pass through, don't re-anchor
	}
	if valid < 8 || !(sampleS > 0) { // !(>0) also rejects NaN (every NaN compare is false)
		return false, -1
	}
	chA, chB := f.C1, f.C1
	if p.ChA == 1 {
		chA = f.C2
	}
	if p.ChB == 1 {
		chB = f.C2
	}
	if len(chA) < valid || len(chB) < valid {
		return false, -1
	}
	var res decode.Result
	switch p.Proto {
	case serUART:
		res = decode.DecodeUART(chA[:valid], sampleS, decode.UARTCfg{Baud: p.Baud, Bits: p.Bits, Parity: p.Parity, Threshold: p.Threshold, HaveThr: p.HaveThr})
	case serI2C:
		res = decode.DecodeI2C(chA[:valid], chB[:valid], sampleS, decode.I2CCfg{Threshold: p.Threshold, HaveThr: p.HaveThr})
	case serSPI:
		res = decode.DecodeSPI(chA[:valid], chB[:valid], sampleS, decode.SPICfg{CPOL: p.CPOL, CPHA: p.CPHA, MSB: p.MSB, Threshold: p.Threshold, HaveThr: p.HaveThr})
	default:
		return true, -1
	}
	if !res.OK {
		return false, -1
	}
	if p.Proto == serI2C {
		return matchI2C(res.Spans, p)
	}
	return matchBytes(res.Spans, p.Bytes) // UART / SPI
}

// matchI2C finds a transaction addressing p.Addr (or any if <0) with the wanted
// R/W direction; when p.Bytes is set, the transaction's data bytes must also
// contain that sequence. Anchors on the address span (the transaction start).
func matchI2C(sp []decode.Span, p SerialParams) (bool, int) {
	for i := 0; i < len(sp); i++ {
		if sp[i].Kind != "addr" {
			continue
		}
		if p.Addr >= 0 && sp[i].Val != p.Addr {
			continue
		}
		if p.RW != 2 { // R/W is a separate span right after the address
			if i+1 >= len(sp) || sp[i+1].Kind != "rw" {
				continue
			}
			want := "W"
			if p.RW == 1 {
				want = "R"
			}
			if sp[i+1].Text != want {
				continue
			}
		}
		if len(p.Bytes) == 0 {
			return true, sp[i].I0 // address (+ R/W) match, no data requirement
		}
		// gather this transaction's data bytes (until the next start/stop/addr)
		var vals []int
		for j := i + 1; j < len(sp); j++ {
			switch sp[j].Kind {
			case "start", "stop", "addr":
				j = len(sp) // break out
			case "data":
				vals = append(vals, sp[j].Val)
			}
		}
		if indexSeq(vals, p.Bytes) >= 0 {
			return true, sp[i].I0
		}
	}
	return false, -1
}

// matchBytes finds the wanted byte sequence in the UART/SPI data stream (empty
// want = any decodable byte). Anchors on the first matching byte's sample index.
// Two rules keep it honest: (1) only Kind=="data" spans count — a byte the
// decoder flagged frame-error/parity-error is NOT a valid protocol byte and must
// not satisfy a data trigger (matchI2C is data-only too); (2) a MULTI-byte
// pattern must be CONTIGUOUS on the wire — consecutive matched bytes must abut
// (no large idle gap / burst boundary between them), so "AB" cannot be forged
// from an 'A' and a 'B' emitted far apart in separate transmissions.
func matchBytes(sp []decode.Span, want []int) (bool, int) {
	type db struct{ val, i0, i1 int }
	var seq []db
	for _, s := range sp {
		if s.Kind == "data" {
			seq = append(seq, db{s.Val, s.I0, s.I1})
		}
	}
	if len(want) == 0 {
		if len(seq) == 0 {
			return false, -1
		}
		return true, seq[0].i0 // any valid data byte present
	}
	for start := 0; start+len(want) <= len(seq); start++ {
		ok := true
		for k := 0; k < len(want); k++ {
			if seq[start+k].val != want[k] {
				ok = false
				break
			}
			if k > 0 { // require adjacency: gap ≤ ~2 byte-widths, else it bridged a gap
				w := seq[start+k].i1 - seq[start+k].i0
				if w < 1 {
					w = 1
				}
				if seq[start+k].i0-seq[start+k-1].i1 > 2*w {
					ok = false
					break
				}
			}
		}
		if ok {
			return true, seq[start].i0
		}
	}
	return false, -1
}

// indexSeq returns the start index of the first occurrence of needle in hay, or
// -1. Empty needle matches at 0.
func indexSeq(hay, needle []int) int {
	if len(needle) == 0 {
		return 0
	}
	for start := 0; start+len(needle) <= len(hay); start++ {
		ok := true
		for k := range needle {
			if hay[start+k] != needle[k] {
				ok = false
				break
			}
		}
		if ok {
			return start
		}
	}
	return -1
}
