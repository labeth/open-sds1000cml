// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"open-sds/app/internal/bus"
	"open-sds/app/internal/decode"
	"open-sds/app/internal/decodedlines"
	"open-sds/app/internal/sramcapture"
)

// The decoded stream of the protocols on the qualify image's symbol trigger
// (ADR-STREAM-SOFT-DECODE): that image has no decoded event transport, so
// the stream is decoded in software from samples, in one of two modes.
//
//   - Gap-free: the stream image's continuous peak-detect stream (every word
//     reaches the host, ADR-STREAM-IMAGE) is decoded as it arrives
//     (decode.StreamDecoder), where its finest lossless bucket (2^9 samples,
//     1.024 us) keeps three and a half buckets in the protocol's shortest
//     feature. The stream image has no symbol trigger, so a stop on a value
//     ends the transcript at the frame software read it in, and the record
//     shown is the FPGA's: the qualify image's symbol trigger armed on the
//     value, the next time it comes.
//   - Captures: the faster buses are decoded from back-to-back full-rate
//     (or lightly decimated) records on the qualify image, each armed with
//     the symbol trigger - the frame start, or the stop value - and forced
//     when it does not fire; what lies between records is not seen, and the
//     stream says what share of the signal time it read. A stop on a value is
//     a record the symbol trigger fired on.
//
// A stop on a decode error is refused: the symbol trigger matches no error
// token, and the FPGA decides every trigger.

// softEpochBase marks the record identities of software-decoded streams.
const softEpochBase = 0xC0000000

// softPhase is where a software-decoded stream stands.
type softPhase int

const (
	softStreaming  softPhase = iota // decoding the stream or the records
	softRecord                      // transcript ended on the value: arming the record
	softRecordWait                  // waiting for the symbol trigger's record
	softDone                        // ended: the record is ready, or err says why not
)

// softRecordWaitMax bounds the wait for the value to come again for the
// record after a gap-free stream read it.
const softRecordWaitMax = 10 * time.Second

// softCaptureWords is the words of a record in the captures mode: a shorter
// record reads sooner but the share of signal time read is the readout's
// (about the same whatever the length).
const softCaptureWords = 16384

// softStream is a software-decoded stream's state, on the acquisition owner.
type softStream struct {
	id      sramcapture.RecordIdentity
	p       SerialParams
	proto   string
	decoder SerialDecoder
	trig    StreamTrigger
	value   int
	mask    int
	gapFree bool
	quiet   float64
	phase   softPhase
	err     error
	start   time.Time

	dec     *decode.StreamDecoder
	sampleS float64
	// ordinal is the 2 ns sample ordinal of the stream's sample i (gap-free)
	// or of a record's sample i (captures).
	ordBase, ordStep float64

	events []sramcapture.DecodedEvent
	seq    uint32

	// Line B's slicing level, learned from the stream when the request has
	// none (a window of idle line B has no level of its own).
	bLo, bHi uint8
	bSeen    bool

	// Gap-free stream session.
	log       uint8
	streaming bool
	isolated  bool
	deadline  *bus.DecodedDeadline
	expected  uint64
	buf       [65536]byte
	raw       []byte
	pendA     []uint8
	pendB     []uint8
	pushN     int
	last      [2]uint16
	haveLast  bool
	losses    int

	// Captures mode.
	capCfg    sramcapture.Config
	armed     bool
	armAt     time.Time
	forced    bool
	symCount  int
	capWait   time.Duration
	read      time.Duration // signal time read
	nextOrd   uint64
	stopUnit  int    // the stop value's unit in its frame
	noStop    bool   // emit: the value read does not stop (the FPGA did not fire)
	valueRead bool   // emit: the value was read
	missed    int    // records in a row whose decode read the value the FPGA did not fire on
	stopFirst uint64 // the stop frame's first and last sample ordinals
	stopLast  uint64

	// The record shown at the stop.
	recCfg  sramcapture.Config
	recMeta sramcapture.Metadata
	recOK   bool
	recDue  time.Time
}

// softValueMisses is how many records in a row may hold the stop value
// the symbol trigger did not fire on before a captures stream gives up.
const softValueMisses = 8

// softProtoNames are the decode package's names of the symbol protocols.
var softProtoNames = map[int]string{serLIN: "lin", serDMX: "dmx512", serModbus: "modbus", serOneWire: "onewire",
	serWS281x: "ws281x", serDHT: "dht", serCEC: "cec", serJ1850: "j1850", serIRNEC: "irnec", serIRSIRC: "irsirc",
	serIRRC5: "irrc5", serIRRC6: "irrc6", serDALI: "dali", serUSBPD: "usbpd", serSPDIF: "spdif", serPS2: "ps2",
	serSWD: "swd", serMDIO: "mdio", serWiegand: "wiegand", serQuad: "quad", serStepper: "stepper", serPWM: "pwm",
	serRCEncode: "rcencode", serOOK: "ook", serMiller: "miller", serMorse: "morse", serDCF77: "dcf77", serEM4100: "em4100",
	serEM4305: "em4305", serT55xx: "t55xx", serSWIM: "swim", serSDQ: "sdq", serPJDL: "pjdl", serDSI: "dsi", serQi: "qi",
	serSDCard: "sdcard", serPDI: "pdi", serCJTAG: "cjtag", serMaple: "maple", serCaliper: "caliper", serIRMP: "irmp"}

// SoftStreamMode says how the scope streams proto at bit rate baud (the
// decoder settings'): "gap-free" from the continuous stream, "captures" of
// back-to-back records, "" when it streams through the FPGA's decoded events.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func SoftStreamMode(proto, baud int, overdrive bool) string {
	name, ok := softProtoNames[proto]
	if !ok {
		return ""
	}
	if _, ok := softGapFreeLog(name, baud, overdrive); ok {
		return "gap-free"
	}
	return "captures"
}

// softGapFreeLog is the gap-free stream's decimation for a protocol, ok
// false when it needs the captures.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func softGapFreeLog(name string, baud int, overdrive bool) (int, bool) {
	feature, _, _, ok := decode.StreamTiming(name, float64(baud))
	if !ok {
		return 0, false
	}
	if name == "onewire" && overdrive {
		feature /= 10 // overdrive's slots are a tenth as long
	}
	return decode.StreamBucketLog(feature)
}

// softCaptureLog is the captures mode's record decimation: the coarsest
// that keeps eight samples in the protocol's shortest feature (0: full rate).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func softCaptureLog(feature float64) uint8 {
	log := uint8(0)
	for l := uint8(4); l <= 12; l++ {
		if float64(uint64(1)<<l)*2e-9*8 > feature {
			break
		}
		log = l
	}
	return log
}

// beginSoftStream starts a software-decoded stream of a symbol protocol.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) beginSoftStream(ctx context.Context, p SerialParams, trig StreamTrigger, value int) (sramcapture.RecordIdentity, error) {
	name := softProtoNames[p.Proto]
	d, ok := serialDecoder(p.Proto)
	if name == "" || !ok {
		return sramcapture.RecordIdentity{}, fmt.Errorf("no software stream for protocol %d", p.Proto)
	}
	if trig == StreamOnError {
		return sramcapture.RecordIdentity{}, fmt.Errorf("%s streams stop on a value or by hand: the FPGA's symbol trigger matches no decode error", d.Name)
	}
	if !p.HaveThr {
		return sramcapture.RecordIdentity{}, fmt.Errorf("%s streams need the decode's threshold", d.Name)
	}
	feature, longest, quiet, _ := decode.StreamTiming(name, float64(p.Baud))
	if name == "onewire" && p.Overdrive {
		feature, longest, quiet = feature/10, longest/10, quiet/10
	}
	log, gapFree := softGapFreeLog(name, p.Baud, p.Overdrive)
	if !gapFree && trig == StreamOnValue {
		// Captures stop on a record the symbol trigger fired on; the value
		// must be one it matches wherever it comes.
		q := p
		q.Bytes, q.Pattern = nil, []PatternElem{{Kind: patternData, Value: value & unitMask(p), Mask: unitMask(p)}}
		if !planHardwareSymbol(q).Enabled {
			return sramcapture.RecordIdentity{}, fmt.Errorf("%s streams as captures, and the FPGA's symbol trigger matches %X only at a fixed place in a frame: stop it by hand", d.Name, value&unitMask(p))
		}
	}
	s := &softStream{p: p, proto: name, decoder: d, trig: trig, value: value & unitMask(p), mask: unitMask(p),
		gapFree: gapFree, quiet: quiet, log: uint8(log)}
	v, err := e.decodedCall(ctx, func() (any, error) {
		e.decodedStarting = time.Time{}
		if e.decodedSession != nil || e.soft != nil {
			return nil, fmt.Errorf("a decoded stream is already running")
		}
		want := imageQualify
		if gapFree {
			want = imageStream
		}
		if e.images == nil || !e.hasImage(want) {
			return nil, fmt.Errorf("this build has no %s FPGA image to stream %s", want, d.Name)
		}
		if err := e.sram.Halt(ctx); err != nil {
			return nil, err
		}
		if e.protocolImage != want {
			e.loadImage(want)
			if e.protocolImage != want {
				return nil, fmt.Errorf("the %s FPGA image did not load", want)
			}
		}
		e.SetRunning(false) // streaming is not RUN
		e.softEpoch++
		s.id = sramcapture.RecordIdentity{Epoch: softEpochBase | e.softEpoch&0x3fffffff, Record: 1}
		s.start = e.clk.Now()
		if gapFree {
			bucket := float64(uint64(1)<<log) * 2e-9
			s.sampleS = bucket / 2
			s.ordStep = float64(uint64(1) << (log - 1))
			// Decode a fifth of a second at a time, or twice Close (each decode
			// reads the open window again, so short pushes cost it over and
			// over), but at least once a second: a DCF77 minute's Close is 32 s.
			s.pushN = int(math.Max(0.2, math.Min(1, 2*quiet)) / s.sampleS)
			s.dec = decode.NewStreamDecoder(s.decodeFunc(), int(quiet/s.sampleS), int((longest+quiet)/s.sampleS))
			s.dec.SetLead(int(decode.StreamLead(name, float64(p.Baud)) / s.sampleS))
			// The stream starts on the loop's next step (softDrain), after
			// the loop has ended a roll stream that may hold the banks.
		} else {
			s.capWait = 20 * time.Millisecond
		}
		e.soft = s
		mode := s.modeText(0)
		e.mu.Lock()
		e.stats.DecodedStreamMode = mode
		e.mu.Unlock()
		e.logf("engine: %s stream: %s", d.Name, mode)
		return s.id, nil
	})
	if err != nil {
		return sramcapture.RecordIdentity{}, err
	}
	return v.(sramcapture.RecordIdentity), nil
}

// decodeFunc is the registry decoder at the stream's sample time and the
// request's settings, with line B's learned level.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (s *softStream) decodeFunc() func(a, b []uint8) decode.Result {
	return func(a, b []uint8) decode.Result {
		p := s.p
		if !p.HaveThrB && s.bSeen && int(s.bHi)-int(s.bLo) >= 24 {
			p.ThresholdB, p.HaveThrB = (float64(s.bLo)+float64(s.bHi))/2, true
		}
		return s.decoder.Decode(a, b, s.sampleS, p)
	}
}

// modeText is the stream's mode for the displays.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (s *softStream) modeText(elapsed time.Duration) string {
	if s.gapFree {
		return fmt.Sprintf("gap-free, software decode of the %.3g us stream", s.sampleS*2e6)
	}
	if elapsed <= 0 {
		return "captures, not gap-free"
	}
	return fmt.Sprintf("captures, not gap-free: %.2g %% of the signal time read", 100*s.read.Seconds()/elapsed.Seconds())
}

// softStreamStart starts the stream image's continuous peak stream.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240, REQ-SDS-035
func (e *Engine) softStreamStart(s *softStream) error {
	ch := uint8(s.p.ChA & 1)
	cfg := sramcapture.Config{Source: sramcapture.ADC, DecimationLog2: s.log, PreWords: sramcapture.Words - 17, PostWords: 17,
		TriggerChannel: ch, TriggerLevel: uint8(math.Round(s.p.Threshold))}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := e.sram.StartStream(ctx, cfg, true, true); err != nil {
		return err
	}
	s.streaming, s.expected, s.haveLast = true, 0, false
	if p, ok := e.b.(streamPumper); ok {
		owned, err := p.SetIsolatedStreamPump(true)
		if err != nil {
			e.softStreamStop(s)
			return err
		}
		s.isolated = owned
	}
	if dev, ok := e.b.(*bus.Dev); ok && !s.isolated && s.deadline == nil {
		if d, err := dev.StartDecodedDeadline(); err == nil {
			s.deadline = d
		}
	}
	return nil
}

// softStreamStop ends the stream session, if one runs.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240, REQ-SDS-035
func (e *Engine) softStreamStop(s *softStream) {
	if !s.streaming {
		return
	}
	if s.isolated {
		if _, err := e.b.(streamPumper).SetIsolatedStreamPump(false); err != nil {
			e.busErr(err)
		}
		s.isolated = false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	if err := e.sram.StopStream(ctx); err != nil {
		e.busErr(err)
	}
	cancel()
	if s.deadline != nil {
		_ = s.deadline.Close()
		s.deadline = nil
	}
	s.streaming = false
}

// softStep runs the stream one step on the acquisition owner.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) softStep() {
	s := e.soft
	switch s.phase {
	case softStreaming:
		if s.gapFree {
			e.softDrain(s)
		} else {
			e.softCapture(s)
		}
		e.mu.Lock()
		e.stats.DecodedStreamMode = s.modeText(e.clk.Now().Sub(s.start))
		e.mu.Unlock()
	case softRecord:
		e.softArmRecord(s)
	case softRecordWait:
		e.softWaitRecord(s)
	default:
		if s.deadline != nil {
			_ = s.deadline.Pause()
		} else {
			e.clk.Sleep(20 * time.Millisecond)
		}
	}
}

// softDrain moves the stream's words into the decoder for one step.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240, REQ-SDS-035
func (e *Engine) softDrain(s *softStream) {
	if !s.streaming {
		if err := e.softStreamStart(s); err != nil {
			e.busErr(err)
			e.sleepBeating(100 * time.Millisecond)
			return
		}
	}
	raw := s.raw[:0]
	w := byteSink{dst: &raw}
	for end := e.clk.Now().Add(100 * time.Millisecond); e.clk.Now().Before(end) && !e.stopReq.Load(); {
		if s.isolated {
			first, n, _, err := e.b.(streamPumper).ReadIsolatedStream(s.buf[:])
			if err == nil && n > 0 && first != s.expected {
				err = fmt.Errorf("stream ordinal %d, want %d", first, s.expected)
			}
			if err != nil {
				e.softLoss(s, err)
				break
			}
			if n == 0 {
				break
			}
			raw = append(raw, s.buf[:n]...)
			s.expected += uint64(n / 4)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		block, err := e.sram.DrainStream(ctx, s.expected, &w)
		cancel()
		if err == sramcapture.ErrNoStreamBlock {
			break
		}
		if err != nil {
			e.softLoss(s, err)
			break
		}
		s.expected = block.FirstWord + uint64(block.Words)
	}
	if len(raw) > 0 {
		s.feedPeak(raw, e.interleaveCalibration)
	}
	s.raw = raw[:0]
	if len(s.pendA) >= s.pushN {
		e.softPush(s)
	} else if len(raw) == 0 {
		if s.deadline != nil {
			_ = s.deadline.Pause()
		} else {
			e.clk.Sleep(5 * time.Millisecond)
		}
	}
}

// byteSink appends what DrainStream writes.
type byteSink struct{ dst *[]byte }

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (w byteSink) Write(b []byte) (int, error) {
	*w.dst = append(*w.dst, b...)
	return len(b), nil
}

// softLoss records words the stream lost: the transcript says so, the
// decoder starts afresh after them, and a new stream starts on the next step.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) softLoss(s *softStream, err error) {
	s.losses++
	e.logf("engine: software stream lost words after %d: %v", s.expected, err)
	e.softPush(s) // what arrived before the loss
	s.dec.Skip(0)
	s.haveLast = false
	s.addEvent(sramcapture.DecodedEvent{Kind: sramcapture.EventLoss, Sample: s.ord(s.dec.Next()), Count: 1})
	e.softStreamStop(s)
}

// ord is stream sample i's 2 ns ordinal.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (s *softStream) ord(i uint64) uint64 { return uint64(s.ordBase + float64(i)*s.ordStep) }

// feedPeak turns peak words ({max2,max1,min2,min1}) into two samples per
// bucket per line - the converter offsets removed as for the envelope, each
// pair ordered to continue from the sample before it (orderEnvelope) - and
// queues them for the decoder.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240, REQ-SDS-010
func (s *softStream) feedPeak(raw []byte, cal *InterleaveCalibration) {
	n := len(raw) / 4
	lines := 1
	if clockedTwoLine(s.p.Proto) {
		lines = 2
	}
	// One pass a line, appending to the queued samples: at 2^9 the stream
	// brings about a million words a second to a single core.
	for k := 0; k < lines; k++ {
		ch := s.p.ChA & 1
		dst := &s.pendA
		if k == 1 {
			ch, dst = 1-ch, &s.pendB
		}
		var off [2]int
		if cal != nil {
			off = cal.envelopeOffsets(ch)
		}
		// Each extreme is a byte: its debiased Q8.8 value and that value
		// rounded come from tables, not from per-word arithmetic.
		var q [2][256]int32
		var r [2][256]uint8
		for k := range 2 {
			for b := range 256 {
				x := b << 8
				if cal != nil {
					x = debiasQ(x, off[k])
				}
				q[k][b], r[k][b] = int32(x), roundQ8(uint16(x))
			}
		}
		at := len(*dst)
		out := slices.Grow(*dst, 2*n)[:at+2*n]
		prev := int32(s.last[ch])
		if !s.haveLast && n > 0 {
			prev = (q[0][raw[ch]] + q[1][raw[2+ch]]) / 2
		}
		o := out[at:]
		for i := 0; i < n; i++ {
			bl, bh := raw[4*i+ch], raw[4*i+2+ch]
			lo, hi := q[0][bl], q[1][bh]
			dl, dh := lo-prev, hi-prev
			if dl < 0 {
				dl = -dl
			}
			if dh < 0 {
				dh = -dh
			}
			if dh < dl { // the pair continues from the sample before it
				o[2*i], o[2*i+1] = r[1][bh], r[0][bl]
				prev = lo
			} else {
				o[2*i], o[2*i+1] = r[0][bl], r[1][bh]
				prev = hi
			}
		}
		s.last[ch] = uint16(prev)
		*dst = out
		if k == 1 {
			s.learnB(out[at:])
		}
	}
	s.haveLast = s.haveLast || n > 0
}

// learnB follows line B's extremes for its slicing level.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (s *softStream) learnB(b []uint8) {
	for _, v := range b {
		if !s.bSeen {
			s.bLo, s.bHi, s.bSeen = v, v, true
		}
		s.bLo, s.bHi = min(s.bLo, v), max(s.bHi, v)
	}
}

// softPush decodes the queued samples and queues the frames they complete;
// on the stop value the transcript ends there and the record is armed.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) softPush(s *softStream) {
	if len(s.pendA) == 0 {
		return
	}
	var b []uint8
	if len(s.pendB) == len(s.pendA) {
		b = s.pendB
	}
	frames := s.dec.Push(s.pendA, b)
	s.pendA, s.pendB = s.pendA[:0], s.pendB[:0]
	if s.emit(frames, s.ord) {
		e.softStreamStop(s)
		s.phase = softRecord
	}
}

// emit queues frames as decoded events (ord maps their sample indices) up
// to the frame holding the stop value; it reports that frame.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (s *softStream) emit(frames []decode.StreamFrame, ord func(uint64) uint64) bool {
	for _, f := range frames {
		s.addEvent(sramcapture.DecodedEvent{Kind: sramcapture.EventStart, Sample: ord(f.I0), Value: decodedlines.StartNoteCode(frameStart(f))})
		hit, unit := false, 0
		for _, sp := range f.Spans {
			switch sp.Kind {
			case "data":
				v := sp.Val & s.mask
				s.addEvent(sramcapture.DecodedEvent{Kind: sramcapture.EventData, Sample: ord(sp.I0), Value: uint32(v), Valid: true})
				if !hit && s.trig == StreamOnValue && v == s.value {
					s.valueRead = true
					if !s.noStop {
						hit, s.stopUnit = true, unit
					}
				}
				unit++
			case "frame-error", "parity-error", "gap":
				s.addEvent(sramcapture.DecodedEvent{Kind: sramcapture.EventError, Sample: ord(sp.I0)})
			}
		}
		s.addEvent(sramcapture.DecodedEvent{Kind: sramcapture.EventEnd, Sample: ord(f.I1)})
		if hit {
			// The frame's line starts at its named start or, unnamed (PS/2,
			// Wiegand), at its first unit: the trigger mark goes on that line.
			s.stopFirst, s.stopLast = ord(f.I0), ord(f.I1)
			if decodedlines.StartNoteCode(frameStart(f)) == 0 {
				for _, sp := range f.Spans {
					if sp.Kind == "data" || sp.Kind == "frame-error" || sp.Kind == "parity-error" || sp.Kind == "gap" {
						s.stopFirst = ord(sp.I0)
						break
					}
				}
			}
			s.addEvent(sramcapture.DecodedEvent{Kind: sramcapture.EventTrigger, Sample: ord(f.I0)})
			return true
		}
	}
	return false
}

// frameStart is a frame's start-of-frame text ("" none).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func frameStart(f decode.StreamFrame) string {
	if len(f.Spans) > 0 && f.Spans[0].Kind == "sof" {
		return f.Spans[0].Text
	}
	return ""
}

// softEventsMax bounds the queued events: a reader that stops reading ends
// the transcript rather than the memory.
const softEventsMax = 1 << 20

// addEvent queues one event.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (s *softStream) addEvent(ev sramcapture.DecodedEvent) {
	if len(s.events) >= softEventsMax {
		if s.err == nil {
			s.err = fmt.Errorf("decoded host buffer overflow; transcript incomplete")
		}
		return
	}
	ev.Epoch, ev.Sequence = s.id.Epoch, s.seq
	s.seq++
	s.events = append(s.events, ev)
}

// valuePattern is the stop value as the symbol trigger matches it: at any
// unit (at < 0), or as unit at of a frame - the buses whose units differ in
// width or meaning (an IR code's address and command, Wiegand's facility
// and card) match a unit only at its place after the frame's start.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (s *softStream) valuePattern(at int) []PatternElem {
	if at < 0 {
		return []PatternElem{{Kind: patternData, Value: s.value, Mask: s.mask}}
	}
	pat := []PatternElem{{Kind: patternStart}}
	for i := 0; i < at; i++ {
		pat = append(pat, PatternElem{Kind: patternData})
	}
	pat = append(pat, PatternElem{Kind: patternData, Value: s.value, Mask: s.mask})
	// The unit at that place may be narrower than the transcript's (an IR
	// address of 5 bits): the mask its width takes, the widest that plans.
	for w := bitsLen(s.mask); w > 0 && s.value>>w == 0; w-- {
		pat[len(pat)-1].Mask = 1<<w - 1
		if s.symbolPlans(pat) {
			break
		}
	}
	return pat
}

// stopPattern is the symbol trigger's request for the value a gap-free
// stream stopped on: the value at any unit, else where the transcript read
// it - and always there on a bus whose request reads its units at their
// places in one frame (an IR code's address and command), where a value
// alone asks the first unit. Not a Morse letter's place, which a run of
// letters of other lengths moves.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240, REQ-SDS-260
func (s *softStream) stopPattern() []PatternElem {
	pat := s.valuePattern(-1)
	spec, ok := symbolSpec(s.p)
	if (ok && spec.oneFrame || !s.symbolPlans(pat)) && s.proto != "morse" {
		pat = s.valuePattern(s.stopUnit)
	}
	return pat
}

// bitsLen is the bits of a mask of low ones.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func bitsLen(mask int) int {
	n := 0
	for ; mask > 0; mask >>= 1 {
		n++
	}
	return n
}

// symbolPlans reports a pattern the symbol trigger takes for the stream's
// bus.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (s *softStream) symbolPlans(pat []PatternElem) bool {
	p := s.p
	p.Bytes, p.Pattern = nil, pat
	return planHardwareSymbol(p).Enabled
}

// softRecordConfig is a record of words at decimation log on the symbol
// trigger, armed on pattern (nil: a frame start), as a hardware stream's
// record is.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) softRecordConfig(s *softStream, words uint32, log uint8, pattern []PatternElem) sramcapture.Config {
	c := sramcapture.Config{Source: sramcapture.ADC, DecimationLog2: log, PreWords: words / 2, PostWords: words - words/2, Normal: true}
	p := s.p
	p.Bytes, p.Pattern = nil, pattern
	e.planSerial(&c, p, false)
	e.planHysteresis(&c, log != 0)
	return c
}

// softArmRecord loads the qualify image and arms the symbol trigger on the
// stop value for the record shown.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) softArmRecord(s *softStream) {
	s.phase = softDone
	if !e.hasImage(imageQualify) {
		s.err = fmt.Errorf("no qualify FPGA image for the record")
		return
	}
	if e.protocolImage != imageQualify {
		if e.loadImage(imageQualify); e.protocolImage != imageQualify {
			s.err = fmt.Errorf("the qualify FPGA image did not load for the record")
			return
		}
	}
	words := uint32(sramcapture.SamplesPerChannel / 2)
	c := e.softRecordConfig(s, words, 0, s.stopPattern())
	if !c.Symbol.Enabled {
		s.err = fmt.Errorf("the FPGA's symbol trigger cannot match %X where it came (unit %d of its frame, past its 64-bit window or among units of other lengths): no record", s.value, s.stopUnit+1)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	err := e.sram.Arm(ctx, c)
	cancel()
	if err != nil {
		s.err = err
		return
	}
	s.recCfg, s.recDue, s.phase = c, e.clk.Now().Add(softRecordWaitMax), softRecordWait
}

// softWaitRecord waits for the symbol trigger's record.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) softWaitRecord(s *softStream) {
	m, err := e.sram.Status()
	switch {
	case err != nil:
		s.err, s.phase = err, softDone
	case m.Frozen && m.Ready:
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err = e.sram.Halt(ctx)
		cancel()
		s.recMeta, s.recOK, s.err, s.phase = m, err == nil, err, softDone
	case e.clk.Now().After(s.recDue):
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = e.sram.Halt(ctx)
		cancel()
		s.err = fmt.Errorf("%X did not come again within %v for the FPGA's record", s.value, softRecordWaitMax)
		s.phase = softDone
	default:
		e.clk.Sleep(2 * time.Millisecond)
	}
}

// softCapture runs the captures mode one step: arm a record on the symbol
// trigger, force it when the trigger does not fire, read and decode it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) softCapture(s *softStream) {
	feature, longest, _, _ := decode.StreamTiming(s.proto, float64(s.p.Baud))
	if s.proto == "onewire" && s.p.Overdrive {
		feature /= 10
	}
	if !s.armed {
		log := softCaptureLog(feature)
		words := uint32(softCaptureWords)
		if log == 0 {
			words *= 4 // full rate: a record holds a few of the fast frames
		}
		// A frame is handed out once Close follows it, and the first only after
		// a quiet Close: a record holds eight, so most of its frames come out.
		wordS := float64(uint64(1)<<log) * 2e-9
		if log == 0 {
			wordS = 4e-9
		}
		need := 8 * s.quiet
		if decode.StreamEdgeFramed(s.proto) {
			need = max(need, 2*longest) // its first longest frame is dropped unless after a quiet line
		}
		words = uint32(min(float64(sramcapture.Words), max(float64(words), math.Ceil(need/wordS))))
		var pat []PatternElem
		if s.trig == StreamOnValue {
			pat = s.valuePattern(-1)
		}
		c := e.softRecordConfig(s, words, log, pat)
		s.symCount = e.symbolCount(c)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := e.sram.Arm(ctx, c)
		cancel()
		if err != nil {
			e.busErr(err)
			e.sleepBeating(100 * time.Millisecond)
			return
		}
		s.capCfg, s.armed, s.forced, s.armAt = c, true, false, e.clk.Now()
		return
	}
	m, err := e.sram.Status()
	if err != nil || m.DataFault {
		s.armed = false
		if err != nil {
			e.busErr(err)
		}
		return
	}
	if !(m.Frozen && m.Ready) {
		// Fill the record's history first, then give the trigger a moment.
		fill := time.Duration(float64(s.capCfg.PreWords) * float64(uint64(1)<<s.capCfg.DecimationLog2) * 4e-9 * float64(time.Second))
		if s.capCfg.DecimationLog2 != 0 {
			fill /= 2 // a decimated word is one sample
		}
		if !s.forced && e.clk.Now().Sub(s.armAt) >= fill+s.capWait {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			err = e.sram.Force(ctx)
			cancel()
			s.forced = true
			if err != nil {
				e.busErr(err)
				s.armed = false
			}
			return
		}
		e.clk.Sleep(time.Millisecond)
		return
	}
	end := e.clk.Now()
	s.armed = false
	hit := s.trig == StreamOnValue && s.symCount >= 0 && e.symbolCount(s.capCfg) != s.symCount
	f := &Frame{}
	n := int(m.Length) * int(m.SamplesPerWord)
	if n == 0 || !(m.SampleRateHz > 0) {
		return
	}
	f.C1, f.C2, f.Valid = make([]uint8, n), make([]uint8, n), n
	if m.FractionBits == 8 {
		f.Q1, f.Q2 = make([]uint16, n), make([]uint16, n)
	}
	writer := sramFrameWriter{f: f, m: m}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = e.sram.RecallReadsOf(ctx, m, sramcapture.RecordRead{Offset: 0, Count: m.Length, Dst: &writer})
	cancel()
	if err != nil || writer.n != n {
		if err != nil {
			e.busErr(err)
		}
		return
	}
	span := float64(n) / m.SampleRateHz
	s.read += time.Duration(span * float64(time.Second))
	a, b := f.C1, f.C2
	if s.p.ChA&1 == 1 {
		a, b = f.C2, f.C1
	}
	if !clockedTwoLine(s.p.Proto) {
		b = nil
	} else {
		s.learnB(b)
	}
	// The record ended about when it was seen frozen: place it there on the
	// stream's clock, after the last one.
	first := uint64(math.Max(0, end.Sub(s.start).Seconds()-span) / 2e-9)
	first = max(first, s.nextOrd)
	s.sampleS = 1 / m.SampleRateHz
	s.ordBase, s.ordStep = float64(first), s.sampleS/2e-9
	s.nextOrd = s.ord(uint64(n)) + 1
	cutN := 0
	if decode.StreamEdgeFramed(s.proto) {
		cutN = int(longest / s.sampleS)
	}
	record := decode.StreamRecord
	if decode.StreamRecordAfterIdle(s.proto) {
		record = decode.StreamRecordFromIdle
	}
	frames := record(a, b, s.decodeFunc(), int(s.quiet/s.sampleS), int(decode.StreamRecordHead(s.proto, feature, longest)/s.sampleS), cutN, s.proto == "spdif")
	s.noStop, s.valueRead = !hit, false
	stop := s.emit(frames, s.ord)
	s.noStop = false
	if s.valueRead && !hit {
		// The decode read the value in a record the symbol trigger did not
		// fire on: before it was armed, now and then; in every record, a
		// value the trigger cannot read where it comes (the last byte of
		// a Miller frame).
		if s.missed++; s.missed >= softValueMisses {
			s.err = fmt.Errorf("the FPGA's symbol trigger did not fire on %X, which the decode read in %d records in a row: no record", s.value, s.missed)
			s.phase = softDone
			return
		}
	} else if hit {
		s.missed = 0
	}
	if hit {
		if !stop { // the FPGA matched where the decode did not read the value
			at := s.ord(uint64(m.TriggerIndex) * uint64(m.SamplesPerWord))
			s.stopFirst, s.stopLast = at, at
			s.addEvent(sramcapture.DecodedEvent{Kind: sramcapture.EventTrigger, Sample: at})
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = e.sram.Halt(ctx)
		cancel()
		s.recCfg, s.recMeta, s.recOK, s.phase = s.capCfg, m, true, softDone
	}
}

// softFor reports whether id names the running software stream.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) softFor(id sramcapture.RecordIdentity) bool {
	return id.Epoch&softEpochBase == softEpochBase
}

// softIdentity checks id against the running software stream.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) softIdentity(id sramcapture.RecordIdentity) error {
	if e.soft == nil || e.soft.id != id {
		return fmt.Errorf("decoded capture identity is no longer active")
	}
	return nil
}

// softRead copies queued events into dst.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) softRead(id sramcapture.RecordIdentity, dst []sramcapture.DecodedEvent) (int, error) {
	if err := e.softIdentity(id); err != nil {
		return 0, err
	}
	s := e.soft
	n := copy(dst, s.events)
	s.events = s.events[:copy(s.events, s.events[n:])]
	if n == 0 && s.err != nil && s.phase != softDone {
		return 0, s.err
	}
	return n, nil
}

// softStatus is the stream's status for the stream view: triggered once
// the record is ready (its span the stop frame's), the reason when none
// will be.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) softStatus(id sramcapture.RecordIdentity) (sramcapture.Metadata, error) {
	if err := e.softIdentity(id); err != nil {
		return sramcapture.Metadata{}, err
	}
	s := e.soft
	if s.phase != softDone {
		return sramcapture.Metadata{}, nil
	}
	if !s.recOK {
		if s.err == nil {
			s.err = fmt.Errorf("no record")
		}
		return sramcapture.Metadata{}, s.err
	}
	return sramcapture.Metadata{Triggered: true, Frozen: true, Ready: true, HasSampleTimeline: true,
		SampleFirst: s.stopFirst, SampleLast: s.stopLast}, nil
}

// softFreeze ends the stream's acquisition; a record the symbol trigger
// took goes to the display.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) softFreeze(ctx context.Context, id sramcapture.RecordIdentity) (sramcapture.Metadata, bool, error) {
	if err := e.softIdentity(id); err != nil {
		return sramcapture.Metadata{}, false, err
	}
	s := e.soft
	if s.gapFree && s.phase == softStreaming {
		e.softPush(s)
	}
	e.softStreamStop(s)
	if s.phase != softDone || !s.recOK {
		if err := e.sram.Halt(ctx); err != nil {
			return sramcapture.Metadata{}, false, err
		}
		if s.phase != softDone {
			s.phase = softDone
		}
		return sramcapture.Metadata{}, false, nil
	}
	e.logf("software stream end: record words=%d trigger_index=%d", s.recMeta.Length, s.recMeta.TriggerIndex)
	e.mu.Lock()
	e.adopt = &adoptedRecord{m: s.recMeta, cfg: s.recCfg}
	e.mu.Unlock()
	return sramcapture.Metadata{Triggered: true, Frozen: true, Ready: true, HasSampleTimeline: true,
		SampleFirst: s.stopFirst, SampleLast: s.stopLast}, true, nil
}

// softEnd tears the stream down; acquisition stays stopped until RUN.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) softEnd(ctx context.Context, id sramcapture.RecordIdentity) error {
	if err := e.softIdentity(id); err != nil {
		return err
	}
	s := e.soft
	e.softStreamStop(s)
	err := e.sram.Halt(ctx)
	e.soft = nil
	e.logf("engine: software stream ended: %s, %d losses", s.modeText(e.clk.Now().Sub(s.start)), s.losses)
	e.SetRunning(false)
	return err
}

// DecodedStreamMode says how the decoded stream runs, for the displays:
// gap-free from the FPGA's decoded events or software decode of the
// continuous stream, or captures with the share of signal time read.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) DecodedStreamMode() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stats.DecodedStreamMode
}
