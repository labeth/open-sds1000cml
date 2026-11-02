// ENGMODEL-OWNER-UNIT: FU-APP-DECODE

package decode

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
	out := SampleEdges(wide, SampleLineConfig{Ch: c.ClockCh, Rise: rising, Fall: !rising, Level: c.Level, Low: c.Low, High: c.High, OtherLevel: c.DataLevel, Delay: c.Delay})
	found := 0
	for k := range out {
		if out[k].Found {
			found++
		}
	}
	edges := make([]SampleSPIEdge, 0, found)
	for k, o := range out {
		if o.Found {
			edges = append(edges, SampleSPIEdge{Clock: k, Time: 128*k + o.Time, Bit: o.Other != c.Inverted})
		}
	}
	// A clock's strobe and data bit are its extractor output's.
	var events []SampleSPIEvent
	// The framing stage, SampleSPILatency clocks later: spi_trigger.v's.
	validConfig := c.GapClocks >= 2 && c.Length >= 0 && c.Length <= 4
	primed, active, framed := false, false, false
	idle, nbits, count := 0, 0, 0
	shift := uint8(0)
	history := uint32(0)
	for t := range n {
		if !validConfig {
			continue
		}
		// Until the first word has passed the pipeline, the framing stage
		// is held in reset: an idle gap is only counted on the line.
		k := t - SampleSPILatency
		if k < 0 {
			continue
		}
		edge, data := primed && out[k].Found, out[k].Found && out[k].Other != c.Inverted
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
