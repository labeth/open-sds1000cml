// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"context"
	"errors"
	"fmt"
	"math/bits"
	"sync/atomic"
	"time"

	"github.com/labeth/open-sds1000cml/app/internal/board/acq"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/superres"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// ImageSwitcher reconfigures the acquisition FPGA between its images (the
// acquisition board's loader).
type ImageSwitcher = acq.ImageSwitcher

type (
	// FPGAStackRequest is the core's hardware stacking session.
	FPGAStackRequest = superres.StackRequest
	// FPGAStackResult is its outcome.
	FPGAStackResult = superres.StackResult
)

// SetImageSwitcher enables FPGA stacking. Call before Run.
// TRLC-LINKS: REQ-SDS-141
func (e *Engine) SetImageSwitcher(s ImageSwitcher) {
	e.images = s
	e.hw().SetImages(s)
	b, ok := s.(BitsImages)
	bitsImage.Store(ok && b.HasBits())
}

// BitsImages is an ImageSwitcher with the digital stream image
// (ADR-STREAM-BITS), which the decoded stream runs on when it has it.
type BitsImages = acq.BitsImages

// bitsImage reports the build's digital stream image to SoftStreamMode,
// which plans without an engine (the panel's decode menu asks it).
var bitsImage atomic.Bool

// sramCall runs an SRAM job on the owner goroutine at the top of the SRAM
// loop, where no arm or recall is in flight; any retained record is
// forgotten afterwards. A job whose caller gave up still runs to completion
// and restores the general image.
// TRLC-LINKS: REQ-SDS-141
func (e *Engine) sramCall(ctx context.Context, run func(context.Context) (any, error)) (any, error) {
	if e.sram == nil {
		return nil, errors.New("FPGA stacking requires the SRAM acquisition image")
	}
	return e.callOwner(ctx, e.sramJobs, func() (any, error) { return run(ctx) })
}

// serviceSRAMJobs reports whether a job ran (and so replaced the fabric).
// TRLC-LINKS: REQ-SDS-141
func (e *Engine) serviceSRAMJobs() bool {
	select {
	case job := <-e.sramJobs:
		if !job.Serve() {
			return false
		}
		e.beatN.Add(1)
		return true
	default:
		return false
	}
}

// FPGAStack loads the stacking image, stacks req.Records captures in the FPGA,
// and always restores the general image before returning.
// TRLC-LINKS: REQ-SDS-141
func (e *Engine) FPGAStack(ctx context.Context, req FPGAStackRequest) (FPGAStackResult, error) {
	if e.images == nil || !e.images.HasStack() {
		return FPGAStackResult{}, errors.New("FPGA stacking image not available in this build")
	}
	if req.Records < 1 || req.Records > 100000 {
		return FPGAStackResult{}, fmt.Errorf("records %d out of range", req.Records)
	}
	value, err := e.sramCall(ctx, func(ctx context.Context) (any, error) {
		if e.decodedSession != nil || e.soft != nil {
			return nil, errors.New("decoded capture is active")
		}
		cfg, _, _, _, _ := e.sramConfig()
		// Raw samples at the current depth. Hits come from the stacker's own
		// level crossings, so records auto-trigger once prehistory exists.
		cfg = sramcapture.Config{Source: sramcapture.ADC, PreWords: cfg.PreWords, PostWords: cfg.PostWords}
		if w := req.RecordWords; w != 0 {
			w = min(max(w, 2), sramcapture.Words)
			cfg.PreWords, cfg.PostWords = w/2, w-w/2
		}
		return e.runFPGAStack(ctx, cfg, req)
	})
	return core.OwnerResult[FPGAStackResult](value, err)
}

// TRLC-LINKS: REQ-SDS-141
func (e *Engine) runFPGAStack(ctx context.Context, cfg sramcapture.Config, req FPGAStackRequest) (res FPGAStackResult, err error) {
	// The session holds the owner loop for seconds, including two FPGA loads
	// with no bus reads. It is bounded by ctx, so beat for its duration or the
	// OTA agent judges the app hung and restarts it.
	defer e.beatWhile()()
	halt, cancel := context.WithTimeout(context.Background(), time.Second)
	haltErr := e.sram.Halt(halt)
	cancel()
	if haltErr != nil {
		return res, haltErr
	}
	start := time.Now()
	// The session ends on the general image; the loop reloads the packet image
	// if the serial trigger still needs it.
	e.protocolImage = ""
	defer e.probeSerialHardware()
	// Loads reset the panel scan; restore it after the general image returns.
	defer func() { _ = e.applyPanelScan() }()
	if err = e.loadFeature("stack"); err != nil {
		// A failed load may leave any image; restore the general one.
		if restore := e.loadFeature("general"); restore != nil {
			e.logf("FPGA stack: general image restore failed: %v", restore)
		}
		return res, fmt.Errorf("stacking image: %w", err)
	}
	defer func() {
		if restore := e.loadFeature("general"); restore != nil {
			e.logf("FPGA stack: general image restore failed: %v", restore)
			if err == nil {
				err = fmt.Errorf("general image restore: %w", restore)
			}
		}
	}()
	res.LoadS = time.Since(start).Seconds()
	_ = e.applyPanelScan()
	if _, err = sramcapture.New(e.b); err != nil {
		return res, err
	}
	tile, err := e.sram.StackBegin(req.Stack)
	if err != nil {
		return res, err
	}
	// Every tile must see the same frozen records and hit parity. Retain each
	// tile's moments on the ARM while another tile occupies the FPGA RAM.
	moments := make([]superres.FPGAStackMoments, 2*req.Stack.Bins)
	scratch := make([]superres.FPGAStackMoments, 2*tile)
	stackStart, beats := time.Now(), e.sram.Beats()
	var m sramcapture.Metadata
	for r := range req.Records {
		t0 := time.Now()
		if m, err = e.captureOne(ctx, cfg); err != nil {
			return res, fmt.Errorf("record %d: %w", r, err)
		}
		res.CaptureS += time.Since(t0).Seconds()
		var recordHits, recordCrossings, recordRejected uint32
		for first := 0; first < req.Stack.Bins; first += tile {
			n := min(tile, req.Stack.Bins-first)
			state := moments[2*first : 2*(first+n)]
			// Every scan starts from a zeroed tile and its moments are added on
			// the ARM: uploading the running state before each scan costs one bus
			// round trip a word, half the session (20 records take minutes with
			// the display frozen).
			if err = e.sram.StackResetTile(ctx); err != nil {
				return res, err
			}
			t0 = time.Now()
			hits, crossings, rejected, scanErr := e.sram.StackScan(ctx, first, n, res.Hits)
			if scanErr != nil {
				return res, fmt.Errorf("tile %d record %d: %w", first/tile, r, scanErr)
			}
			res.ScanS += time.Since(t0).Seconds()
			if first == 0 {
				recordHits, recordCrossings, recordRejected = hits, crossings, rejected
			} else if hits != recordHits || crossings != recordCrossings || rejected != recordRejected {
				return res, fmt.Errorf("stack tiles disagree on hits/crossings/rejections at record %d tile %d", r, first/tile)
			}
			t0 = time.Now()
			part := scratch[:len(state)]
			if err = e.sram.StackDownload(ctx, part); err != nil {
				return res, err
			}
			addTileMoments(state, part)
			res.DownloadS += time.Since(t0).Seconds()
		}
		if req.Progress != nil {
			req.Progress(r+1, req.Records)
		}
		res.Hits = recordHits
		res.Crossings += uint64(recordCrossings)
		res.Rejected += uint64(recordRejected)
		res.RawWords += uint64(m.Length)
	}
	res.BusReads = e.sram.Beats() - beats
	res.StackS = time.Since(stackStart).Seconds()
	stack, err := superres.FPGAStack(moments, req.Stack.Factor, int(req.Stack.Channel), int(res.Hits), req.Records, 1/m.SampleRateHz)
	if err != nil {
		return res, err
	}
	res.Result, res.Stack = stack.Result(false, 1), stack
	res.Records = req.Records
	res.SampleS, res.Factor = 1/m.SampleRateHz, req.Stack.Factor
	return res, nil
}

// addTileMoments is superres.AddFPGAMoments over a tile, by pointer: a whole
// downloaded tile is folded in per record with the owner loop held.
// TRLC-LINKS: REQ-SDS-141
func addTileMoments(state, part []superres.FPGAStackMoments) {
	part = part[:len(state)]
	for i := range state {
		a, b := &state[i], &part[i]
		var c uint64
		a.Sum[0], c = bits.Add64(a.Sum[0], b.Sum[0], 0)
		a.Sum[1] += b.Sum[1] + c
		a.SumSquares[0], c = bits.Add64(a.SumSquares[0], b.SumSquares[0], 0)
		a.SumSquares[1] += b.SumSquares[1] + c
		a.SumOdd[0], c = bits.Add64(a.SumOdd[0], b.SumOdd[0], 0)
		a.SumOdd[1] += b.SumOdd[1] + c
		a.Count += b.Count
		a.CountOdd += b.CountOdd
	}
}

// captureOne arms an auto-triggered raw capture and waits until it is frozen.
// TRLC-LINKS: REQ-SDS-141
func (e *Engine) captureOne(ctx context.Context, cfg sramcapture.Config) (sramcapture.Metadata, error) {
	arm, cancel := context.WithTimeout(ctx, 3*time.Second)
	err := e.sram.Arm(arm, cfg)
	cancel()
	if err != nil {
		return sramcapture.Metadata{}, err
	}
	wait, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for {
		m, err := e.sram.Status()
		if err != nil {
			return m, err
		}
		if m.DataFault {
			return m, errors.New("SRAM acquisition FIFO overflow")
		}
		if m.Frozen && m.Ready {
			return m, nil
		}
		if err := wait.Err(); err != nil {
			halt, cancel := context.WithTimeout(context.Background(), time.Second)
			haltErr := e.sram.Halt(halt)
			cancel()
			return m, errors.Join(err, haltErr)
		}
		e.clk.Sleep(time.Millisecond)
	}
}
