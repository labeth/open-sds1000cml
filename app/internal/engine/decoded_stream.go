// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"open-sds/app/internal/decodedlines"
	"open-sds/app/internal/sramcapture"
)

// StreamTrigger is what ends a scope-side decoded stream (ADR-PANEL-DECODED-STREAM).
type StreamTrigger int

const (
	// StreamManual streams until the operator stops it.
	StreamManual StreamTrigger = iota
	// StreamOnError triggers on any decode error the hardware decoder flags.
	StreamOnError
	// StreamOnValue triggers on one decoded unit equal to the chosen value.
	StreamOnValue
)

// streamNever is a data bit no decoder emits (units are at most 29 bits):
// a manual stream's single sequence element requires it, so it never fires.
const streamNever = 1 << 31

// adoptedRecord is a decoded capture's retained record, handed to the
// stopped SRAM loop for display.
type adoptedRecord struct {
	m   sramcapture.Metadata
	cfg sramcapture.Config
}

// ErrLevelsLearning is a stream refused while the ARINC 429 slicer levels
// are still being learned from a sampled record (a moment after the decoder
// settings name ARINC 429).
var ErrLevelsLearning = errors.New("the ARINC 429 levels are not learned yet")

// BeginDecodedStream starts a decoded capture for the scope's own display: it
// loads the image whose decoder serves p, builds a native full-depth
// configuration with p's decoder and the chosen trigger, and starts the
// capture. value is the unit StreamOnValue matches.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (e *Engine) BeginDecodedStream(ctx context.Context, p SerialParams, trig StreamTrigger, value int) (sramcapture.RecordIdentity, error) {
	p.Bytes, p.Addr, p.RW = nil, -1, 2
	if symbolProtocol(p.Proto) {
		// ADR-STREAM-SOFT-DECODE: the symbol trigger's image has no decoded
		// event transport; these buses are decoded in software.
		return e.beginSoftStream(ctx, p, trig, value)
	}
	switch trig {
	case StreamOnError:
		p.Pattern = []PatternElem{{Kind: patternError}}
	case StreamOnValue:
		p.Pattern = []PatternElem{{Kind: patternData, Value: value & unitMask(p), Mask: unitMask(p)}}
	default: // planned as an error trigger, then made unmatchable below
		p.Pattern = []PatternElem{{Kind: patternError}}
	}
	cfgValue, err := e.decodedCall(ctx, func() (cfg any, err error) {
		// Hold the image until BeginDecodedCapture takes over: between the two
		// owner calls the acquisition loop sees no session and puts the general
		// image back, so a packet stream loads packet, general, packet (three
		// multi-second loads).
		e.decodedStarting = e.clk.Now().Add(30 * time.Second) // lapses if the capture never begins
		// The audio stream ends before this image load: left running, the load
		// lost its words, it loaded the stream image back, and the BUS stream
		// fought it for the fabric (or failed).
		e.soundYield()
		defer func() {
			if err != nil {
				e.decodedStarting = time.Time{}
			}
		}()
		want := protocolImage(p)
		if e.images != nil && e.hasImage(want) && want != e.protocolImage {
			if err := e.sram.Halt(ctx); err != nil {
				return nil, err
			}
			e.loadImage(want)
		}
		if p.Proto == serARINC && e.arincAux.Load() == 0 {
			return nil, fmt.Errorf("%w: show the bus decoded a moment, then start again", ErrLevelsLearning)
		}
		return e.decodedStreamConfig(p, trig)
	})
	if err != nil {
		return sramcapture.RecordIdentity{}, err
	}
	// Install worker transcript mode in the same owner call that starts capture.
	id, err := e.beginDecodedCapture(ctx, cfgValue.(sramcapture.Config), p.Proto, false)
	if err == nil {
		e.mu.Lock()
		e.stats.DecodedStreamMode = "gap-free, the FPGA's decoded events"
		e.mu.Unlock()
	}
	return id, err
}

// decodedStreamConfig is a hardware decoded stream's capture on the loaded
// image: a full-depth record, p's decoder and its pattern (the stop: an error
// token, a value, or for a manual stream an element no unit matches).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (e *Engine) decodedStreamConfig(p SerialParams, trig StreamTrigger) (sramcapture.Config, error) {
	words := uint32(sramcapture.SamplesPerChannel / 2)
	c := sramcapture.Config{Source: sramcapture.ADC, PreWords: words / 2, PostWords: words - words/2, Normal: true}
	e.planSerial(&c, p, false)
	if hardwareSerialName(c) == "" {
		return c, fmt.Errorf("the loaded image cannot stream this protocol with that trigger")
	}
	if trig == StreamManual {
		if c.Sequence.Length != 1 {
			return c, fmt.Errorf("the loaded image has no sequence trigger for a manual stream")
		}
		c.Sequence.Qualify = false
		c.Sequence.Elements[0] = sramcapture.SequenceElement{Kind: sramcapture.SequenceData, Value: streamNever, Mask: streamNever}
	}
	e.planHysteresis(&c, false)
	return c, nil
}

// ReadDecodedLines fetches transcript lines the worker built, from absolute
// line index from; flush completes its line in progress. ok is false when the
// stream runs in event mode (no worker).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (e *Engine) ReadDecodedLines(ctx context.Context, id sramcapture.RecordIdentity, from int, flush bool) (decodedlines.Batch, bool, error) {
	bp := lineBufs.Get().(*[]byte)
	buf := *bp
	var n int
	var lines bool
	_, err := e.decodedCall(ctx, func() (any, error) {
		if e.softFor(id) {
			return nil, e.softIdentity(id) // event mode
		}
		if err := e.decodedIdentity(id); err != nil {
			return nil, err
		}
		e.decodedContact = time.Now() // a reader follows the session
		if lines = e.decodedLines; !lines {
			return nil, nil
		}
		var rerr error
		n, _, rerr = e.sram.ReadDecodedLines(id.Epoch, from, flush, buf)
		return nil, rerr
	})
	if err != nil && ctx.Err() != nil {
		// gave up waiting: the job may still write into buf, which stays its own
		return decodedlines.Batch{}, lines, err
	}
	defer lineBufs.Put(bp) // DecodeLines copies what it keeps
	if err != nil || !lines {
		return decodedlines.Batch{}, lines, err
	}
	b, err := decodedlines.DecodeLines(buf[:n])
	return b, true, err
}

// lineBufs hold the transcript reads' 64 KB buffers for reuse: a stream's
// list polls several times a second, a cleared allocation each.
var lineBufs = sync.Pool{New: func() any { b := make([]byte, 64*1024); return &b }}

// FreezeDecodedStream freezes a decoded capture's record - forcing the
// trigger when none has fired - and halts acquisition, leaving the session
// open so the caller can drain the events still queued; a frozen record
// becomes the stopped capture the display recalls once the session ends. It
// returns the record's metadata (its sample span and trigger).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (e *Engine) FreezeDecodedStream(ctx context.Context, id sramcapture.RecordIdentity) (sramcapture.Metadata, bool, error) {
	var adopted bool
	var meta sramcapture.Metadata
	if e.softFor(id) {
		_, err := e.decodedCall(ctx, func() (any, error) {
			var err error
			meta, adopted, err = e.softFreeze(ctx, id)
			return nil, err
		})
		return meta, adopted, err
	}
	{
		_, err := e.decodedCall(ctx, func() (any, error) {
			if err := e.decodedIdentity(id); err != nil {
				return nil, err
			}
			// A manual stop has no trigger yet: force one so the fabric fills
			// the post-trigger half and freezes the record around this moment.
			m, err := e.sram.Status()
			if err != nil {
				return nil, err
			}
			if !(m.Frozen && m.Ready) {
				if err := e.sram.Force(ctx); err != nil {
					return nil, err
				}
				for deadline := time.Now().Add(500 * time.Millisecond); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
					if m, err = e.sram.Status(); err != nil || (m.Frozen && m.Ready) {
						break
					}
				}
				if err != nil {
					return nil, err
				}
			}
			if err := e.sram.Halt(ctx); err != nil {
				return nil, err
			}
			e.logf("decoded stream end: triggered=%v frozen=%v ready=%v words=%d trigger_index=%d", m.Triggered, m.Frozen, m.Ready, m.Length, m.TriggerIndex)
			meta = m
			if m.Frozen && m.Ready && m.Length > 0 {
				e.mu.Lock()
				e.adopt = &adoptedRecord{m: m, cfg: e.decodedCfg}
				e.mu.Unlock()
				adopted = true
			}
			return nil, nil
		})
		if err != nil {
			return meta, false, err
		}
	}
	return meta, adopted, nil
}

// EndDecodedStream freezes the record (when adopt) and ends the capture.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (e *Engine) EndDecodedStream(ctx context.Context, id sramcapture.RecordIdentity, adopt bool) (sramcapture.Metadata, bool, error) {
	var meta sramcapture.Metadata
	var adopted bool
	if adopt {
		var err error
		if meta, adopted, err = e.FreezeDecodedStream(ctx, id); err != nil {
			return meta, false, err
		}
	}
	return meta, adopted, e.EndDecodedCapture(ctx, id)
}

// HoldDecodedImage keeps the loaded image while a decoded stream ends to be
// started again (another stop, another bus) - at most a few seconds, until
// the next stream's capture begins: between the two the acquisition loop
// would load the trigger's image and the next stream its own again.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
func (e *Engine) HoldDecodedImage(ctx context.Context) error {
	_, err := e.decodedCall(ctx, func() (any, error) {
		e.decodedStarting = e.clk.Now().Add(10 * time.Second)
		return nil, nil
	})
	return err
}

// DiscardDecodedRecord drops a decoded stream's frozen record before the
// stream ends: a stream left for live captures (BUS's Stream view left, or
// started again) needs no recall of it, which reads the full record (about
// 2.5 s) before the next capture or image load.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
func (e *Engine) DiscardDecodedRecord() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.adopt = nil
}

// takeAdopted hands an adopted record to the acquisition loop once.
// TRLC-LINKS: REQ-SDS-013
func (e *Engine) takeAdopted() *adoptedRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	a := e.adopt
	e.adopt = nil
	return a
}
