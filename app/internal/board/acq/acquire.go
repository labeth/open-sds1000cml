// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"errors"
	"time"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// ErrDataFault is Wait's answer for a capture whose interleave FIFO
// overflowed: its record has missing samples.
var ErrDataFault = errors.New("SRAM acquisition FIFO overflow")

// The bounds of the fabric's commands.
const (
	armTimeout   = 3 * time.Second
	forceTimeout = time.Second
	haltTimeout  = time.Second
)

type acquirerFacet Board

// planPart is a configured plan as the plan fields of the fabric's capture
// configuration: the source, the decimation, picking, and the record's
// words with those before the trigger the plan asks for. It is small and
// comparable: a plan configured again is known by it.
type planPart struct {
	source    sramcapture.Source
	decimLog2 uint8
	pick      bool
	words     uint32
	pre       uint32 // words before the trigger, before a trigger's own delay
	perWord   int
	sampleS   float64
}

// Configure stages plan p: its decimation, its words around the trigger
// (PretrigFrac's share of the record and PretrigRoom beyond it) and
// whether decimation picks. The trigger is staged by Trigger.Program; the
// next Arm commits both in one write, in either order. A plan that
// configures what is staged changes nothing.
// TRLC-LINKS: REQ-SDS-008
func (a *acquirerFacet) Configure(p hal.Plan) error {
	b := (*Board)(a)
	pp, err := planConfig(p)
	if err != nil {
		return err
	}
	b.plan = p
	if pp != b.planned {
		b.planned = pp
		b.restaged()
	}
	return nil
}

// restaged notes a change of the staged plan or trigger: the composed
// configuration is due again.
// TRLC-LINKS: REQ-SDS-008
func (b *Board) restaged() {
	b.stageGen++
	b.composedOK = false
}

// StageGen counts the changes of the staged capture: the owner knows a
// configuration it took from Staged is still the staged one while it
// holds.
// TRLC-LINKS: REQ-SDS-008
func (b *Board) StageGen() uint64 { return b.stageGen }

// Staged is the configuration the next Arm commits: the configured plan
// and the programmed trigger.
// TRLC-LINKS: REQ-SDS-008
func (b *Board) Staged() sramcapture.Config { return *b.staged() }

// StagedConfig is Staged in place, valid until the next Program or
// Configure: the owner copies it once into its capture.
// TRLC-LINKS: REQ-SDS-008
func (b *Board) StagedConfig() *sramcapture.Config { return b.staged() }

// staged is the staged configuration, composed once after each change:
// it is 2.7 KB, and the owner reads it at every arm.
// TRLC-LINKS: REQ-SDS-008
func (b *Board) staged() *sramcapture.Config {
	if !b.composedOK {
		composeInto(&b.composed, &b.planned, b.trig.stagedPart())
		b.composedOK = true
	}
	return &b.composed
}

// StagedTrigger is the programmed trigger's fields of the staged
// configuration (the plan's are not set), valid until the next Program.
// TRLC-LINKS: REQ-SDS-011
func (b *Board) StagedTrigger() *sramcapture.Config { return &b.trig.stagedPart().cfg }

// composeInto writes plan pp with trigger tp (nil: none) into c as one
// capture configuration: the zone trigger's fire delay is held before
// the trigger too.
// TRLC-LINKS: REQ-SDS-008, REQ-SDS-014
func composeInto(c *sramcapture.Config, pp *planPart, tp *trigPart) {
	var zoneDelayS float64
	if tp != nil {
		*c = tp.cfg
		zoneDelayS = tp.zoneDelayS
	} else {
		*c = sramcapture.Config{}
	}
	c.Source, c.DecimationLog2, c.Pick = pp.source, pp.decimLog2, pp.pick
	if pp.words == 0 {
		return
	}
	pre := pp.pre
	if c.Zones.Enabled() && pp.sampleS > 0 {
		pre = min(pre+uint32(zoneDelayS/pp.sampleS)>>(pp.perWord-1), pp.words-1)
	}
	c.PreWords, c.PostWords = pre, pp.words-pre
}

// Draft stages a capture's plan and trigger as Configure and Program do,
// apart from what the next Arm commits: the owner drafts its settings at
// every look while a capture waits, to see whether they changed. A draft
// is reused from look to look (Reset); Board.Draft makes one.
type Draft struct {
	b     *Board
	plan  planPart
	trig  *trigPart // the drafted trigger; nil: none; the board's staged one when it drafted that
	parts [2]trigPart
	none  trigPart // no trigger: never written
}

// Draft is a draft over the board's loaded image and front end, staging
// nothing.
// TRLC-LINKS: REQ-SDS-008
func (b *Board) Draft() *Draft { return &Draft{b: b} }

// Of reports whether the draft drafts on board b.
// TRLC-LINKS: REQ-SDS-008
func (d *Draft) Of(b *Board) bool { return d.b == b }

// Reset empties the draft for another look.
// TRLC-LINKS: REQ-SDS-008
func (d *Draft) Reset() { d.plan, d.trig = planPart{}, nil }

// Configure drafts plan p.
// TRLC-LINKS: REQ-SDS-008
func (d *Draft) Configure(p hal.Plan) error {
	pp, err := planConfig(p)
	if err == nil {
		d.plan = pp
	}
	return err
}

// Program drafts trigger s. A trigger the board has staged from the same
// spec and inputs is taken as it is, without planning it again.
// TRLC-LINKS: REQ-SDS-011
func (d *Draft) Program(s hal.TriggerSpec) error {
	b := d.b
	in := b.trigInputs()
	if b.trig.kept.matches(&s, &in) {
		d.trig = b.trig.stagedPart()
		return nil
	}
	buf := &d.parts[0]
	if d.trig == buf {
		buf = &d.parts[1]
	}
	if err := b.trigConfig(&s, &in, buf); err != nil {
		return err
	}
	d.trig = buf
	return nil
}

// Staged reports that the draft is the board's staged capture: the same
// plan and the staged trigger, so its configuration is Staged's.
// TRLC-LINKS: REQ-SDS-008
func (d *Draft) Staged() bool {
	return d.trig != nil && d.trig == d.b.trig.stagedPart() && d.plan == d.b.planned
}

// Trigger is the drafted trigger's fields of the configuration (the
// plan's are not set), valid until the draft or the board programs again.
// TRLC-LINKS: REQ-SDS-011
func (d *Draft) Trigger() *sramcapture.Config {
	if d.trig == nil {
		return &d.none.cfg
	}
	return &d.trig.cfg
}

// Config is the drafted capture's configuration.
// TRLC-LINKS: REQ-SDS-008
func (d *Draft) Config() sramcapture.Config {
	var c sramcapture.Config
	d.ConfigInto(&c)
	return c
}

// ConfigInto writes the drafted capture's configuration into c.
// TRLC-LINKS: REQ-SDS-008
func (d *Draft) ConfigInto(c *sramcapture.Config) { composeInto(c, &d.plan, d.trig) }

// Arm applies the front end's staged settings and writes its due relay and
// gain words, commits the staged capture
// and starts it. It is a commit arm when the front end applied a setting,
// the staged plan or trigger changed since the last arm, or it is the
// first arm since Stop (CommitArms counts them); a plain re-arm commits the
// same configuration again. OWNER.
// TRLC-LINKS: REQ-SDS-008
func (a *acquirerFacet) Arm() error {
	b := (*Board)(a)
	if b.mem == nil {
		return hal.Refuse("acquisition", "no SRAM capture fabric")
	}
	b.followACLevel()
	commit := b.stopped || b.stageGen != b.armedGen
	if b.frontEnd != nil {
		applied, err := b.writeFrontEnd()
		if err != nil {
			return err
		}
		commit = commit || applied
	}
	ctx, cancel := context.WithTimeout(context.Background(), armTimeout)
	cfg := b.staged()
	err := b.mem.Arm(ctx, *cfg)
	cancel()
	b.count(func(d *boardDiag) { d.Arms++ })
	if err != nil {
		b.state = hal.Idle
		return err
	}
	if commit {
		b.commitArms++
	}
	b.state, b.forced, b.stopped, b.armedGen = hal.Armed, false, false, b.stageGen
	b.armedPlan, b.armedFree, b.armedNode, b.armedNorm = b.plan, b.trig.free, b.trig.node, b.trig.norm
	b.armedPlace = placeOf(cfg, b.trig.stagedPart().zoneDelayS)
	b.cond.armedPick = cfg.Pick
	return nil
}

// CommitArms counts the commit arms (Arm) so far. OWNER.
// TRLC-LINKS: REQ-SDS-008
func (b *Board) CommitArms() int { return b.commitArms }

// Wait polls the capture: with timeout 0 once, else until the record is
// ready, the timeout passes (ErrTimeout) or ctx ends (its error). A ready record is frozen and read for its
// length and positions (Record). On ErrTimeout the status still says
// whether the hardware triggered, which AUTO's forced trigger waits for.
// OWNER.
// TRLC-LINKS: REQ-SDS-008
func (a *acquirerFacet) Wait(ctx context.Context, timeout time.Duration) (hal.Status, error) {
	b := (*Board)(a)
	deadline := time.Now().Add(timeout)
	for {
		m, err := b.mem.Poll()
		if err != nil {
			return hal.Status{}, err
		}
		if m.DataFault {
			return hal.Status{Triggered: m.Triggered}, ErrDataFault
		}
		if m.Frozen && m.Ready {
			m, err = b.mem.Status()
			b.rec = m
			if err != nil {
				return hal.Status{}, err
			}
			if m.DataFault {
				return hal.Status{Triggered: m.Triggered}, ErrDataFault
			}
			b.state = hal.Ready
			// a forced record is no trigger, whatever the fabric's flag says
			// after the force (the owner forces only an untriggered capture)
			b.recStatus = hal.Status{Triggered: m.Triggered && !b.forced, Forced: b.forced, Done: true, Coherent: true, Record: recordID(m)}
			return b.recStatus, nil
		}
		if err := ctx.Err(); err != nil {
			return hal.Status{Triggered: m.Triggered}, err
		}
		if timeout <= 0 || !time.Now().Before(deadline) {
			return hal.Status{Triggered: m.Triggered}, hal.ErrTimeout
		}
		time.Sleep(min(2*time.Millisecond, time.Until(deadline)))
	}
}

// autoWaitBase is how long AUTO waits for a trigger beyond the time the
// record takes to fill before its trigger.
const autoWaitBase = 100 * time.Millisecond

var _ hal.AutoWaiter = (*acquirerFacet)(nil)

// AutoWait is how long a capture of the staged plan waits in AUTO before
// the owner forces it: the fabric triggers only on its trigger, so AUTO's
// free run is the owner's forced trigger. It waits autoWaitBase and the
// time the record takes to fill before its trigger, so a forced record
// holds samples on both sides. OWNER.
// TRLC-LINKS: REQ-SDS-009, REQ-SDS-011
func (a *acquirerFacet) AutoWait() time.Duration {
	p := (*Board)(a).planned
	return autoWaitBase + time.Duration(float64(p.pre)*float64(p.perWord)*p.sampleS*float64(time.Second))
}

// Force forces the waiting capture's trigger. OWNER.
// TRLC-LINKS: REQ-SDS-008
func (a *acquirerFacet) Force() error {
	b := (*Board)(a)
	ctx, cancel := context.WithTimeout(context.Background(), forceTimeout)
	defer cancel()
	b.count(func(d *boardDiag) { d.Forces++ })
	if err := b.mem.Force(ctx); err != nil {
		return err
	}
	b.forced = true
	return nil
}

// Stop halts the capture; a frozen record stays readable. OWNER.
// TRLC-LINKS: REQ-SDS-008
func (a *acquirerFacet) Stop() error {
	b := (*Board)(a)
	ctx, cancel := context.WithTimeout(context.Background(), haltTimeout)
	defer cancel()
	b.count(func(d *boardDiag) { d.Halts++ })
	b.stopped = true
	if b.state != hal.Ready {
		b.state = hal.Idle
	}
	return b.mem.Halt(ctx)
}

// State is where the acquisition is. ANY from the owner's view; the board
// keeps no lock, so other goroutines read a recent value.
// TRLC-LINKS: REQ-SDS-008
func (a *acquirerFacet) State() hal.AcqState { return a.state }

// Record is the halted record's metadata: its length, trigger word,
// samples a word and rate, which the owner's window arithmetic reads.
// TRLC-LINKS: REQ-SDS-008
func (b *Board) Record() sramcapture.Metadata { return b.rec }

// ReleaseCapture is the owner leaving an armed capture to another owner of
// the record memory (a decoded session, a stream): nothing is armed for the
// Acquirer any more, so the front end's writes go at the next commit
// point. OWNER.
// TRLC-LINKS: REQ-SDS-008
func (b *Board) ReleaseCapture() {
	if b.state == hal.Armed {
		b.state = hal.Idle
	}
}

// FrontEndPending reports that the front end has a hardware write due,
// which the board makes once no capture is armed: an owner waiting on a
// capture abandons it for a fresh one. ANY.
// TRLC-LINKS: REQ-SDS-008, REQ-SDS-015
func (b *Board) FrontEndPending() bool { return b.frontEnd != nil && b.frontEnd.pendingWrite() }

// SetRecord makes m the record Readout reads: a record the board did not
// halt itself (a stopped record read again).
// TRLC-LINKS: REQ-SDS-008
func (b *Board) SetRecord(m sramcapture.Metadata) { b.rec = m; b.state = hal.Ready }

// AdoptRecord makes m, captured with configuration cfg by a decoded stream
// outside the owner's plans, the record Readout reads, read as the
// engine's stopped loop read an adopted record: a full-rate record at 2 ns,
// a decimated one (a software stream's capture) at its own rate and
// decimation, its screen at the timebase now, triggered on an edge. OWNER.
// TRLC-LINKS: REQ-SDS-008, REQ-SDS-013
func (b *Board) AdoptRecord(m sramcapture.Metadata, cfg sramcapture.Config) {
	p := SRAMPlan{Samples: int(m.Length) * 2, SampleS: 2e-9}
	if m.SampleRateHz > 0 && m.SamplesPerWord > 0 {
		p = SRAMPlan{Log: cfg.DecimationLog2, Samples: int(m.Length) * int(m.SamplesPerWord), SampleS: 1 / m.SampleRateHz}
	}
	plan := b.plan
	p = RetainedSRAMPlan(p, plan.Request.TimePerDivS)
	plan.SampleS, plan.Depth, plan.AcqDecim, plan.WinSamples = p.SampleS, p.Samples, 1<<p.Log, p.Screen
	plan.Content = sample.ContentSetOf(sample.RawFullRate, sample.PeakPairs)
	if p.Log != 0 {
		plan.Content = sample.ContentSetOf(sample.RawDecimated, sample.PeakPairs)
	}
	b.rec, b.state = m, hal.Ready
	b.armedPlan, b.armedFree, b.armedNode, b.armedNorm = plan, false, hal.NodeEdge, true
	b.armedPlace, b.cond.armedPick = triggerPlace{}, cfg.Pick
	b.recStatus = hal.Status{Triggered: m.Triggered, Done: true, Coherent: true, Record: recordID(m)}
}
