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
// sampling phase difference and any chosen offset), sliced at its own level.
// Frames are split by an idle gap, as the level-sampling receiver did.

// SampleSPIConfig is the receiver's configuration, as its registers hold it.
type SampleSPIConfig struct {
	ClockCh          int   // 0: C1 carries the clock, 1: C2
	Level, Low, High uint8 // the clock's slicing level and re-arm band
	DataLevel        uint8 // the data line's slicing level (register 59)
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

// SPIDataLag is SPI's DataLag for a clock on clockCh (0 C1, 1 C2) in
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
