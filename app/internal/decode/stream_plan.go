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
		return 5e-6, 20e-3, 2e-3, true // a pause between bytes stays under 2 ms
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
		// A frame of at most 70 ms (NEC's 67.5). A modulated line's 38 kHz
		// carrier needs no resolving: a bucket holds both its levels, the
		// mark reads as runs the decoders merge, so the shortest stretch is
		// a fraction of a mark (RC-6's 444 us half bit).
		return 50e-6, 80e-3, 80e-3, true
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
	// The rate of the protocols past the thirtieth is their panel rate: an
	// encoder's or a stepper's cycles a second, a PWM's frequency, an RC
	// code's short pulse, a tag's or a line's bit, a reader's field, a
	// clocked bus's clock, Morse units, DCF77's code bits a second.
	case "quad", "stepper": // a step each cycle; two cycles of rest open a motion
		b := bit(1000)
		return b / 4, 64 * b, 3 * b, true
	case "pwm": // each period a frame; its duty read to a sixty-fourth
		b := bit(1000)
		return b / 64, 4 * b, 3 * b, true
	case "rcencode": // 24 pulses of four short ones, then the sync of 32
		b := bit(2857)
		return b, 140 * b, 140 * b, true
	case "ook": // Oregon bursts: preamble, sync and up to 80 nibbles of Manchester (2.1 at half the bit)
		b := bit(1024)
		return b / 4, 400 * b, 400 * b, true
	case "miller": // a modified Miller pause is a quarter bit; an idle of 2.5 bits ends a message
		b := bit(106000)
		return b / 4, 600 * b, 4 * b, true
	case "morse": // a letter's marks come before its span: up to 16 units
		b := bit(17)
		return b, 120 * b, 20 * b, true
	case "dcf77": // a minute's fields come at seconds 28, 35 and 58 of 60; pulses of 100 and 200 ms
		b := bit(1)
		return 0.1 * b, 61 * b, 32 * b, true
	case "em4100": // 64 Manchester bits
		b := bit(1953)
		return b / 2, 66 * b, 66 * b, true
	case "em4305": // gaps of 12 field clocks; a Write of 57 bits of 32 clocks and 300 to end it
		b := bit(125000)
		return 12 * b, 2400 * b, 2400 * b, true
	case "t55xx": // gaps of 16 field clocks; 70 bits of up to 64 clocks
		b := bit(125000)
		return 16 * b, 4800 * b, 4800 * b, true
	case "swim": // a 1's low is two of a bit's 22 SWIM clocks
		b := bit(363636)
		return b / 11, 300 * b, 20 * b, true // a frame's spans come at its end: 13 bits and a gap
	case "sdq": // a 1's low pulse under an eighth of a bit
		b := bit(98425)
		return b / 8, 100 * b, 50 * b, true
	case "pjdl": // bytes of a sync pad and eight data times, frames of up to 30 bytes
		b := bit(22727)
		return b, 400 * b, 40 * b, true
	case "dsi": // a start and eight Manchester bits
		b := bit(600)
		return b / 2, 12 * b, 12 * b, true
	case "qi": // packets of up to 30 bytes of 11 biphase-mark bits
		b := bit(2000)
		return b / 2, 360 * b, 360 * b, true
	case "sdcard": // a token of 48 or 136 clocks
		b := bit(400000)
		return b / 2, 150 * b, 150 * b, true
	case "pdi": // an instruction of up to nine 12-clock characters
		b := bit(1000000)
		return b / 2, 120 * b, 120 * b, true
	case "cjtag": // three TCKC rises a TCK; a scan's bits come at its UPDATE
		b := bit(1000000)
		return b / 2, 512 * b, 512 * b, true
	case "maple": // frames of up to 255 words; bytes come as they end
		b := bit(2000000)
		return b / 2, 8192 * b, 64 * b, true
	case "caliper": // 24 clocks after 10 ms of quiet
		b := bit(12500)
		return b / 2, 30 * b, 30 * b, true
	case "irmp": // as the IR codes; Denon's frame comes twice, 65 ms apart
		return 50e-6, 0.2, 0.2, true
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

// StreamRecordAfterIdle reports a protocol whose frames carry no mark a
// decoder opened in their middle can find, only the idle line between them
// (StreamRecordFromIdle). PDI runs its characters back to back and its
// instructions too: a record opened inside an instruction reads its
// operands as opcodes, and on a periodic bus stays out of step for good.
// TRLC-LINKS: REQ-SDS-260
func StreamRecordAfterIdle(proto string) bool {
	return proto == "pdi"
}

// StreamEdgeFramed reports a protocol whose decoder finds a frame by its
// first edge, with no leader of its own (Miller), or follows a transaction
// whose place a cut loses (SWIM): in a record, a frame cut by the
// record's start reads as frames until a quiet line (StreamRecord's cutN, a
// longest frame).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
func StreamEdgeFramed(proto string) bool {
	switch proto {
	case "miller", "swim":
		return true
	}
	return false
}

// StreamLead is how much line (seconds) a protocol's decoder needs before a
// frame's start to find it: DCF77 knows second 0 by the missing pulse before
// it, so the pulse before that; a JVC repeat comes without a leader and is
// read only after the frame it repeats (IRMP), a longest frame earlier.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
func StreamLead(proto string, baud float64) float64 {
	switch proto {
	case "dcf77":
		if baud <= 0 {
			baud = 1
		}
		return 3 / baud
	case "irmp":
		_, longest, _, _ := StreamTiming(proto, baud)
		return longest
	}
	return 0
}

// StreamGapFreeMinLog is the finest decimation the scope decodes gap-free in
// software, the stream's own finest: one peak word per 2^9 samples (about
// 977 k words a second). On the device's single core a word there costs
// about 0.1 us to fetch from the bus worker, 0.1 us to turn into samples
// and 0.15 to 0.3 us to decode, and the worker's bank transfers about a
// third of the core: a DMX512 bus streamed for minutes without a loss.
const StreamGapFreeMinLog = 9

// streamCheapLog is the decimation a feature that resolves there keeps:
// a finer one (2^10, 2^9) only for the features 2^11 does not resolve, as
// it costs the core two or four times the words.
const streamCheapLog = 11

// StreamMaxLog is the coarsest decimation the stream carries (2^20 samples
// a word, 2.1 ms): a DCF77 minute is then 29 k samples a decode, not 930 k.
const StreamMaxLog = 20

// StreamBucketLog picks the gap-free stream's decimation for a shortest
// feature of feature seconds: the coarsest bucket of 2^log samples (2 ns
// each; a peak word gives a bucket's two extremes, so two samples) that
// keeps six buckets in the feature, and never finer than 2^11; a feature
// 2^11 leaves fewer than three and a half buckets in takes the coarsest
// finer one that leaves it that many (streamCheapLog). ok is false when even
// the finest leaves fewer: the edges then move by a bucket within a feature.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func StreamBucketLog(feature float64) (log int, ok bool) {
	bucket := func(l int) float64 { return float64(uint64(1)<<l) * 2e-9 }
	if feature < 3.5*bucket(streamCheapLog) {
		for log = streamCheapLog - 1; log >= StreamGapFreeMinLog; log-- {
			if feature >= 3.5*bucket(log) {
				return log, true
			}
		}
		return 0, false
	}
	log = streamCheapLog
	for log < StreamMaxLog && bucket(log+1)*6 <= feature {
		log++
	}
	return log, true
}
