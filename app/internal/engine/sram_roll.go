// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"open-sds/app/internal/bus"
	"open-sds/app/internal/sramcapture"
)

// Roll mode (ADR-SRAM-ROLL): at slow timebases a triggered frame must wait out
// its whole screen, so AUTO instead scrolls a continuous view. The shipping
// fabric reads SRAM only from a frozen record, so the view is stitched from
// short back-to-back decimated captures placed on the host clock; the few
// milliseconds between them (re-arm and readout) hold the last value.

// rollMinTdiv is the slowest timebase that still runs triggered frames: from
// here a screen spans 100 ms or more, too long for 10 frames a second.
const rollMinTdiv = 10e-3

// rollScreenSamples bounds the roll view; a screen is decimated to fit.
const rollScreenSamples = 4096

// rollChunkS is the length of one capture: about 20 view updates a second.
const rollChunkS = 0.05

// planRollPeak plans a peak-detect stream view: one word per bucket of 2^log
// samples, shown as its minimum and maximum, so two view samples per bucket.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-035
func planRollPeak(tdiv float64) rollPlan {
	span := 10 * tdiv
	p := rollPlan{log: 8}
	for p.log < 20 && span*500e6/float64(uint64(1)<<p.log) > rollScreenSamples/2 {
		p.log++
	}
	bucketS := float64(uint64(1)<<p.log) / 500e6
	p.sampleS = bucketS / 2
	p.screen = int(math.Max(2, 2*math.Round(span/bucketS)))
	p.chunk = p.screen
	return p
}

// rollPlan is the decimation and geometry of a roll view.
type rollPlan struct {
	log     uint8
	sampleS float64
	screen  int // samples in the view
	chunk   int // words per capture
}

// planRoll picks the finest decimation whose screen fits rollScreenSamples.
// TRLC-LINKS: REQ-SDS-010
func planRoll(tdiv float64) rollPlan {
	span := 10 * tdiv
	p := rollPlan{log: 4, sampleS: 32e-9}
	for p.log < 20 && span/p.sampleS > rollScreenSamples {
		p.log++
		p.sampleS *= 2
	}
	p.screen = int(math.Max(2, math.Round(span/p.sampleS)))
	p.chunk = max(8, min(int(math.Round(rollChunkS/p.sampleS)), p.screen))
	return p
}

// rollActive reports whether the live view rolls: running AUTO at a slow
// timebase on a plain edge trigger, with nothing that needs triggered frames.
// TRLC-LINKS: REQ-SDS-010
func (e *Engine) rollActive() bool {
	if !e.running.Load() || e.singleArmed.Load() || e.rawOnly {
		return false
	}
	e.mu.Lock()
	tdiv := e.band.TdivS
	if e.pendSet {
		tdiv = e.pendBand.TdivS
	}
	norm, tp := e.norm, e.tp
	e.mu.Unlock()
	switch {
	case norm || tdiv < rollMinTdiv*0.999 || tp.typ != TrigEdge:
		return false
	case e.acqMode.Load() == AcqAverage, e.serialMode.Load() == SerialTrigger,
		e.zoneMode.Load() == ZoneTrigger, e.maskMode.Load() != MaskOff, e.bodeMode.Load() == BodeOn:
		return false
	case e.leased(&e.decodeLease):
		return false // a raw consumer wants triggered full-rate records
	}
	return true
}

// SetRollStream chooses how roll runs. Off (the default), it stitches short
// captures on whatever image is loaded, so a timebase step into or out of roll
// is instant. On, roll loads the stream image and is gap-free, at the cost of
// an image reload (seconds) whenever roll starts or ends.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-035
func (e *Engine) SetRollStream(on bool) { e.rollStream.Store(on) }

// RollStream reports SetRollStream.
// TRLC-LINKS: REQ-SDS-010
func (e *Engine) RollStream() bool { return e.rollStream.Load() }

// rollView is the scrolling ring: the newest sample last.
type rollView struct {
	plan     rollPlan
	tdiv     float64
	c1, c2   []uint8
	q1, q2   []uint16
	filled   int       // samples received since the view started
	lastEnd  time.Time // host time of the newest sample
	scale    [2]chScale
	covered  float64 // captured share of the recent timeline, 0..1
	captured float64
	elapsed  float64
	// Stream session (rollStreamStep).
	streaming bool
	first     bool // the next data starts a new stream: place it by the host clock
	expected  uint64
	restartAt time.Time
	lastPub   time.Time
	losses    int
	lastBlock time.Time
	buf       [32768]byte
	isolated  bool                 // the bus worker drains the banks into its ring
	deadline  *bus.DecodedDeadline // prompt service while streaming (bus.StartDecodedDeadline)
}

// TRLC-LINKS: REQ-SDS-010
func (r *rollView) reset(p rollPlan, tdiv float64, scale [2]chScale) {
	r.plan, r.tdiv, r.scale = p, tdiv, scale
	r.c1, r.c2 = make([]uint8, p.screen), make([]uint8, p.screen)
	r.q1, r.q2 = make([]uint16, p.screen), make([]uint16, p.screen)
	r.filled, r.lastEnd, r.first = 0, time.Time{}, true
	r.captured, r.elapsed, r.covered = 0, 0, 0
}

// push appends gap held samples and then the chunk (Q8.8 pairs per word).
// TRLC-LINKS: REQ-SDS-010
func (r *rollView) push(gap int, q1, q2 []uint16) {
	n := len(r.c1)
	add := min(gap+len(q1), n)
	copy(r.c1, r.c1[add:])
	copy(r.c2, r.c2[add:])
	copy(r.q1, r.q1[add:])
	copy(r.q2, r.q2[add:])
	at := n - add
	hold := min(gap, add)
	if hold > 0 {
		h1, h2 := r.q1[max(at-1, 0)], r.q2[max(at-1, 0)]
		if r.filled == 0 && len(q1) > 0 {
			h1, h2 = q1[0], q2[0]
		}
		for i := 0; i < hold; i++ {
			r.q1[at+i], r.q2[at+i] = h1, h2
		}
	}
	src := len(q1) - (add - hold)
	copy(r.q1[at+hold:], q1[src:])
	copy(r.q2[at+hold:], q2[src:])
	for i := at; i < n; i++ {
		r.c1[i], r.c2[i] = roundQ8(r.q1[i]), roundQ8(r.q2[i])
	}
	if r.filled == 0 { // the first chunk also fills the view's past
		for i := 0; i < at; i++ {
			r.q1[i], r.q2[i] = r.q1[at], r.q2[at]
			r.c1[i], r.c2[i] = r.c1[at], r.c2[at]
		}
	}
	r.filled += gap + len(q1)
}

// rollWriter collects a chunk's words: as Q8.8 pairs (chunked captures) and
// as raw bytes (peak-detect stream words).
type rollWriter struct {
	q1, q2 []uint16
	raw    []byte
}

// TRLC-LINKS: REQ-SDS-010
func (w *rollWriter) Write(b []byte) (int, error) {
	if len(b)%4 != 0 {
		return 0, fmt.Errorf("unaligned roll payload")
	}
	w.raw = append(w.raw, b...)
	for i := 0; i < len(b); i += 4 {
		w.q1 = append(w.q1, binary.LittleEndian.Uint16(b[i:]))
		w.q2 = append(w.q2, binary.LittleEndian.Uint16(b[i+2:]))
	}
	return len(b), nil
}

// rollStep captures one chunk, places it on the view's timeline and publishes
// the view. It returns early (nothing published) when roll ends or a setting
// changes mid-capture.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-009
func (e *Engine) rollStep(r *rollView) {
	e.mu.Lock()
	if e.pendSet {
		e.band = e.pendBand
		e.pendSet = false
	}
	tdiv := e.band.TdivS
	e.stats.TdivS = tdiv
	e.stats.BandKind = "sram"
	e.stats.HaltMode = "roll"
	e.mu.Unlock()
	p := planRoll(tdiv)
	scale := e.liveScale()
	if r.plan != p || r.tdiv != tdiv || r.c1 == nil {
		r.reset(p, tdiv, scale)
	} else if scale != r.scale {
		// A V/div or offset change: re-express the history at the new scale.
		for ch := range scale {
			c, q := r.c1, r.q1
			if ch == 1 {
				c, q = r.c2, r.q2
			}
			rescaleCodes(c, q, r.scale[ch], scale[ch])
		}
		r.scale = scale
	}
	cfg := sramcapture.Config{Source: sramcapture.ADC, DecimationLog2: p.log, PreWords: uint32(p.chunk - 1), PostWords: 1,
		TriggerChannel: uint8(e.trigSrc.Load()), TriggerLevel: uint8(e.trigLevelWord())}
	armAt := e.clk.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	err := e.sram.Arm(ctx, cfg)
	cancel()
	if err != nil {
		e.busErr(err)
		e.sleepBeating(100 * time.Millisecond)
		return
	}
	var m sramcapture.Metadata
	aborted := false
	for !e.stopReq.Load() {
		e.serviceCommands()
		e.beatN.Add(1)
		if !e.rollActive() || e.decodedSession != nil || len(e.sramJobs) != 0 {
			aborted = true
			break
		}
		e.mu.Lock()
		changed := e.pendSet
		e.mu.Unlock()
		if changed {
			aborted = true
			break
		}
		m, err = e.sram.Status()
		if err != nil || m.DataFault || (m.Frozen && m.Ready) {
			break
		}
		e.clk.Sleep(time.Millisecond)
	}
	end := e.clk.Now()
	if aborted || e.stopReq.Load() || err != nil || m.DataFault {
		ctx, cancel = context.WithTimeout(context.Background(), time.Second)
		if herr := e.sram.Halt(ctx); herr != nil {
			e.busErr(herr)
		}
		cancel()
		if err != nil {
			e.busErr(err)
		}
		if aborted && e.running.Load() && !e.rollActive() {
			r.c1 = nil // leaving roll: start a fresh view next time
		}
		return
	}
	w := rollWriter{q1: make([]uint16, 0, m.Length), q2: make([]uint16, 0, m.Length)}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	recall := e.sram.Recall
	if m.Revision == 10 {
		recall = e.sram.RecallForward
	}
	_, err = recall(ctx, 0, m.Length, &w)
	cancel()
	if err != nil || len(w.q1) == 0 {
		if err == nil {
			err = fmt.Errorf("empty roll chunk")
		}
		e.busErr(err)
		return
	}
	// Place the chunk: its last sample is at the freeze, seen within a poll.
	n := len(w.q1)
	gap := 0
	if !r.lastEnd.IsZero() {
		missing := end.Sub(r.lastEnd).Seconds() - float64(n)*p.sampleS
		gap = max(0, int(math.Round(missing/p.sampleS)))
		r.elapsed = r.elapsed*0.9 + end.Sub(r.lastEnd).Seconds()
		r.captured = r.captured*0.9 + float64(n)*p.sampleS
	}
	r.lastEnd = end
	if r.elapsed > 0 {
		r.covered = math.Min(1, r.captured/r.elapsed)
	}
	r.push(gap, w.q1, w.q2)

	e.rollPublish(r, m.Decimation, "roll view: back-to-back decimated captures; gaps between them hold the last value",
		fmt.Sprintf("roll (%d samples, %.0f%% captured)", p.screen, 100*r.covered),
		fmt.Sprintf("roll chunk %d words %.0f ms, gap %d samples, cycle %.0f ms",
			n, float64(n)*p.sampleS*1e3, gap, float64(e.clk.Now().Sub(armAt))/float64(time.Millisecond)))
}

// rollPublish publishes the view as an untriggered frame.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-009
func (e *Engine) rollPublish(r *rollView, decimation uint32, filter, readout, stages string) {
	p, tdiv := r.plan, r.tdiv
	f := e.arena.Write()
	c1, c2, q1, q2 := f.C1, f.C2, f.Q1, f.Q2
	*f = Frame{C1: c1, C2: c2, Valid: p.screen, WinCols: p.screen, SampleS: p.sampleS, CaptureSampleS: p.sampleS,
		TdivS: tdiv, DisplayedS: tdiv, EdgeX: -1, TrigPos: -1, Coherent: true, HaltOK: true, Interp: false,
		Decimation: decimation, TriggerKind: "roll", Roll: true, CaptureDepth: p.screen, Filter: filter}
	copy(f.C1, r.c1)
	copy(f.C2, r.c2)
	f.Q1 = append(q1[:0], r.q1...)
	f.Q2 = append(q2[:0], r.q2...)
	e.noteChannelMeans(f)
	e.seq++
	f.Seq = e.seq
	e.arena.Publish()
	now := e.clk.Now()
	e.mu.Lock()
	e.pubIdent = frameIdent{f.TdivS, f.SampleS, f.PeakDetect}
	e.stats.Published++
	e.stats.Coherent++
	e.lastPublish = now
	e.stats.Seq = e.seq
	e.lastPubAt = now
	e.pubTimes = append(e.pubTimes, now)
	if len(e.pubTimes) > 64 {
		e.pubTimes = e.pubTimes[len(e.pubTimes)-64:]
	}
	e.stats.ValidDepth, e.stats.MemDepth, e.stats.WinCols = p.screen, p.screen, p.screen
	e.stats.RecordS, e.stats.DisplayedS = float64(p.screen)*p.sampleS, tdiv
	e.stats.LiveReadout, e.stats.FrameStages = readout, stages
	e.mu.Unlock()
}

// rollPublishEvery paces a streamed view: banks can publish faster than the
// displays draw.
const rollPublishEvery = 40 * time.Millisecond

// rollStreamStep runs roll on the stream image: one continuous decimated
// capture whose every word reaches the host in order through the stream
// banks, so the view has no gaps (ADR-STREAM-IMAGE). It drains for one view
// update and publishes. An overrun ends the session; the next starts a new
// stream and places its data by the host clock, as the chunked view does.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-035
func (e *Engine) rollStreamStep(r *rollView) {
	e.mu.Lock()
	if e.pendSet {
		e.band = e.pendBand
		e.pendSet = false
	}
	tdiv := e.band.TdivS
	e.stats.TdivS = tdiv
	e.stats.BandKind = "sram"
	e.stats.HaltMode = "roll-stream"
	e.mu.Unlock()
	p := planRollPeak(tdiv)
	scale := e.liveScale()
	if r.plan != p || r.tdiv != tdiv || r.c1 == nil {
		e.rollStreamStop(r)
		r.reset(p, tdiv, scale)
	} else if scale != r.scale {
		for ch := range scale {
			c, q := r.c1, r.q1
			if ch == 1 {
				c, q = r.c2, r.q2
			}
			rescaleCodes(c, q, r.scale[ch], scale[ch])
		}
		r.scale = scale
	}
	if !r.streaming {
		cfg := sramcapture.Config{Source: sramcapture.ADC, DecimationLog2: p.log, PreWords: sramcapture.Words - 17, PostWords: 17,
			TriggerChannel: uint8(e.trigSrc.Load()), TriggerLevel: uint8(e.trigLevelWord())}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := e.sram.StartStream(ctx, cfg, true, true)
		cancel()
		if err != nil {
			e.busErr(err)
			e.sleepBeating(100 * time.Millisecond)
			return
		}
		r.streaming, r.expected, r.restartAt, r.lastBlock = true, 0, e.clk.Now(), e.clk.Now()
		// With a bus worker (the instrument), the worker drains the banks at its
		// FIFO service into a ring, so the app's scheduling cannot lose words
		// (bench 2026-10-10: in-app drains stalled 16 to 21 ms behind the LCD).
		if p, ok := e.b.(streamPumper); ok {
			owned, err := p.SetIsolatedStreamPump(true)
			if err != nil {
				e.busErr(err)
				e.rollStreamStop(r)
				return
			}
			r.isolated = owned
		}
		// Two banks give the drain only a seal period or two of slack: on the
		// single core the LCD render alone held the engine off for 23 to 50 ms
		// (bench 2026-10-10, every loss with both banks unreleased). Run the
		// session with the decoded stream's FIFO service, as that stream does.
		if dev, ok := e.b.(*bus.Dev); ok && r.deadline == nil && !r.isolated {
			if d, err := dev.StartDecodedDeadline(); err != nil {
				e.logf("roll: stream scheduling: %v", err)
			} else {
				r.deadline = d
			}
		}
	}
	start := e.clk.Now()
	var w rollWriter
	for !e.stopReq.Load() {
		e.serviceCommands()
		e.beatN.Add(1)
		e.mu.Lock()
		changed := e.pendSet
		e.mu.Unlock()
		if changed || !e.rollActive() || len(e.sramJobs) != 0 {
			return // the loop stops the stream or starts the new view
		}
		if r.isolated {
			p := e.b.(streamPumper)
			first, n, _, err := p.ReadIsolatedStream(r.buf[:])
			if err == nil && n > 0 && first != r.expected {
				err = fmt.Errorf("stream ordinal %d, want %d", first, r.expected)
			}
			if err != nil {
				r.losses++
				e.logf("roll: stream ended after %d words: %v", r.expected, err)
				e.rollStreamStop(r)
				break
			}
			if n > 0 {
				w.Write(r.buf[:n])
				r.expected += uint64(n / 4)
				r.lastBlock = e.clk.Now()
				continue
			}
			if len(w.q1) > 0 && e.clk.Now().Sub(r.lastPub) >= rollPublishEvery {
				break
			}
			e.clk.Sleep(5 * time.Millisecond)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		block, err := e.sram.DrainStream(ctx, r.expected, &w)
		cancel()
		if err == sramcapture.ErrNoStreamBlock {
			if len(w.q1) > 0 && e.clk.Now().Sub(r.lastPub) >= rollPublishEvery {
				break
			}
			if r.deadline != nil {
				if err := r.deadline.Pause(); err != nil {
					e.logf("roll: pause: %v", err)
				}
			} else {
				e.clk.Sleep(2 * time.Millisecond)
			}
			continue
		}
		if err != nil {
			// Overrun or a transport error: the stream lost words. Say so, and
			// start a new one (placed by the host clock) on the next step.
			r.losses++
			raw, _ := e.sram.StreamStatus()
			e.logf("roll: stream ended after %d words, %.1f ms since the last block: %v (status %+v)", r.expected,
				float64(e.clk.Now().Sub(r.lastBlock))/float64(time.Millisecond), err, raw)
			e.rollStreamStop(r)
			break
		}
		r.expected = block.FirstWord + uint64(block.Words)
		r.lastBlock = e.clk.Now()
		if e.clk.Now().Sub(start) > time.Second {
			break // keep the view alive even if a bank is always ready
		}
	}
	if len(w.q1) == 0 {
		return
	}
	now := e.clk.Now()
	gap := 0
	if r.first {
		// The first data of a (re)started stream: place it by the host clock.
		if !r.lastEnd.IsZero() {
			missing := now.Sub(r.lastEnd).Seconds() - float64(2*len(w.q1))*p.sampleS // two view samples a word
			gap = max(0, int(math.Round(missing/p.sampleS)))
		}
		r.first = false
	}
	q1, q2 := e.peakSamples(r, w.raw)
	r.push(gap, q1, q2)
	r.lastEnd, r.lastPub = now, now
	e.rollPublish(r, uint32(1)<<p.log, "roll view: continuous peak-detect stream (stream image)",
		fmt.Sprintf("roll stream (%d samples, %d losses)", p.screen, r.losses),
		fmt.Sprintf("roll stream %d words this update, ordinal %d", len(w.q1), r.expected))
}

// streamPumper is the bus worker's stream-bank pump (bus.Dev with a worker).
type streamPumper interface {
	SetIsolatedStreamPump(bool) (bool, error)
	ReadIsolatedStream([]byte) (uint64, int, bool, error)
}

// peakSamples turns peak-detect stream words ({max2,max1,min2,min1}) into
// time-ordered view samples, two per bucket: converter offsets are removed as
// for an envelope recall, and each pair is ordered to continue from the view's
// newest sample (orderEnvelope).
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-035
func (e *Engine) peakSamples(r *rollView, raw []byte) ([]uint16, []uint16) {
	n := len(raw) / 4
	// Two leading slots carry the view's newest sample for the ordering.
	c := [2][]uint8{make([]uint8, 2+2*n), make([]uint8, 2+2*n)}
	q := [2][]uint16{make([]uint16, 2+2*n), make([]uint16, 2+2*n)}
	for i := 0; i < n; i++ {
		b := raw[4*i : 4*i+4]
		c[0][2+2*i], c[0][3+2*i] = b[0], b[2] // CH1 min, max
		c[1][2+2*i], c[1][3+2*i] = b[1], b[3] // CH2 min, max
	}
	out := [2][]uint16{}
	for ch := range c {
		body, qb := c[ch][2:], q[ch][2:]
		if cal := e.interleaveCalibration; cal != nil {
			cal.debiasEnvelope(ch, body, qb, false)
		} else {
			for i, v := range body {
				qb[i] = uint16(v) << 8
			}
		}
		last := r.q1
		if ch == 1 {
			last = r.q2
		}
		if r.filled > 0 && len(last) > 0 {
			q[ch][1] = last[len(last)-1]
		} else if len(qb) > 0 {
			q[ch][1] = qb[0]
		}
		c[ch][1] = roundQ8(q[ch][1])
		orderEnvelope(c[ch], q[ch])
		out[ch] = q[ch][2:]
	}
	return out[0], out[1]
}

// rollStreamStop ends a stream session, if one runs.
// TRLC-LINKS: REQ-SDS-035
func (e *Engine) rollStreamStop(r *rollView) {
	if !r.streaming {
		return
	}
	if r.isolated {
		if _, err := e.b.(streamPumper).SetIsolatedStreamPump(false); err != nil {
			e.busErr(err)
		}
		r.isolated = false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	if err := e.sram.StopStream(ctx); err != nil {
		e.busErr(err)
	}
	cancel()
	if r.deadline != nil {
		if err := r.deadline.Close(); err != nil {
			e.logf("roll: stream scheduling: %v", err)
		}
		r.deadline = nil
	}
	r.streaming, r.first = false, true
}
