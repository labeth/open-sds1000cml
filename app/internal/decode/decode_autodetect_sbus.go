// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import "math"

// Futaba S.BUS is a UART at 100 kBd, 8 data bits, even parity and two stop
// bits, sent inverted: the line idles low. The UART search reads idle-high
// lines at a rate it infers, so an S.BUS line is tried on its own framing,
// only when its timing allows (bits of about 10 us, a low idle), and named
// only on its frames: 25 bytes from 0x0F with a valid end byte, repeating
// every 7 or 14 ms. A UART, LIN, DMX512, MIDI or Modbus line holds no such
// frames on that framing.

const (
	sbusBaud     = 100000
	sbusFrameLen = 25
)

// sbusBitUs is S.BUS's bit time in microseconds.
const sbusBitUs = 1e6 / sbusBaud

// considerSBUS tries the S.BUS framing on a line whose timing t allows it
// and passes the decode on when its frames prove it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-244, REQ-SDS-247
func considerSBUS(codes []uint8, colTimeS float64, t lineTiming, format string, consider func(Result)) {
	// the shortest run one bit, the idle (the longest run, low) at least a
	// millisecond between frames of 3 ms: an idle-high line is no S.BUS
	if !near(t.minRun, sbusBitUs, 0.2) || t.maxLow < 1000 || t.maxLow < 4*t.maxHigh {
		return
	}
	r := DecodeUART(codes, colTimeS, UARTCfg{Baud: sbusBaud, Bits: 8, Parity: "even", Inverted: true, Format: format})
	if !r.OK || sbusFrames(r, colTimeS) < 2 {
		return
	}
	r.Baud, r.Parity, r.Inverted = sbusBaud, "even", true
	consider(r)
}

// sbusEnd reports an S.BUS frame's end byte: 0x00, or S.BUS2's telemetry
// slot markers 0x04, 0x14, 0x24 and 0x34.
// TRLC-LINKS: REQ-SDS-244
func sbusEnd(b int) bool { return b == 0x00 || (b&0x0f == 0x04 && b>>4 <= 3) }

// sbusFrames counts the S.BUS frames of a decode on S.BUS's framing: the
// bursts of bytes back to back, each 25 bytes from 0x0F with a valid end
// byte and no parity or stop error, their starts 7 or 14 ms apart. A burst
// the record cuts (the first or the last) is not counted; any other that is
// no frame, or a spacing S.BUS does not keep, makes the line no S.BUS (0).
// TRLC-LINKS: REQ-SDS-244, REQ-SDS-247
func sbusFrames(r Result, colTimeS float64) int {
	if !(colTimeS > 0) {
		return 0
	}
	bit := sbusBitUs * 1e-6 / colTimeS // samples a bit
	var bursts [][]Span
	for _, s := range r.Spans {
		if s.Kind == "gap" {
			continue
		}
		if n := len(bursts); n > 0 {
			last := bursts[n-1]
			if float64(s.I0-last[len(last)-1].I0) <= 40*bit { // bytes 12 bits apart; frames over 100
				bursts[n-1] = append(last, s)
				continue
			}
		}
		bursts = append(bursts, []Span{s})
	}
	var starts []float64
	for k, b := range bursts {
		cut := k == 0 || k == len(bursts)-1
		good := len(b) == sbusFrameLen && b[0].Val == 0x0f && sbusEnd(b[len(b)-1].Val)
		for _, s := range b {
			good = good && s.Kind == "data"
		}
		if !good {
			if cut && len(b) < sbusFrameLen {
				continue
			}
			return 0
		}
		starts = append(starts, float64(b[0].I0)*colTimeS)
	}
	for k := 1; k < len(starts); k++ {
		dt := (starts[k] - starts[k-1]) * 1e3 // ms
		if math.Abs(dt-7) > 1 && math.Abs(dt-14) > 2 {
			return 0
		}
	}
	return len(starts)
}
