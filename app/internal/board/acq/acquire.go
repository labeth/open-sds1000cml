// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"errors"
	"time"

	"github.com/labeth/open-dso-core/hal"
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

// Configure stages plan p: its decimation, its words around the trigger and
// whether decimation picks. The trigger is staged by Trigger.Program; the
// next Arm commits both in one write.
func (a *acquirerFacet) Configure(p hal.Plan) error {
	b := (*Board)(a)
	c, err := planConfig(p)
	if err != nil {
		return err
	}
	b.plan = p
	b.staged.Source, b.staged.DecimationLog2, b.staged.Pick = c.Source, c.DecimationLog2, c.Pick
	b.staged.PreWords, b.staged.PostWords = c.PreWords, c.PostWords
	return nil
}

// StageCapture stages a whole capture configuration, plan and trigger as
// the fabric takes them in its one arm write. The engine's planner builds
// it from the instrument's settings until the trigger settings reach the
// board in volts. COMMIT.
func (b *Board) StageCapture(c sramcapture.Config) { b.staged = c }

// Staged is the configuration the next Arm commits.
func (b *Board) Staged() sramcapture.Config { return b.staged }

// Arm commits the staged capture and starts it. OWNER.
func (a *acquirerFacet) Arm() error {
	b := (*Board)(a)
	if b.mem == nil {
		return hal.Refuse("acquisition", "no SRAM capture fabric")
	}
	ctx, cancel := context.WithTimeout(context.Background(), armTimeout)
	err := b.mem.Arm(ctx, b.staged)
	cancel()
	b.count(func(d *boardDiag) { d.Arms++ })
	if err != nil {
		b.state = hal.Idle
		return err
	}
	b.state = hal.Armed
	return nil
}

// Wait polls the capture: with timeout 0 once, else until the record is
// ready or the timeout passes. A ready record is frozen and read for its
// length and positions (Record). On ErrTimeout the status still says
// whether the hardware triggered, which AUTO's forced trigger waits for.
// OWNER.
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
			return hal.Status{Triggered: m.Triggered, Done: true, Coherent: true, Record: recordID(m)}, nil
		}
		if timeout <= 0 || !time.Now().Before(deadline) || ctx.Err() != nil {
			return hal.Status{Triggered: m.Triggered}, hal.ErrTimeout
		}
		time.Sleep(min(2*time.Millisecond, time.Until(deadline)))
	}
}

// Force forces the waiting capture's trigger. OWNER.
func (a *acquirerFacet) Force() error {
	b := (*Board)(a)
	ctx, cancel := context.WithTimeout(context.Background(), forceTimeout)
	defer cancel()
	b.count(func(d *boardDiag) { d.Forces++ })
	return b.mem.Force(ctx)
}

// Stop halts the capture; a frozen record stays readable. OWNER.
func (a *acquirerFacet) Stop() error {
	b := (*Board)(a)
	ctx, cancel := context.WithTimeout(context.Background(), haltTimeout)
	defer cancel()
	b.count(func(d *boardDiag) { d.Halts++ })
	if b.state != hal.Ready {
		b.state = hal.Idle
	}
	return b.mem.Halt(ctx)
}

// State is where the acquisition is. ANY from the owner's view; the board
// keeps no lock, so other goroutines read a recent value.
func (a *acquirerFacet) State() hal.AcqState { return a.state }

// Record is the halted record's metadata: its length, trigger word,
// samples a word and rate, which the owner's window arithmetic reads.
func (b *Board) Record() sramcapture.Metadata { return b.rec }

// SetRecord makes m the record Readout reads: a record the board did not
// halt itself (a decoded stream's adopted capture, a stopped record read
// again).
func (b *Board) SetRecord(m sramcapture.Metadata) { b.rec = m; b.state = hal.Ready }
