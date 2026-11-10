// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package streams

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/labeth/open-dso-core/decodedlines"
	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// StreamTrigger is what ends a scope-side decoded stream: the core's.
type StreamTrigger = core.StreamTrigger

// The stream triggers.
const (
	StreamManual  = core.StreamManual
	StreamOnError = core.StreamOnError
	StreamOnValue = core.StreamOnValue
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

// ErrLevelsLearning is a stream refused while the ARINC 429 levels are
// learned: the core's.
var ErrLevelsLearning = core.ErrLevelsLearning

// BeginDecodedStream starts a decoded capture for the scope's own display: it
// loads the image whose decoder serves p, builds a native full-depth
// configuration with p's decoder and the chosen trigger, and starts the
// capture. value is the unit StreamOnValue matches.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (d *DecodedStreams) BeginDecodedStream(ctx context.Context, p SerialParams, trig StreamTrigger, value int) (sramcapture.RecordIdentity, error) {
	p.Bytes, p.Addr, p.RW = nil, -1, 2
	if acq.SymbolProtocol(p.Proto) {
		// ADR-STREAM-SOFT-DECODE: the symbol trigger's image has no decoded
		// event transport; these buses are decoded in software.
		return d.beginSoftStream(ctx, p, trig, value)
	}
	// An error trigger; a manual stream is planned as one, then made
	// unmatchable below.
	p.Pattern = []PatternElem{{Kind: core.PatternError}}
	if trig == StreamOnValue {
		p.Pattern = []PatternElem{{Kind: core.PatternData, Value: value & core.UnitMask(p), Mask: core.UnitMask(p)}}
	}
	cfgValue, err := d.decodedCall(ctx, func() (cfg any, err error) {
		// Hold the image until BeginDecodedCapture takes over: between the two
		// owner calls the acquisition loop sees no session and puts the general
		// image back, so a packet stream loads packet, general, packet (three
		// multi-second loads).
		d.decodedStarting = d.h.clock().Now().Add(30 * time.Second) // lapses if the capture never begins
		// The audio stream ends before this image load: left running, the load
		// lost its words, it loaded the stream image back, and the BUS stream
		// fought it for the fabric (or failed).
		d.h.soundYield()
		defer func() {
			if err != nil {
				d.decodedStarting = time.Time{}
			}
		}()
		want := acq.ProtocolImage(p)
		if d.h.hasImages() && d.h.hasImage(want) && want != d.h.protocolImage() {
			if err := d.h.memory().Halt(ctx); err != nil {
				return nil, err
			}
			d.h.loadImage(want)
		}
		if p.Proto == core.SerARINC && d.h.arincAux() == 0 {
			return nil, fmt.Errorf("%w: show the bus decoded a moment, then start again", ErrLevelsLearning)
		}
		return d.decodedStreamConfig(p, trig)
	})
	cfg, err := core.OwnerResult[sramcapture.Config](cfgValue, err)
	if err != nil {
		return sramcapture.RecordIdentity{}, err
	}
	// Install worker transcript mode in the same owner call that starts capture.
	id, err := d.beginDecodedCapture(ctx, cfg, p.Proto, false)
	if err == nil {
		d.mu.Lock()
		d.stats.DecodedStreamMode = "gap-free, the FPGA's decoded events"
		d.mu.Unlock()
	}
	return id, err
}

// decodedStreamConfig is a hardware decoded stream's capture on the loaded
// image: a full-depth record, p's decoder and its pattern (the stop: an error
// token, a value, or for a manual stream an element no unit matches).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (d *DecodedStreams) decodedStreamConfig(p SerialParams, trig StreamTrigger) (sramcapture.Config, error) {
	words := sramcapture.SamplesPerChannel / 2
	c := sramcapture.Config{Source: sramcapture.ADC, PreWords: words / 2, PostWords: words - words/2, Normal: true}
	d.h.planSerial(&c, p, false)
	if acq.HardwareSerialName(&c) == "" {
		return c, errors.New("the loaded image cannot stream this protocol with that trigger")
	}
	if trig == StreamManual {
		if c.Sequence.Length != 1 {
			return c, errors.New("the loaded image has no sequence trigger for a manual stream")
		}
		c.Sequence.Qualify = false
		c.Sequence.Elements[0] = sramcapture.SequenceElement{Kind: sramcapture.SequenceData, Value: streamNever, Mask: streamNever}
	}
	d.h.planHysteresis(&c)
	return c, nil
}

// ReadDecodedLines fetches transcript lines the worker built, from absolute
// line index from; flush completes its line in progress. ok is false when the
// stream runs in event mode (no worker).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (d *DecodedStreams) ReadDecodedLines(ctx context.Context, id sramcapture.RecordIdentity, from int, flush bool) (decodedlines.Batch, bool, error) {
	bp := getLineBuf()
	buf := *bp
	var n int
	var lines bool
	_, err := d.decodedCall(ctx, func() (any, error) {
		if d.softFor(id) {
			return nil, d.softIdentity(id) // event mode
		}
		if err := d.decodedIdentity(id); err != nil {
			return nil, err
		}
		d.decodedContact = time.Now() // a reader follows the session
		if lines = d.decodedLines; !lines {
			return nil, nil
		}
		var rerr error
		n, _, rerr = d.h.memory().ReadDecodedLines(id.Epoch, from, flush, buf)
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
var lineBufs = sync.Pool{New: func() any { return newLineBuf() }}

// TRLC-LINKS: REQ-SDS-018
func newLineBuf() *[]byte {
	b := make([]byte, 64*1024)
	return &b
}

// getLineBuf takes a transcript read buffer from lineBufs.
// TRLC-LINKS: REQ-SDS-018
func getLineBuf() *[]byte {
	if bp, ok := lineBufs.Get().(*[]byte); ok {
		return bp
	}
	return newLineBuf()
}

// FreezeDecodedStream freezes a decoded capture's record - forcing the
// trigger when none has fired - and halts acquisition, leaving the session
// open so the caller can drain the events still queued; a frozen record
// becomes the stopped capture the display recalls once the session ends. It
// returns the record's metadata (its sample span and trigger).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (d *DecodedStreams) FreezeDecodedStream(ctx context.Context, id sramcapture.RecordIdentity) (sramcapture.Metadata, bool, error) {
	var adopted bool
	var meta sramcapture.Metadata
	if d.softFor(id) {
		_, err := d.decodedCall(ctx, func() (any, error) {
			var err error
			meta, adopted, err = d.softFreeze(ctx, id)
			return nil, err
		})
		return meta, adopted, err
	}
	{
		_, err := d.decodedCall(ctx, func() (any, error) {
			if err := d.decodedIdentity(id); err != nil {
				return nil, err
			}
			// A manual stop has no trigger yet: force one so the fabric fills
			// the post-trigger half and freezes the record around this moment.
			m, err := d.h.memory().Status()
			if err != nil {
				return nil, err
			}
			if !m.Frozen || !m.Ready {
				if err := d.h.memory().Force(ctx); err != nil {
					return nil, err
				}
				for deadline := time.Now().Add(500 * time.Millisecond); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
					if m, err = d.h.memory().Status(); err != nil || (m.Frozen && m.Ready) {
						break
					}
				}
				if err != nil {
					return nil, err
				}
			}
			if err := d.h.memory().Halt(ctx); err != nil {
				return nil, err
			}
			d.h.logf("decoded stream end: triggered=%v frozen=%v ready=%v words=%d trigger_index=%d", m.Triggered, m.Frozen, m.Ready, m.Length, m.TriggerIndex)
			meta = m
			if m.Frozen && m.Ready && m.Length > 0 {
				d.mu.Lock()
				d.adopt = &adoptedRecord{m: m, cfg: d.decodedCfg}
				d.mu.Unlock()
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

// HoldDecodedImage keeps the loaded image while a decoded stream ends to be
// started again (another stop, another bus) - at most a few seconds, until
// the next stream's capture begins: between the two the acquisition loop
// would load the trigger's image and the next stream its own again.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
func (d *DecodedStreams) HoldDecodedImage(ctx context.Context) error {
	_, err := d.decodedCall(ctx, func() (any, error) {
		d.decodedStarting = d.h.clock().Now().Add(10 * time.Second)
		return nil, nil
	})
	return err
}

// DiscardDecodedRecord drops a decoded stream's frozen record before the
// stream ends: a stream left for live captures (BUS's Stream view left, or
// started again) needs no recall of it, which reads the full record (about
// 2.5 s) before the next capture or image load.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
func (d *DecodedStreams) DiscardDecodedRecord() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.adopt = nil
}

// takeAdopted hands an adopted record to the acquisition loop once.
// TRLC-LINKS: REQ-SDS-013
func (d *DecodedStreams) takeAdopted() *adoptedRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	a := d.adopt
	d.adopt = nil
	return a
}
