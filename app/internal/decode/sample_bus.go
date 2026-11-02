// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// The fast image's sample-domain edge extractor (fpga/images/fast
// sample_edges.v), modelled clock for clock, and the UART and I2C receivers
// on it (uart_sample.v, i2c_sample.v); SampleSPI is the SPI receiver on it.
// The extractor works on the converters' own codes, four samples a channel
// per 125 MHz receiver clock: C1 samples at even and C2 at odd nanoseconds
// (spec 12 §4). A crossing is the first sample at or past the level after a
// sample beyond the re-arm band, in each direction asked for, at most one a
// clock; a direction re-arms only once the line has been beyond the band's
// other edge since its last crossing (a Schmitt trigger), so the converters'
// offsets and noise on a slow edge make one crossing, not several, or has
// stayed beyond the band's own edge for a whole clock (after a runt pulse); it is timed by linear interpolation between the two samples around
// the level, in sixteenths of a nanosecond; the other line's level (sliced at
// that line's own level) is taken at the sample nearest that time plus a delay; and the line's own levels
// come out with it, for a receiver that samples the line at times of its own.

// SampleLineConfig is one extractor's configuration, as its registers hold it.
type SampleLineConfig struct {
	Ch               int  // 0: C1, 1: C2
	Rise, Fall       bool // the crossings found
	Level, Low, High uint8
	// OtherLevel slices the other line where it is read (that line's own
	// level: the channels may sit at different offsets and V/div).
	OtherLevel uint8
	// Delay is where the other line is read after a crossing, in sixteenths
	// of a nanosecond on this line's sample grid (-128..127).
	Delay int
}

// SampleLineEdge is the extractor's output for one receiver clock k (the
// word it came from): a crossing in it, its time after the clock's first
// sample of the line (-32..96 sixteenths of a nanosecond), its direction,
// the other line's level at that time plus the delay, and the line's own
// four levels (at or above the level) in the clock.
type SampleLineEdge struct {
	Found  bool
	Time   int
	Rising bool
	Other  bool
	Levels [4]bool
}

// SampleBusLatency is the receiver clocks between a word's arrival and a
// receiver acting on the extractor's output for it.
const SampleBusLatency = SampleSPILatency

// SampleUARTLatency is SampleBusLatency and the clock the UART receiver
// takes to work out what a crossing gives on its own (its half bit).
const SampleUARTLatency = SampleBusLatency + 1

// SampleI2CLatency is SampleBusLatency and the clock the I2C receiver
// takes to put two lines' crossings in time order.
const SampleI2CLatency = SampleBusLatency + 1

// SampleBusEvent is one decoded-event output of the UART or I2C receiver on
// the samples, at the receiver clock (from the first word) the RTL publishes
// it on. Kinds: 1 START, 2 data, 3 STOP, 4 error (UART: framing or parity).
// Meta is I2C's {NACK, address byte}.
type SampleBusEvent struct {
	Clock int
	Kind  uint8
	Data  uint16
	Meta  uint8
	Match bool
}

// SampleUARTConfig is the UART receiver's configuration, as its registers
// hold it.
type SampleUARTConfig struct {
	Ch               int
	Level, Low, High uint8
	Inverted         bool // idle low
	// Period16 is the bit time in sixteenths of a nanosecond: at least 256
	// (16 ns, 62.5 MBd), below 2^24 (1.05 ms).
	Period16 int
	Bits     int  // data bits, 5..9, LSB first
	Parity   int  // 0 none, 1 even, 2 odd
	Resync   bool // every edge inside a frame re-times the following bits
	Pattern  uint32
	Length   int
}

// SampleUARTMinPeriod16 is the shortest bit the UART receiver takes: one
// bit sample a receiver clock.
const SampleUARTMinPeriod16 = 256

// UARTPeriod16 is the bit time of baud in sixteenths of a nanosecond.
// TRLC-LINKS: REQ-SDS-229
func UARTPeriod16(baud float64) int {
	if !(baud > 0) {
		return 0
	}
	return int(16e9/baud + 0.5)
}

// SampleI2CConfig is the I2C receiver's configuration, as its registers
// hold it.
type SampleI2CConfig struct {
	ClockCh          int   // SCL's channel; SDA is on the other
	Level, Low, High uint8 // SCL's level and re-arm band
	// SDA's level and re-arm band (register 59, register 7's hysteresis).
	SDALevel, SDALow, SDAHigh uint8
	Inverted                  bool
	// Delay is where SDA is read after an SCL crossing, in sixteenths of a
	// nanosecond on SCL's sample grid: the channels' sampling phase (SCL's
	// less SDA's); SCL is read after an SDA crossing at its negation.
	Delay     int
	Address   int // -1: any seven-bit address
	Direction int // 0 write, 1 read, 2 either
	Pattern   uint32
	Length    int
}
