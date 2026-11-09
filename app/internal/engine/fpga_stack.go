// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"context"
	"errors"
	"fmt"
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
	var session superres.StackResult
	session, err = e.hw().StackRecords(ctx, cfg, req)
	session.LoadS = res.LoadS
	return session, err
}
