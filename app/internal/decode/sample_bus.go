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

// SampleEdges runs the extractor over receiver words, one output a word.
// TRLC-LINKS: REQ-SDS-229
func SampleEdges(wide []uint64, c SampleLineConfig) []SampleLineEdge {
	n := len(wide)
	other := 1 - c.Ch
	levels := make([]bool, 4*n) // the other line's samples at or above its level
	for k, w := range wide {
		for j := 0; j < 4; j++ {
			levels[4*k+j] = sampleOf(w, other, j) >= c.OtherLevel
		}
	}
	out := make([]SampleLineEdge, n)
	armR, armF := false, false
	okR, okF := true, true // the line has passed the opposite band edge since the direction's last crossing
	prev := uint8(0)
	for k, w := range wide {
		o := &out[k]
		pos, rising := -1, false
		for j := 0; j < 4; j++ {
			s := sampleOf(w, c.Ch, j)
			hitR, farR := s >= c.Level, s <= c.Low
			hitF, farF := s < c.Level, s >= c.High
			fireR, fireF := armR && hitR, armF && hitF
			armR = (armR && !hitR) || (farR && okR)
			armF = (armF && !hitF) || (farF && okF)
			okR = (okR && !fireR) || farF
			okF = (okF && !fireF) || farR
			if pos < 0 && ((c.Rise && fireR) || (c.Fall && fireF)) {
				pos, rising = j, fireR
			}
			o.Levels[j] = hitR
		}
		// a line beyond the band for a whole clock re-arms the other way: a
		// runt pulse (past the level, short of the band) must not hold it
		lowAll, highAll := true, true
		for j := 0; j < 4; j++ {
			s := sampleOf(w, c.Ch, j)
			lowAll, highAll = lowAll && s <= c.Low, highAll && s >= c.High
		}
		okR, okF = okR || lowAll, okF || highAll
		if pos >= 0 {
			a := prev
			if pos > 0 {
				a = sampleOf(w, c.Ch, pos-1)
			}
			b := sampleOf(w, c.Ch, pos)
			num, den := int(c.Level)-int(a), int(b)-int(a)
			if !rising {
				num, den = int(a)-int(c.Level), int(a)-int(b)
			}
			q := 32 * num / den // 0..32 sixteenths of a nanosecond past sample pos-1
			t := 32*(pos-1) + q
			m := (t + c.Delay + 16) >> 5 // the other line's sample nearest t+Delay, from this clock's first
			d := false                   // before the first word: the receiver's zeros
			if i := 4*k + m; i >= 0 && i < len(levels) {
				d = levels[i]
			}
			o.Found, o.Time, o.Rising, o.Other = true, t, rising, d
		}
		prev = sampleOf(w, c.Ch, 3)
	}
	return out
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

// TRLC-LINKS: REQ-SDS-229
func (c SampleUARTConfig) valid() bool {
	return c.Period16 >= SampleUARTMinPeriod16 && c.Period16 < 1<<24 && c.Bits >= 5 && c.Bits <= 9 &&
		c.Parity >= 0 && c.Parity <= 2 && c.Length >= 0 && c.Length <= 4
}

// SampleUART runs the UART receiver on the samples over receiver words. A
// start is a falling crossing of the idle line; bit k of the frame (0 the
// start bit) is the line's sample nearest the start's crossing plus
// (k+1/2) bit times, and with Resync every crossing inside the frame moves
// the next bit to half a bit time after it, so a baud error accumulates only
// over a run of equal bits. A frame whose stop bit reads high and whose
// parity holds publishes its data; otherwise an error event.
// TRLC-LINKS: REQ-SDS-229
func SampleUART(wide []uint64, c SampleUARTConfig) []SampleBusEvent {
	if !c.valid() {
		return nil
	}
	edges := SampleEdges(wide, SampleLineConfig{Ch: c.Ch, Rise: true, Fall: true, Level: c.Level, Low: c.Low, High: c.High})
	half := c.Period16 >> 1
	last := c.Bits + 1 // the stop bit's index
	if c.Parity != 0 {
		last++
	}
	var events []SampleBusEvent
	busy := false
	next := 0 // the pending bit's time from the clock's first sample
	index := 0
	value, ones := 0, 0
	history, count := uint32(0), 0
	for t := 0; t < len(wide); t++ {
		k := t - SampleUARTLatency
		if k < 0 {
			continue
		}
		e := edges[k]
		falling := e.Found && e.Rising == c.Inverted
		level := func(at int) bool { return e.Levels[(at+16)>>5] != c.Inverted }
		var ev *SampleBusEvent
		edge := func() {
			if !busy {
				if falling {
					busy, next, index, value, ones = true, e.Time+half, 0, 0, 0
				}
			} else if c.Resync {
				next = e.Time + half
			}
		}
		sample := func() {
			bit := level(next)
			b := 0
			if bit {
				b = 1
			}
			switch {
			case index == 0:
				if bit { // no start bit there
					busy = false
				}
			case index <= c.Bits:
				value |= b << (index - 1)
				ones += b
			case index < last: // parity
				ones += b
			default: // the stop bit
				busy = false
				good := bit && (c.Parity == 0 || ones&1 == c.Parity-1) // even: an even count of ones with the parity bit
				if good {
					v := uint32(value & 0xff)
					nh := history<<8 | v
					hit := c.Length == 0 ||
						(c.Length == 1 && nh&0xff == c.Pattern&0xff) ||
						(c.Length == 2 && count >= 1 && nh&0xffff == c.Pattern&0xffff) ||
						(c.Length == 3 && count >= 2 && nh&0xffffff == c.Pattern&0xffffff) ||
						(c.Length == 4 && count >= 3 && nh == c.Pattern)
					ev = &SampleBusEvent{Clock: t, Kind: 2, Data: uint16(value), Match: hit}
					history = nh
					count = min(count+1, 4)
				} else {
					ev = &SampleBusEvent{Clock: t, Kind: 4}
					history, count = 0, 0
				}
			}
			index++
			next += c.Period16
		}
		pending := busy && next < 112
		if e.Found && (!pending || e.Time <= next) {
			edge()
			if busy && next < 112 {
				sample()
			}
		} else {
			if pending {
				sample()
			}
			if e.Found {
				edge()
			}
		}
		if busy {
			next -= 128
		}
		if ev != nil {
			events = append(events, *ev)
		}
	}
	return events
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

// SampleI2C runs the I2C receiver on the samples over receiver words: an
// SCL rising crossing reads SDA at its time (the bit); an SDA crossing reads
// SCL at its own time and, SCL high, is a START (falling) or STOP (rising).
// Crossings of the two lines in one receiver clock are taken in the order
// of their interpolated times, the later on the next clock (one is held;
// a third in that clock is dropped; the ordering takes a clock of its own).
// Events, metadata and the match are i2c_trigger.v's; a match (the address byte's or the pattern's eighth bit)
// has no event of its own and is returned as its clock.
// TRLC-LINKS: REQ-SDS-229
func SampleI2C(wide []uint64, c SampleI2CConfig) (events []SampleBusEvent, matches []int) {
	if c.Length < 0 || c.Length > 4 || c.Direction < 0 || c.Direction > 2 {
		return nil, nil
	}
	scl := SampleEdges(wide, SampleLineConfig{Ch: c.ClockCh, Rise: !c.Inverted, Fall: c.Inverted, Level: c.Level, Low: c.Low, High: c.High, OtherLevel: c.SDALevel, Delay: c.Delay})
	sda := SampleEdges(wide, SampleLineConfig{Ch: 1 - c.ClockCh, Rise: true, Fall: true, Level: c.SDALevel, Low: c.SDALow, High: c.SDAHigh, OtherLevel: c.Level, Delay: wrap8(-c.Delay)})
	type crossing struct {
		valid, isSDA, rising, other bool
	}
	var held crossing
	active, addressByte, selected, receivedAddress := false, true, false, false
	bitIndex, count := 0, 0
	shift := uint8(0)
	history := uint32(0)
	for t := 0; t < len(wide); t++ {
		k := t - SampleI2CLatency
		if k < 0 {
			continue
		}
		a, b := scl[k], sda[k]
		ca := crossing{valid: a.Found, other: a.Other != c.Inverted}
		cb := crossing{valid: b.Found, isSDA: true, rising: b.Rising != c.Inverted, other: b.Other != c.Inverted}
		// SCL's crossing on SDA's grid: its time plus the delay.
		first, second := ca, cb
		if a.Found && b.Found && b.Time < a.Time+c.Delay {
			first, second = cb, ca
		} else if !a.Found {
			first, second = cb, crossing{}
		}
		var now crossing
		if held.valid {
			now, held = held, first
		} else {
			now, held = first, second
		}
		var ev *SampleBusEvent
		match := false
		switch {
		case !now.valid:
		case now.isSDA && now.other && !now.rising: // START, or repeated START
			ev = &SampleBusEvent{Clock: t, Kind: 1}
			active, selected, count, history, bitIndex, addressByte = true, false, 0, 0, 0, true
		case now.isSDA && now.other: // STOP
			if active {
				ev = &SampleBusEvent{Clock: t, Kind: 3}
			}
			active, selected, count, history = false, false, 0, 0
		case !now.isSDA && active:
			bit := uint8(0)
			if now.other {
				bit = 1
			}
			received := shift<<1 | bit
			if bitIndex == 8 {
				meta := uint8(0)
				if receivedAddress {
					meta = 1
				}
				ev = &SampleBusEvent{Clock: t, Kind: 2, Data: uint16(shift), Meta: meta | bit<<1}
				bitIndex = 0
			} else {
				shift = received
				if bitIndex == 7 {
					receivedAddress = addressByte
					if addressByte {
						addressMatch := (c.Address < 0 || int(received>>1) == c.Address&127) &&
							(c.Direction == 2 || (c.Direction == 0 && received&1 == 0) || (c.Direction == 1 && received&1 != 0))
						addressByte, selected = false, addressMatch
						match = addressMatch && c.Length == 0
					} else if selected {
						nh := history<<8 | uint32(received)
						match = (c.Length == 1 && nh&0xff == c.Pattern&0xff) ||
							(c.Length == 2 && count >= 1 && nh&0xffff == c.Pattern&0xffff) ||
							(c.Length == 3 && count >= 2 && nh&0xffffff == c.Pattern&0xffffff) ||
							(c.Length == 4 && count >= 3 && nh == c.Pattern)
						history = nh
						count = min(count+1, 4)
					}
				}
				bitIndex++
			}
		}
		if match {
			matches = append(matches, t)
		}
		if ev != nil {
			events = append(events, *ev)
		}
	}
	return events, matches
}

// wrap8 is v as the 8-bit signed register holds it.
// TRLC-LINKS: REQ-SDS-229
func wrap8(v int) int { return int(int8(uint8(v))) }
