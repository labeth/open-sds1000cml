// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// sramCycle is the SRAM fabric's part of the core owner loop's pass: the
// streams, images and decoded captures that claim the acquisition, the
// capture planned as one fabric configuration, the record's windows read
// and conditioned, and the frame judged. The loop itself (the wait, the
// forced triggers, the publish gate, the single shot, the pacing) is the
// core's. Its fields carry the retained record between passes: a stopped
// record is shown again at a new V/div, offset or timebase without a new
// capture.
type sramCycle struct {
	e *Engine

	previousFrozen  time.Time
	cond            frameConditioner
	average         core.SramAverage
	retained        bool // a record is held for a stopped replay
	recalled        bool // and it was shown whole
	savedM          sramcapture.Metadata
	savedCfg        sramcapture.Config
	savedPlan       SRAMPlan
	savedNorm       bool
	savedForced     bool
	savedTP         core.TrigParams
	savedScale      [2]core.ChScale // front-end scale the retained record was captured at
	savedShownTrigd bool            // the retained capture was published triggered
	edges           edgeDetail      // the last edge windows read, carried to the frames between reads
	shownScaleGen   uint64          // scaleGen when the shown frame was produced
	held            heldRecall      // the stopped record's recall, kept for re-scaled replays
	roll            rollView        // the slow-timebase scrolling view (sram_roll.go)
	streamShown     bool            // stopped on a decoded stream's record: its image stays

	// this pass's capture
	cfg     sramcapture.Config
	plan    SRAMPlan
	tdiv    float64
	norm    bool
	tp      core.TrigParams
	replay  bool
	feAtArm uint64 // the front end's write generation the capture is armed under
	symbols int    // the symbol trigger's match count at the arm, -1 for none

	// this pass's stage times, for FrameStages
	armAt, armedAt, frozenAt, recalledAt, detailAt, conditionedAt time.Time
}

var _ core.Cycle = (*sramCycle)(nil)

// newSRAMLoop is the core owner loop over the SRAM fabric's cycle.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-008
func (e *Engine) newSRAMLoop() *core.Loop {
	return core.NewLoop(core.LoopConfig{
		Acquirer: e.acquirer(),
		Cycle:    &sramCycle{e: e},
		Clock:    e.clk,
		Stopping: e.stopReq.Load,
		OnPanic:  e.parked,
	})
}

// Open starts the fabric's loop: the read progress hook, the probed
// matchers, the converters.
// TRLC-LINKS: REQ-SDS-003, REQ-SDS-231
func (c *sramCycle) Open() {
	e := c.e
	// A press lights its lamp mid-recall, not after a multi-second deep recall.
	e.hw().SetReadProgress(func(done, total int) {
		e.busyAll.Store(int64(total))
		e.busyDone.Store(int64(done))
	})
	e.probeSerialHardware()
	e.mu.Lock()
	e.stats.Image = acq.ImageGeneralName
	e.mu.Unlock()
	e.logf("engine: full-depth SRAM capture; software protocol qualification has capture dead time")
	e.startConverters("the start") // as after every image load (loadImage)
}

// Close ends a decoded capture and a software stream still running.
// TRLC-LINKS: REQ-SDS-013
func (c *sramCycle) Close() {
	e := c.e
	if e.decodedSession != nil {
		if err := e.acquirer().Stop(); err != nil {
			e.logf("decoded shutdown halt: %v", err)
		}
		if _, err := e.sram.EnableDecodedEvents(false); err != nil {
			e.logf("decoded shutdown stream: %v", err)
		}
	}
	if err := e.closeDecodedDeadline(); err != nil {
		e.logf("decoded shutdown scheduling: %v", err)
	}
	if e.soft != nil {
		e.softStreamStop(e.soft)
	}
}

// Serve is the commit point: the decoded capture's requests and events,
// the board's housekeeping.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-008
func (c *sramCycle) Serve() { c.e.serviceCommands() }

// Claim lets the streams, the image jobs and a decoded capture take the
// pass, adopts a decoded stream's record as the stopped capture, checks
// the converters between acquisitions, idles while stopped with nothing to
// show again, and runs the roll view.
// TRLC-LINKS: REQ-SDS-003, REQ-SDS-008, REQ-SDS-013, REQ-SDS-033, REQ-SDS-231
func (c *sramCycle) Claim(context.Context) bool {
	e := c.e
	e.setBusy("", 0) // set again only around a recall and its processing
	if c.roll.streaming && (!e.rollActive() || e.protocolImage != acq.ImageStream || len(e.sramJobs) != 0) {
		e.rollStreamStop(&c.roll) // leaving roll: free the fabric for records
	}
	// A decoded stream's triggered record becomes the stopped capture: it is
	// recalled at full depth below, before any image switch can drop it.
	adopting := false
	// Only once the capture has ended: the adoption and the end are two
	// owner calls, and an iteration between them must leave the record.
	var a *adoptedRecord
	if e.decodedSession == nil && e.soft == nil {
		a = e.takeAdopted()
	}
	if a != nil {
		c.savedM, c.savedCfg = a.m, a.cfg
		c.savedScale = e.liveScale()
		c.savedPlan = SRAMPlan{Samples: int(a.m.Length) * 2, SampleS: 2e-9}
		if a.m.SampleRateHz > 0 && a.m.SamplesPerWord > 0 { // a decimated record (a software stream's capture)
			c.savedPlan = SRAMPlan{Log: a.cfg.DecimationLog2, Samples: int(a.m.Length) * int(a.m.SamplesPerWord), SampleS: 1 / a.m.SampleRateHz}
		}
		c.savedPlan = acq.RetainedSRAMPlan(c.savedPlan, e.Snapshot().TdivS)
		c.savedNorm, c.savedForced, c.savedTP = true, false, core.TrigParams{Typ: TrigEdge}
		c.retained, c.recalled, adopting = true, false, true
		e.logf("engine: showing the decoded stream's record (%d words, trigger word %d)", a.m.Length, a.m.TriggerIndex)
	}
	// Stopped on a stream's record, the image the stream ran on stays: a
	// switch drops the record, and the stream started again (RUN/STOP in
	// BUS Stream) needs that image back (two loads of about four seconds).
	c.streamShown = (c.streamShown || adopting) && c.retained && !e.running.Load()
	if e.soft != nil { // a software-decoded stream owns the acquisition (soft_stream.go)
		c.retained, c.recalled = false, false
		e.softStep()
		return true
	}
	if (e.soundWanted() || e.soundStreaming()) && e.soundStep() { // the audio stream (sound_stream.go)
		c.retained, c.recalled = false, false
		return true
	}
	// Image-switching jobs run only here, between acquisitions. The switch
	// drops the fabric's record metadata, so the retained record is gone.
	if !adopting && e.decodedSession == nil && e.serviceSRAMJobs() {
		c.retained, c.recalled = false, false
		return true
	}
	if !adopting && !c.streamShown && e.decodedSession == nil && !e.clk.Now().Before(e.decodedStarting) && e.images != nil && e.serviceImage() {
		c.retained, c.recalled = false, false
		return true
	}
	if e.decodedSession != nil {
		c.retained, c.recalled = false, false
		e.decodedIdle()
		return true
	}
	// Between acquisitions: a stopped record stays (a recovery reloads).
	if e.running.Load() && !e.rollActive() {
		e.checkCoresDue()
	}
	e.bumpFrames()
	e.viewHeld.Store(c.retained)
	e.mu.Lock()
	viewPending := e.pendSet
	e.mu.Unlock()
	// A stopped record follows a V/div or offset change: replay it at the
	// new scale instead of reading its codes at the old one.
	viewPending = viewPending || e.scaleGen.Load() != c.shownScaleGen
	if !e.running.Load() && (!c.retained || (c.recalled && !viewPending)) {
		e.clk.Sleep(20 * time.Millisecond)
		return true
	}
	rolling := e.rollActive()
	e.rolling.Store(rolling)
	if rolling {
		c.retained, c.recalled = false, false // the roll view is not a retained record
		e.viewHeld.Store(false)
		if e.protocolImage == acq.ImageStream {
			e.rollStreamStep(&c.roll)
		} else {
			e.rollStep(&c.roll)
		}
		return true
	}
	return false
}

// Plan programs the capture's trigger and configures its plan on the board
// from the settings, or takes the retained record's to replay it.
// TRLC-LINKS: REQ-SDS-008, REQ-SDS-009, REQ-SDS-010, REQ-SDS-011
func (c *sramCycle) Plan(context.Context) (core.Capture, bool) {
	e := c.e
	c.replay = !e.running.Load() && c.retained
	if c.replay {
		// the settings only for the timebase the stopped record is shown at
		_, c.tdiv, _, _ = e.sramConfigInto(&c.cfg)
		c.cfg, c.plan, c.norm, c.tp = c.savedCfg, c.savedPlan, c.savedNorm, c.savedTP
		c.plan = acq.RetainedSRAMPlan(c.plan, c.tdiv)
	} else {
		// staged on the board: the trigger programmed, the plan configured
		c.plan, c.tdiv, c.norm, c.tp = e.planCapture(boardTarget{e})
		c.cfg = e.hw().Staged()
	}
	e.mu.Lock()
	if e.pendSet {
		e.band = e.pendBand
		e.pendSet = false
	}
	e.stats.BandKind = "sram"
	e.stats.HaltMode = "freeze-read-rearm"
	e.noteBackendsLocked(c.cfg, c.tp)
	e.stats.TdivS = c.tdiv
	e.stats.DisplayedS = math.Min(c.tdiv, float64(c.plan.Samples)*c.plan.SampleS/10)
	e.stats.RecordS = float64(c.plan.Samples) * c.plan.SampleS
	e.stats.WinCols = c.plan.Screen
	e.mu.Unlock()
	c.feAtArm = e.feGen.Load()
	c.symbols = -1
	if !c.replay {
		c.retained = false
		c.held = heldRecall{} // a new capture: the stopped record's copies go
		c.symbols = e.symbolCount(c.cfg)
	}
	// AUTO waits for enough prehistory plus a bounded event wait. SINGLE/NORM
	// never fabricate a trigger.
	autoWait := 100*time.Millisecond + time.Duration(float64(c.cfg.PreWords)*float64(2-acq.BoolInt(c.plan.Log != 0))*c.plan.SampleS*1e9)
	return core.Capture{Replay: c.replay, Normal: c.cfg.Normal, Norm: c.norm, AutoWait: autoWait}, true
}

// Watch leaves the capture to a decoded or software stream that started,
// aborts it for an image job, and for a fresh capture when the settings
// changed.
// TRLC-LINKS: REQ-SDS-008, REQ-SDS-013
func (c *sramCycle) Watch(settings bool) core.Interrupt {
	e := c.e
	if settings {
		var next sramcapture.Config
		_, nextTdiv, nextNorm, nextTP := e.sramConfigInto(&next)
		if next != c.cfg || nextTdiv != c.tdiv || nextNorm != c.norm || nextTP != c.tp {
			return core.Abort
		}
		return core.Continue
	}
	if e.decodedSession != nil || e.soft != nil {
		return core.Leave
	}
	if len(e.sramJobs) != 0 {
		return core.Abort
	}
	return core.Continue
}

// ArmFailed counts the bus error and backs off.
// TRLC-LINKS: REQ-SDS-008
func (c *sramCycle) ArmFailed(err error) {
	c.e.busErr(err)
	c.e.sleepBeating(100 * time.Millisecond)
}

// Fault counts the wait's and the halt's errors; a data fault (the record
// lost samples) after the halt's.
// TRLC-LINKS: REQ-SDS-008, REQ-SDS-009
func (c *sramCycle) Fault(err, haltErr error) {
	e := c.e
	dataFault := errors.Is(err, acq.ErrDataFault)
	if err != nil && !dataFault {
		e.busErr(err)
	}
	if haltErr != nil {
		e.busErr(haltErr)
	}
	if dataFault {
		e.busErr(acq.ErrDataFault)
	}
}

// Frame reads the frozen record's window (a live envelope with its view
// and edge windows, a raw window, or the whole stopped record), conditions
// it, places and checks its trigger, runs the mask test, the average and
// the frequency response, and judges it.
// TRLC-LINKS: REQ-SDS-009, REQ-SDS-010, REQ-SDS-011, REQ-SDS-012, REQ-SDS-013, REQ-SDS-014, REQ-SDS-036, REQ-SDS-037, REQ-SDS-040
func (c *sramCycle) Frame(_ context.Context, _ core.Capture, a core.Armed) (*sample.Frame, core.Verdict, bool) { //nolint:gocyclo,funlen // one record's read, conditioning and judgement, read top to bottom against spec 03
	e := c.e
	cfg, plan, tdiv, norm, tp, replay := c.cfg, c.plan, c.tdiv, c.norm, c.tp, c.replay
	m, forced, userForced, armedAt := c.savedM, c.savedForced, false, a.ArmAt
	if !replay {
		m, forced, userForced, armedAt = e.hw().Record(), a.Forced, a.UserForced, a.ArmedAt
	}
	c.armAt, c.armedAt = a.ArmAt, armedAt
	if !replay {
		c.savedScale = e.liveScale()
		e.noteSymbolMatches(cfg, c.symbols, e.clk.Now().Sub(armedAt))
	}
	c.shownScaleGen = e.scaleGen.Load()
	e.shownScaleGen.Store(c.shownScaleGen)
	c.savedM, c.savedCfg, c.savedPlan, c.savedNorm, c.savedTP, c.savedForced = m, cfg, plan, norm, tp, forced
	fullRecall := !e.running.Load()
	c.retained, c.recalled = true, fullRecall
	e.viewHeld.Store(true)
	recallOffset, recallWords := uint32(0), m.Length
	posFrac := math.Float64frombits(e.trigPosFrac.Load())
	// Pulse, slope and video triggers are the FPGA's: nothing is searched
	// in software, only a video line moves back to its sync's start.
	softQualify := false
	// The capture that serves a SINGLE is first read as a live frame is, so
	// it shows and stops at once; the stop that follows reads its full
	// record (reading the whole record first kept the screen empty for
	// seconds at every timebase).
	singleShot := !replay && e.singleArmed.Load() && a.SingleGen == e.singleGen.Load()
	pre := 0 // samples the live window holds before the screen's
	if !fullRecall {
		// A software-qualified trigger needs room to find its event away
		// from the hardware edge and still fill the screen around it. The
		// FPGA's serial trigger fires at the match's end and software
		// moves the trigger point back to its start: the window holds
		// that much more before the trigger (twice the screen would halve
		// the live envelope's resolution, and a fast bus no longer
		// decodes).
		screen := plan.Screen
		switch {
		case softQualify:
			screen *= 2
		case e.serialMode.Load() == SerialTrigger:
			pre = e.serialPreRoom(plan.SampleS, screen)
		case cfg.Qualifier.Enabled && cfg.Qualifier.Kind == sramcapture.QualVideo && plan.SampleS > 0:
			pre = int(8e-6 / plan.SampleS) // a line sync's width
		}
		recallOffset, recallWords = acq.LiveSRAMWindowPre(m, screen, posFrac, pre)
	}
	// While running, a window larger than the display needs is read as a
	// peak-detect envelope; the deep record stays in SRAM for a stop.
	var bucket uint32
	envBuckets := uint32(sramcapture.EnvelopeBuckets)
	if m.FractionBits == 8 {
		envBuckets = sramcapture.EnvelopeBucketsQ8 // two words a bucket
	}
	readout := "full record"
	rawForLease := false // a live raw window read only for the raw leases: abandoned when they end
	if !fullRecall {
		why := e.envelopeBlock()
		if why == "" {
			bucket, recallOffset, recallWords = acq.EnvelopeWindow(recallOffset, recallWords, envBuckets, m.Length, uint32(pre/max(1, int(m.SamplesPerWord)))) //nolint:gosec // G115: pre is a sample offset within the record
		}
		readout = liveReadoutName(why, bucket, singleShot)
		rawForLease = why == blockRaw && !singleShot && !replay
	}
	e.mu.Lock()
	e.stats.LiveReadout = fmt.Sprintf("%s (%d words)", readout, recallWords)
	e.mu.Unlock()
	frozenAt := e.clk.Now()
	c.frozenAt = frozenAt
	f := e.arena.Write()
	q1, q2 := f.Precision(0), f.Precision(1)
	acq.ResetFrame(f)
	f.SetLen(int(recallWords) * int(m.SamplesPerWord))
	f.WinPoints, f.SampleS = plan.Screen, 1/m.SampleRateHz
	f.Snapshot = sample.Settings{TdivS: tdiv, DisplayedS: math.Min(tdiv, float64(plan.Samples)*plan.SampleS/10), Norm: norm}
	f.TriggerAt = float64(m.TriggerIndex-recallOffset) * float64(m.SamplesPerWord)
	f.Status = sample.Status{Triggered: m.Triggered && (cfg.Normal || m.HostTrigger) && !forced, Coherent: true, Done: true}
	f.Flags.Set(sample.Interp, plan.Screen < 800)
	f.AcqDecim = int(m.Decimation)
	f.TrigKind, f.TrigNode = sample.HardwareNode, "edge"
	f.Depth = int(m.Length) * int(m.SamplesPerWord)
	if !f.Status.Triggered {
		f.TrigKind, f.TrigNode = sample.Forced, ""
		f.TriggerAt = -1
	}
	f.CaptureS = f.SampleS
	if bucket != 0 {
		// Each output sample is half a bucket: a (min, max) pair per bucket.
		half := float64(bucket) * float64(m.SamplesPerWord) / 2
		f.SetLen(2 * int(envBuckets))
		f.SampleS *= half
		f.WinPoints = min(f.Len(), int(float64(plan.Screen)/half+0.5))
		f.Flags.Set(sample.Interp, false)
		if f.Status.Triggered || f.TriggerAt >= 0 {
			f.TriggerAt = float64(m.TriggerIndex-recallOffset) * float64(m.SamplesPerWord) / half
		}
	}
	if m.FractionBits == 8 {
		f.Traces[0].Precision = core.QBuffer(q1, f.Len())
		f.Traces[1].Precision = core.QBuffer(q2, f.Len())
	}
	got := 0                // samples a channel the read gave
	e.setBusy("reading", 0) // the recall's burst loop reports progress
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if rawForLease {
		e.rawMu.Lock()
		e.rawCancel = cancel
		e.rawMu.Unlock()
		e.abandonRawRead() // the last lease ended since the readout was chosen
	}
	e.rawMu.Lock()
	e.recallCancel = cancel // a SOUND lease starting abandons it (LeaseSound)
	e.rawMu.Unlock()
	recallAt := e.clk.Now()
	var views viewReads
	var err error
	if bucket != 0 {
		views, got, err = e.recallEnvelope(ctx, f, m, plan.Screen, recallOffset, recallWords, bucket, envBuckets)
	} else {
		got, err = c.readRaw(ctx, f, m, recallOffset, recallWords)
	}
	recalledAt := e.clk.Now()
	c.recalledAt = recalledAt
	cancelled := errors.Is(ctx.Err(), context.Canceled)
	soundCut := cancelled && e.soundStarting()
	abandoned := rawForLease && cancelled
	cancel()
	e.rawMu.Lock()
	e.recallCancel = nil
	if rawForLease {
		e.rawCancel = nil
	}
	e.rawMu.Unlock()
	if soundCut {
		// SOUND takes the fabric: the record would be dropped anyway.
		e.logf("engine: record read abandoned after %v: SOUND starts", recalledAt.Sub(recallAt).Round(time.Millisecond))
		return nil, core.Verdict{}, false
	}
	if abandoned {
		// Nobody wants the raw record any more: no frame, and the next
		// capture is the live view.
		e.logf("engine: raw read abandoned after %v: the raw leases ended", recalledAt.Sub(recallAt).Round(time.Millisecond))
		return nil, core.Verdict{}, false
	}
	if err != nil || got != f.Len() {
		if err == nil {
			err = errors.New("short SRAM frame")
		}
		e.busErr(err)
		return nil, core.Verdict{}, false
	}
	if bucket == 0 && !replay && m.Decimation <= 1 && m.FractionBits == 0 && m.SamplesPerWord == 2 {
		e.noteRecordCores(f.Codes8(0)[:f.Len()], f.Codes8(1)[:f.Len()]) // raw codes, before any conditioning
	}
	// A live envelope holds no edge shape, nor a mean where the signal is
	// faster than a bucket: read the trigger edge and the next edge the
	// other way at the record's rate, two short reads while it is still
	// frozen, for rise, fall, over/preshoot and such a signal's mean and
	// RMS.
	if bucket != 0 && !replay && f.Status.Triggered {
		if key := (edgeKey{averageCfg(cfg), c.savedScale}); c.edges.due(key, e.clk.Now()) {
			edge, opp := e.readEdgeWindows(f, m, cfg, tp, recallOffset, bucket, c.savedScale)
			c.edges.store(key, e.clk.Now(), edge, opp)
		}
		addEdgeWindows(f, &c.edges)
	} else if !replay {
		c.edges = edgeDetail{} // a capture of another kind: the windows no longer match
	}
	e.windows(f, &views, m, recallOffset, bucket, c.savedScale)
	detailAt := e.clk.Now()
	c.detailAt = detailAt
	if e.soundStarting() {
		// SOUND starts: a second of conditioning for a frame it drops.
		e.logf("engine: record dropped before processing: SOUND starts")
		return nil, core.Verdict{}, false
	}
	e.setBusy("processing", 0)
	// CPU-only conditioning: let a staged lamp latch meanwhile (SetLEDs).
	e.cpuStage.Unlock()
	condKey := heldCondKey{c.held.key, e.acqMode.Load(), e.eresLen.Load()}
	if replay && c.held.key == (heldKey{m, recallOffset, recallWords}) && c.held.cond.key == condKey {
		// The record's conditioning is independent of the view: reuse it.
		c.held.restoreCond(f, q1, q2)
		f.ContentID = c.held.cond.content
	} else {
		e.contentGen++
		f.ContentID = e.contentGen
		q1, q2 = e.conditionFrame(f, m, cfg, bucket, q1, q2, c.savedScale, &c.cond)
		if c.held.key == (heldKey{m, recallOffset, recallWords}) {
			c.held.storeCond(condKey, f)
			c.held.cond.content = f.ContentID
		}
	}
	if bucket != 0 {
		// After the (min, max)-pair calibration above: put each pair in
		// time order so the live envelope also serves as samples.
		acq.OrderEnvelopeHead(f.Codes8(0)[:f.Len()], f.Precision(0))
		acq.OrderEnvelopeHead(f.Codes8(1)[:f.Len()], f.Precision(1))
		acq.OrderEnvelope(f.Codes8(0)[:f.Len()], f.Precision(0))
		acq.OrderEnvelope(f.Codes8(1)[:f.Len()], f.Precision(1))
		f.Flags.Set(sample.Ordered, true)
	}
	switch {
	case fullRateFrame(bucket, m.Decimation, e.acqMode.Load()):
		f.Content = sample.RawFullRate
	case f.Content != sample.PeakPairs:
		f.Content = sample.RawDecimated
	}
	if fullRecall {
		f.Cond.Filter += "; stopped full record (single acquisition)"
	}
	if live := e.liveScale(); replay && live != c.savedScale {
		for ch := range live {
			cc, q := f.Codes8(0)[:f.Len()], f.Precision(0)
			if ch == 1 {
				cc, q = f.Codes8(1)[:f.Len()], f.Precision(1)
			}
			if len(q) != f.Len() {
				q = nil
			}
			core.RescaleCodes(cc, q, c.savedScale[ch], live[ch])
		}
		f.Cond.Filter += "; stopped record shown at the current V/div and offset"
	}
	conditionedAt := e.clk.Now()
	c.conditionedAt = conditionedAt
	e.cpuStage.Lock()
	if !replay {
		e.learnLevels(f, c.savedScale, c.feAtArm)
	}
	f.Stream.WindowS = float64(int64(float64(f.Len())*f.SampleS*1e9)) * 1e-9
	if !c.previousFrozen.IsZero() {
		f.Stream.GapS = float64(int64(a.ArmAt.Sub(c.previousFrozen))) * 1e-9
	}
	c.previousFrozen = frozenAt
	disc := f.Codes8(0)[:f.Len()]
	if cfg.TriggerChannel == 1 {
		disc = f.Codes8(1)[:f.Len()]
	}
	_, _, f.SourcePtp = core.Ptp(disc)
	qualified := f.Status.Triggered
	if cfg.Zones.Enabled() && f.Status.Triggered && f.TriggerAt >= 0 && f.SampleS > 0 {
		// The FPGA's zones decided and fired when the latest window ended:
		// the trigger point moves back to the edge they qualified, before
		// it is placed on its crossing.
		_, delayS, _ := e.planZones()
		f.TriggerAt -= delayS / f.SampleS
	}
	switch {
	case cfg.Qualifier.Enabled:
		// The FPGA's qualifier decided; software only places the trigger
		// point on the event's level crossing.
		if f.Status.Triggered {
			f.TrigKind, f.TrigNode = sample.HardwareNode, acq.QualName(cfg.Qualifier.Kind)
			q := f.Precision(0)
			if cfg.TriggerChannel == 1 {
				q = f.Precision(1)
			}
			f.TriggerAt = acq.PlaceQualified(f, disc, q, cfg.Qualifier, cfg.TriggerLevel)
		}
	case tp.Typ != TrigEdge && e.serialMode.Load() != SerialTrigger:
		// No qualifier could take it: nothing triggers. Software never
		// decides a trigger.
		qualified, f.Status.Triggered = false, false
		f.TrigKind, f.TrigNode = sample.Untriggered, ""
		f.TriggerAt = -1
	}
	// Place a plain edge trigger at its exact level crossing: the FPGA
	// index is word-granular. Envelope frames hold bucket extremes, not
	// samples in time order.
	if tp.Typ == TrigEdge && e.serialMode.Load() != SerialTrigger && f.Status.Triggered && !m.HostTrigger &&
		!e.refineEdgeTrigger(f, disc, cfg) && (!replay || !c.savedShownTrigd) {
		// The FPGA pointed at an edge the calibrated record does
		// not have (converter noise): software may check a trigger
		// the hardware pointed out and reject it (owner rule).
		// AUTO shows it untriggered, NORMAL and SINGLE keep waiting.
		// A stopped record whose capture was shown triggered keeps
		// that trigger (its envelope confirmed it), so the record
		// shows the screen the capture did, not its middle.
		f.Status.Triggered, qualified = false, false
		f.TrigKind, f.TrigNode = sample.Forced, ""
		f.TriggerAt = -1
	}
	e.noteARINCLevels(f)
	qualified = e.serialTriggerKind(f, m, cfg, qualified)
	switch {
	case cfg.Zones.Enabled():
		if f.Status.Triggered {
			f.TrigKind, f.TrigNode = sample.HardwareNode, "zone"
		}
	case e.zonesOn() && e.serialMode.Load() != SerialTrigger:
		// No zone hardware could take them: nothing triggers. Software
		// never decides a trigger.
		qualified, f.Status.Triggered = false, false
		f.TrigKind, f.TrigNode = sample.Untriggered, ""
		f.TriggerAt = -1
	}
	// Mask test: every triggered frame, published or held (zonemask.go).
	// Stop-on-fail stops on the offending frame and shows it.
	maskStop := false
	if qualified && !fullRecall && e.maskMode.Load() != MaskOff {
		posFrac := math.Float64frombits(e.trigPosFrac.Load())
		if failed, stop := e.maskEval(f, f.Len(), 0, f.TriggerAt, f.SampleS, posFrac); failed && stop {
			maskStop = true
		}
	}
	averageDone := true
	if (e.acqMode.Load() == AcqAverage || e.acqMode.Load() == AcqPrecision) && qualified && !fullRecall {
		averageDone = e.averageFrame(f, &c.average, cfg, tdiv, norm, tp, disc, q1, q2)
		// A stop keeps the averaged frame shown: reading the raw capture
		// again would replace the average with one of its captures.
		c.recalled = true
	} else {
		c.average.Reset()
	}
	e.mu.Lock()
	e.stats.Coherent++
	e.stats.HaltConfirm++
	e.stats.ValidDepth = f.Len()
	e.stats.MemDepth = plan.Samples
	e.stats.LastPtp = f.SourcePtp
	e.stats.SRAMRecallMs = float64(recalledAt.Sub(recallAt)) / float64(time.Millisecond)
	e.stats.ConditionMs = float64(conditionedAt.Sub(detailAt)) / float64(time.Millisecond)
	e.stats.DrainMs = float64(e.clk.Now().Sub(frozenAt)) / float64(time.Millisecond)
	e.mu.Unlock()
	// Software qualifiers may reject a capture that froze before the arm
	// polling loop could consume FORCE. Release that held capture once too.
	if !replay && e.forceReq.Swap(false) {
		userForced = true
	}
	if userForced {
		f.Status.Triggered = false
		f.TrigKind, f.TrigNode = sample.Forced, ""
	}
	// Bode measures every capture, triggered or not: its gain and phase
	// are the ratio of the two channels sampled together. Asking for a
	// trigger would stop the sweep, unsaid, with the level off the signal.
	if !replay && f.Content != sample.PeakPairs && e.bodeMode.Load() == BodeOn {
		e.bodeEval(f, f.Len(), f.SampleS)
	}
	if qualified {
		e.serialHeld.Reset()
		e.zoneHeld.Reset()
	}
	return f, core.Verdict{Qualified: qualified, Complete: averageDone, FullRecall: fullRecall, UserForced: userForced, MaskStop: maskStop}, true
}

// readRaw reads words [offset, offset+words) of record m raw into f, or
// restores them from the held recall when a stopped record is re-shown at
// a new V/div, offset or timebase (seconds of SRAM readout skipped); a
// whole record read is held for such replays. got is the samples a channel
// f holds.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-033, REQ-SDS-040
func (c *sramCycle) readRaw(ctx context.Context, f *sample.Frame, m sramcapture.Metadata, offset, words uint32) (got int, err error) {
	e := c.e
	key := heldKey{m, offset, words}
	if c.replay && c.held.key == key {
		return c.held.restore(f), nil
	}
	e.hw().SetRecord(m)
	e.readWins = append(e.readWins[:0], acq.Window(m, sample.Main, offset, words, 0))
	e.readFrames = append(e.readFrames[:0], f)
	err = e.reader().Read(ctx, hal.ReadRequest{Windows: e.readWins}, e.readFrames, e.yield())
	if err == nil {
		got = f.Len()
	}
	switch {
	case err == nil && offset == 0 && words == m.Length:
		c.held.store(key, f)
	case !c.replay:
		c.held.key = heldKey{} // a new capture: the held record is gone
	}
	return got, err
}

// Liveness is AUTO's rule for captures the serial or zone gate rejected.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-014
func (c *sramCycle) Liveness() bool { return c.e.autoGateLiveness() }

// Publish publishes the frame (or counts it held) and remembers whether
// the capture showed triggered.
// TRLC-LINKS: REQ-SDS-007, REQ-SDS-009
func (c *sramCycle) Publish(f *sample.Frame, _ core.Verdict, publish bool) {
	e := c.e
	if !c.replay {
		c.savedShownTrigd = publish && f.Status.Triggered
	}
	if !publish {
		e.mu.Lock()
		e.stats.Held++
		e.mu.Unlock()
		return
	}
	e.noteChannelMeans(f)
	e.mu.Lock()
	e.pubIdent = core.FrameIdent{TdivS: f.Snapshot.TdivS, SampleS: f.SampleS, PeakDetect: f.Content == sample.PeakPairs}
	e.mu.Unlock()
	e.seq++
	f.Seq = e.seq
	e.arena.Publish()
	e.mu.Lock()
	e.stats.Published++
	e.lastPublish = e.clk.Now()
	e.stats.Seq = e.seq
	e.lastPubAt = e.clk.Now()
	e.notePublish(e.lastPubAt)
	e.mu.Unlock()
}

// Done notes the pass's stage times.
// TRLC-LINKS: REQ-SDS-127
func (c *sramCycle) Done(doneAt time.Time, prevPace time.Duration) {
	e := c.e
	e.setBusy("", 0)
	ms := func(a, b time.Time) float64 { return float64(b.Sub(a)) / float64(time.Millisecond) }
	e.mu.Lock()
	e.stats.FrameStages = fmt.Sprintf("arm %.0f wait %.0f read %.0f detail %.0f proc %.0f post %.0f prev-pace %.0f (ms)",
		ms(c.armAt, c.armedAt), ms(c.armedAt, c.frozenAt), ms(c.frozenAt, c.recalledAt), ms(c.recalledAt, c.detailAt), ms(c.detailAt, c.conditionedAt), ms(c.conditionedAt, doneAt), float64(prevPace)/float64(time.Millisecond))
	e.mu.Unlock()
}

// Pacing is the trigger holdoff and the display's share of the core.
// TRLC-LINKS: REQ-SDS-008, REQ-SDS-011
func (c *sramCycle) Pacing() (holdoff, display time.Duration) {
	return time.Duration(c.e.holdoffNs.Load()), time.Duration(c.e.displayPaceNs.Load())
}

// Beat advances the liveness heartbeat.
// TRLC-LINKS: REQ-SDS-025
func (c *sramCycle) Beat() { c.e.beatN.Add(1) }

// Running reports RUN.
// TRLC-LINKS: REQ-SDS-008
func (c *sramCycle) Running() bool { return c.e.running.Load() }

// SingleArmed reports a single shot waiting.
// TRLC-LINKS: REQ-SDS-011
func (c *sramCycle) SingleArmed() bool { return c.e.singleArmed.Load() }

// SingleGen counts the SINGLE presses.
// TRLC-LINKS: REQ-SDS-011
func (c *sramCycle) SingleGen() uint64 { return c.e.singleGen.Load() }

// TakeForce takes a FORCE press.
// TRLC-LINKS: REQ-SDS-011
func (c *sramCycle) TakeForce() bool { return c.e.forceReq.Swap(false) }

// ForcePending looks at a FORCE press.
// TRLC-LINKS: REQ-SDS-011
func (c *sramCycle) ForcePending() bool { return c.e.forceReq.Load() }

// StopOnFail stops the run on a failed mask test, latched.
// TRLC-LINKS: REQ-SDS-014
func (c *sramCycle) StopOnFail() {
	e := c.e
	e.maskStopped.Store(true)
	e.running.Store(false)
	e.mu.Lock()
	e.stats.Running = false
	e.mu.Unlock()
}

// SingleDone stops the run after the single shot's frame; a completed
// average block is the result, with no raw record read over it.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-012
func (c *sramCycle) SingleDone(core.Verdict) {
	e := c.e
	if acq := e.acqMode.Load(); acq == AcqAverage || acq == AcqPrecision {
		c.recalled = true // the completed block is the result: no raw record over it
	}
	e.singleArmed.Store(false)
	e.running.Store(false)
	e.mu.Lock()
	e.stats.Running = false
	e.stats.Single = false
	e.mu.Unlock()
}
