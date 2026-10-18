// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// StreamTiming is what a continuous stream of proto (a Result.Proto name)
// at bit rate baud (the decoder settings' rate: a UART's baud, a clocked
// bus's clock, a pulse code's bit) needs: feature, the shortest stretch of
// line the stream must resolve; longest, the longest frame; quiet, the
// silence that ends a frame (StreamDecoder's Close: no frame goes longer
// without a decoded span, a whole-frame decoder's frame included). ok is
// false for a protocol the stream does not decode.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func StreamTiming(proto string, baud float64) (feature, longest, quiet float64, ok bool) {
	bit := func(def float64) float64 {
		if baud > 0 {
			return 1 / baud
		}
		return 1 / def
	}
	switch proto {
	case "lin": // break, sync, protected ID, up to 8 bytes and checksum; header and response 40 % slack
		b := bit(19200)
		return b, 1.4 * 140 * b, 1.4 * 140 * b, true
	case "dmx512": // break, mark, start code and 512 slots of 11 bits
		b := bit(250000)
		return b, 1e-3 + 513*11*b, 2e-3, true
	case "midi": // a running status byte stream: three bytes
		b := bit(31250)
		return b, 30 * b, 30 * b, true
	case "modbus": // up to 256 characters of 11 bits; 3.5 characters of silence end a frame
		b := bit(19200)
		return b, 256 * 11 * b, 2 * 256 * 11 * b, true
	case "onewire": // a reset (480 us low, presence) and its bytes, slots of at least 60 us
		// The shortest stretch: a 1's low pulse (1 to 15 us, about 6) and
		// the recovery between slots.
		return 5e-6, 20e-3, 20e-3, true
	case "ws281x": // a reset of 50 us or more ends a frame; a bit's short high is a third of it
		b := bit(800000)
		return b / 3, 10e-3, 100e-6, true
	case "dht": // the host's start (1 to 18 ms low), the response and 40 bits (26 us short high)
		return 26e-6, 25e-3, 25e-3, true
	case "cec": // start and up to 16 blocks of 10 bits of 2.4 ms; a 0's high is 0.9 ms
		return 0.6e-3, 0.4, 0.4, true
	case "j1850": // SOF and up to 12 bytes of 64/128 us symbols
		return 64e-6, 15e-3, 15e-3, true
	case "irnec", "irsirc", "irrc5", "irrc6":
		// A frame of at most 70 ms (NEC's 67.5); a modulated line's 38 kHz
		// carrier is the shortest stretch (13 us).
		return 13e-6, 80e-3, 80e-3, true
	case "dali": // 1200 Bd biphase, a backward frame 8 bits, a forward one up to 24
		b := bit(1200)
		return b / 2, 30 * b, 30 * b, true
	case "usbpd": // 300 kBd biphase mark: preamble, SOP, header and up to seven data objects
		b := bit(300000)
		return b / 2, 500 * b, 500 * b, true
	case "spdif": // 32-bit subframes of biphase mark, a half cell the shortest
		b := bit(3072000)
		return b / 2, 64 * b, 64 * b, true
	case "ps2": // 11 clocks a frame, a host request 12 and its acknowledge
		b := bit(12500)
		return b / 2, 14 * b, 14 * b, true
	case "swd": // a request, turnaround, acknowledge, 33 data bits; a line reset 50 clocks
		b := bit(1000000)
		return b / 2, 64 * b, 64 * b, true
	case "mdio": // 32 bits of preamble and a 32-bit frame
		b := bit(2500000)
		return b / 2, 64 * b, 64 * b, true
	case "wiegand": // up to 64 pulses at the bit interval, each a twentieth of it
		b := bit(1000)
		return b / 20, 64 * b, 64 * b, true
	}
	return 0, 0, 0, false
}

// StreamRecordHead is the stretch at a record's start (seconds) where a
// frame may have been cut: a frame starting in it is dropped
// (StreamRecord). SWD has no leader, so a request read from the middle of a
// cut transfer looks like any other: its head is a whole transfer.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func StreamRecordHead(proto string, feature, longest float64) float64 {
	if proto == "swd" {
		return longest
	}
	return 4 * feature
}

// StreamGapFreeMinLog is the finest decimation the gap-free roll stream
// carries without loss: one peak word per 2^9 samples (about 978 k words a
// second); 2^8 outruns the SRAM drum.
const StreamGapFreeMinLog = 9

// StreamBucketLog picks the gap-free stream's decimation for a shortest
// feature of feature seconds: the coarsest bucket of 2^log samples (2 ns
// each; a peak word gives a bucket's two extremes, so two samples) that
// keeps six buckets in the feature, and never finer than the stream
// carries. ok is false when even the finest leaves fewer than three and a
// half buckets in it: the edges then move by a bucket within a feature.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func StreamBucketLog(feature float64) (log int, ok bool) {
	bucket := func(l int) float64 { return float64(uint64(1)<<l) * 2e-9 }
	if feature < 3.5*bucket(StreamGapFreeMinLog) {
		return 0, false
	}
	log = StreamGapFreeMinLog
	for log < 16 && bucket(log+1)*6 <= feature {
		log++
	}
	return log, true
}
