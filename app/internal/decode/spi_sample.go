// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import "math"

// The sample-domain SPI receiver the fast FPGA image runs
// (fpga/images/fast/sample_edges.v, spi_sample.v), modelled clock for
// clock so its fixtures check the RTL bit for bit. It works on the
// converters' own codes, four samples a channel per 125 MHz receiver clock:
// C1 samples at even and C2 at odd nanoseconds (spec 12 §4). The clock line's
// sampling edge is the first sample at or past the level after a sample
// beyond the re-arm band; its crossing time is interpolated between the two
// samples around the level, in sixteenths of a nanosecond; the data bit is
// the data line's sample nearest to that time plus a delay (the channels'
// sampling phase difference and any chosen offset), sliced at the level.
// Frames are split by an idle gap, as the level-sampling receiver did.

// SampleSPIConfig is the receiver's configuration, as its registers hold it.
type SampleSPIConfig struct {
	ClockCh          int   // 0: C1 carries the clock, 1: C2
	Level, Low, High uint8 // the slicing level and the clock's re-arm band
	Inverted         bool  // both lines inverted
	CPOL, CPHA, MSB  bool
	// Delay is where the data line is sampled after the clock line's
	// crossing, in sixteenths of a nanosecond on the clock line's sample
	// grid: the clock channel's sampling phase less the data channel's, plus
	// any offset into the bit (-128..127).
	Delay     int
	GapClocks int    // receiver clocks without a sampling edge that end a frame
	Pattern   uint32 // the last Length bytes, the newest lowest
	Length    int    // 0..4; 0 matches every byte
}

// SampleSPIEvent is one event of the receiver, at the receiver clock (from
// the first word) the RTL publishes it on.
type SampleSPIEvent struct {
	Clock int
	Kind  uint8 // 1 frame start, 2 byte, 3 frame end, 4 frame end mid-byte
	Data  uint8
	Match bool // the byte completed the pattern
}

// SampleSPIEdge is one sampling edge: its receiver clock, its crossing in
// sixteenths of a nanosecond from the first sample of the clock line, and
// the data bit taken for it.
type SampleSPIEdge struct {
	Clock int
	Time  int
	Bit   bool
}

// SampleSPILatency is the receiver clocks between a word's arrival and the
// framing stage acting on an edge found in it (sample_edges.v and
// spi_sample.v's pipeline).
const SampleSPILatency = 8

// SPIWideWords pairs SRAM record words ({C2,C1,C2,C1} bytes, the earlier
// pair low) into the receiver's 64-bit words, four samples a channel.
// TRLC-LINKS: REQ-SDS-229
func SPIWideWords(words []uint32) []uint64 {
	out := make([]uint64, len(words)/2)
	for i := range out {
		out[i] = uint64(words[2*i]) | uint64(words[2*i+1])<<32
	}
	return out
}

// sampleOf returns sample j (0..3) of channel ch from a receiver word.
// TRLC-LINKS: REQ-SDS-229
func sampleOf(w uint64, ch, j int) uint8 { return uint8(w >> (16*j + 8*ch)) }

// SampleSPI runs the receiver over the receiver words and returns its events
// and its sampling edges.
// TRLC-LINKS: REQ-SDS-229
func SampleSPI(wide []uint64, c SampleSPIConfig) ([]SampleSPIEvent, []SampleSPIEdge) {
	n := len(wide)
	rising := (c.CPOL == c.CPHA) != c.Inverted
	// The edge stage (sample_edges.v): per receiver clock, at most one
	// sampling edge and the data line at it.
	out := SampleEdges(wide, SampleLineConfig{Ch: c.ClockCh, Rise: rising, Fall: !rising, Level: c.Level, Low: c.Low, High: c.High, Delay: c.Delay})
	strobe := make([]bool, n)
	bit := make([]bool, n)
	var edges []SampleSPIEdge
	for k, o := range out {
		if o.Found {
			d := o.Other != c.Inverted
			strobe[k], bit[k] = true, d
			edges = append(edges, SampleSPIEdge{Clock: k, Time: 128*k + o.Time, Bit: d})
		}
	}
	// The framing stage, SampleSPILatency clocks later: spi_trigger.v's.
	var events []SampleSPIEvent
	validConfig := c.GapClocks >= 2 && c.Length >= 0 && c.Length <= 4
	primed, active, framed := false, false, false
	idle, nbits, count := 0, 0, 0
	shift := uint8(0)
	history := uint32(0)
	for t := 0; t < n; t++ {
		if !validConfig {
			continue
		}
		// Until the first word has passed the pipeline, the framing stage
		// is held in reset: an idle gap is only counted on the line.
		k := t - SampleSPILatency
		if k < 0 {
			continue
		}
		edge, data := primed && strobe[k], bit[k]
		var received uint8
		if c.MSB {
			received = shift<<1 | b2u8(data)
		} else {
			received = b2u8(data)<<7 | shift>>1
		}
		next := history<<8 | uint32(received)
		hit := c.Length == 0 ||
			(c.Length == 1 && next&0xff == c.Pattern&0xff) ||
			(c.Length == 2 && count >= 1 && next&0xffff == c.Pattern&0xffff) ||
			(c.Length == 3 && count >= 2 && next&0xffffff == c.Pattern&0xffffff) ||
			(c.Length == 4 && count >= 3 && next == c.Pattern)
		// Registers update together at the clock: read the old values.
		nPrimed, nActive, nFramed, nIdle, nBits, nCount, nShift, nHistory := true, active, framed, idle, nbits, count, shift, history
		var ev *SampleSPIEvent
		if primed && idle < c.GapClocks {
			nIdle = idle + 1
		}
		if primed && idle >= c.GapClocks {
			nFramed = true
		}
		if edge {
			nIdle = 0
		}
		if active && idle >= c.GapClocks {
			nActive, nBits, nCount, nHistory, nShift = false, 0, 0, 0, 0
			kind := uint8(3)
			if nbits != 0 {
				kind = 4
			}
			ev = &SampleSPIEvent{Clock: t, Kind: kind}
		}
		if edge && (framed || idle >= c.GapClocks) {
			nIdle, nActive = 0, true
			if !active || idle >= c.GapClocks {
				if c.MSB {
					nShift = b2u8(data)
				} else {
					nShift = b2u8(data) << 7
				}
				nBits, nCount, nHistory = 1, 0, 0
				ev = &SampleSPIEvent{Clock: t, Kind: 1}
			} else {
				nShift = received
				nBits = (nbits + 1) & 7
				if nbits == 7 {
					ev = &SampleSPIEvent{Clock: t, Kind: 2, Data: received, Match: hit}
					nHistory = next
					if count < 4 {
						nCount = count + 1
					}
				}
			}
		}
		if ev != nil {
			events = append(events, *ev)
		}
		primed, active, framed, idle, nbits, count, shift, history = nPrimed, nActive, nFramed, nIdle, nBits, nCount, nShift, nHistory
	}
	return events, edges
}

// TRLC-LINKS: REQ-SDS-229
func b2u8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// SampleSPIGap is the idle gap, in receiver clocks, that frames an SPI bus
// clocked at rateHz: one and a half clock periods, and at least two receiver
// clocks more than one period (an edge's clock is quantised to 8 ns).
// TRLC-LINKS: REQ-SDS-229
func SampleSPIGap(rateHz float64) int {
	period := 125e6 / rateHz
	return max(int(math.Round(1.5*period)), int(math.Ceil(period))+2)
}

// ChannelPhase16 is C2's sampling instant after C1's, in sixteenths of a
// nanosecond: one nanosecond by the converters' clocks (spec 12 §4), 1.44 ns
// measured with the two probes on one node (their paths included).
const ChannelPhase16 = 23

// SPIDataLag is DecodeSPI's DataLag for a clock on clockCh (0 C1, 1 C2) in
// samples of sampleS: the data line's samples sit that far before the clock
// line's.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-229
func SPIDataLag(clockCh int, sampleS float64) float64 {
	if !(sampleS > 0) {
		return 0
	}
	lag := ChannelPhase16 / 16e9 / sampleS
	if clockCh == 0 {
		return -lag
	}
	return lag
}
