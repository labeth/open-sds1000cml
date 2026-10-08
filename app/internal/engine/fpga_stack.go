// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"context"
	"fmt"
	"time"

	"open-sds/app/internal/sramcapture"
	"open-sds/app/internal/superres"
)

// ImageSwitcher reconfigures the acquisition FPGA between the general image
// and the stacking image (ADR-STACKING-IMAGE-SPLIT). Both share the SRAM
// capture ABI; only the stacking image has the stack port.
// TRLC-LINKS: REQ-SDS-141, REQ-SDS-005
type ImageSwitcher interface {
	LoadStack() error
	LoadGeneral() error
	// LoadPacket and LoadLine load the protocol images of
	// ADR-IMAGE-REGROUP-DECIMATION.
	LoadPacket() error
	LoadLine() error
	HasStack() bool
	HasPacket() bool
	HasLine() bool
}

// FPGAStackRequest stacks Records fresh captures at the current timebase, raw
// samples only. RecordWords, when nonzero, bounds each record's depth (the
// FPGA rescans the whole record once per 32-bin tile).
// TRLC-LINKS: REQ-SDS-141
type FPGAStackRequest struct {
	Stack       sramcapture.StackConfig `json:"stack"`
	Records     int                     `json:"records"`
	RecordWords uint32                  `json:"record_words,omitempty"`
}

// FPGAStackResult reports the stack and the bus traffic it needed. RawWords
// is what recalling the same records would have moved; BusReads is what the
// moments actually cost.
// TRLC-LINKS: REQ-SDS-141
type FPGAStackResult struct {
	Result    superres.Result `json:"result"`
	Hits      uint32          `json:"hits"`
	Crossings uint64          `json:"crossings"`
	// Rejected counts template-qualifier rejections over the whole session.
	Rejected uint64  `json:"rejected"`
	Records  int     `json:"records"`
	SampleS  float64 `json:"sample_s"`
	Factor   int     `json:"factor"`
	BusReads uint64  `json:"bus_reads"`
	RawWords uint64  `json:"raw_words"`
	LoadS    float64 `json:"load_s"`
	StackS   float64 `json:"stack_s"`
	// Time split of StackS: arm-to-frozen captures, FPGA scans, downloads.
	CaptureS  float64 `json:"capture_s"`
	ScanS     float64 `json:"scan_s"`
	DownloadS float64 `json:"download_s"`
	// Stack is the ARM-side accumulation for local review rendering.
	Stack *superres.Stack `json:"-"`
}

// sramJob runs on the owner goroutine at the top of the SRAM loop, where no
// arm or recall is in flight. Any retained record is forgotten afterwards.
type sramJob struct {
	ctx  context.Context
	run  func(context.Context) (any, error)
	done chan decodedReply
}

// SetImageSwitcher enables FPGA stacking. Call before Run.
// TRLC-LINKS: REQ-SDS-141
func (e *Engine) SetImageSwitcher(s ImageSwitcher) { e.images = s }

// TRLC-LINKS: REQ-SDS-141
func (e *Engine) sramCall(ctx context.Context, run func(context.Context) (any, error)) (any, error) {
	if e.sram == nil {
		return nil, fmt.Errorf("FPGA stacking requires the SRAM acquisition image")
	}
	req := sramJob{ctx: ctx, run: run, done: make(chan decodedReply, 1)}
	select {
	case e.sramJobs <- req:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.done:
		return nil, ErrExecStopped
	}
	select {
	case r := <-req.done:
		return r.value, r.err
	case <-ctx.Done():
		// The job still runs to completion and restores the general image.
		return nil, ctx.Err()
	case <-e.done:
		return nil, ErrExecStopped
	}
}

// serviceSRAMJobs reports whether a job ran (and so replaced the fabric).
// TRLC-LINKS: REQ-SDS-141
func (e *Engine) serviceSRAMJobs() bool {
	select {
	case req := <-e.sramJobs:
		if err := req.ctx.Err(); err != nil {
			req.done <- decodedReply{err: err}
			return false
		}
		value, err := req.run(req.ctx)
		req.done <- decodedReply{value, err}
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
		return FPGAStackResult{}, fmt.Errorf("FPGA stacking image not available in this build")
	}
	if req.Records < 1 || req.Records > 100000 {
		return FPGAStackResult{}, fmt.Errorf("records %d out of range", req.Records)
	}
	value, err := e.sramCall(ctx, func(ctx context.Context) (any, error) {
		if e.decodedSession != nil {
			return nil, fmt.Errorf("decoded capture is active")
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
	if err != nil {
		return FPGAStackResult{}, err
	}
	return value.(FPGAStackResult), nil
}

// TRLC-LINKS: REQ-SDS-141
func (e *Engine) runFPGAStack(ctx context.Context, cfg sramcapture.Config, req FPGAStackRequest) (res FPGAStackResult, err error) {
	// The session holds the owner loop for seconds, including two FPGA loads
	// with no bus reads. It is bounded by ctx, so beat for its duration or the
	// OTA agent judges the app hung and restarts it.
	beating := make(chan struct{})
	defer close(beating)
	go func() {
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-beating:
				return
			case <-tick.C:
				e.beatN.Add(1)
			}
		}
	}()
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
	if err = e.images.LoadStack(); err != nil {
		// A failed load may leave any image; restore the general one.
		if restore := e.images.LoadGeneral(); restore != nil {
			e.logf("FPGA stack: general image restore failed: %v", restore)
		}
		return res, fmt.Errorf("stacking image: %w", err)
	}
	defer func() {
		if restore := e.images.LoadGeneral(); restore != nil {
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
	// Tile-major: each tile stays in the FPGA for Records fresh records and is
	// downloaded once, so the ARM receives only the final moments.
	moments := make([]superres.FPGAStackMoments, 2*req.Stack.Bins)
	stackStart, beats := time.Now(), e.sram.Beats()
	var m sramcapture.Metadata
	for first := 0; first < req.Stack.Bins; first += tile {
		n := min(tile, req.Stack.Bins-first)
		if err = e.sram.StackResetTile(ctx); err != nil {
			return res, err
		}
		hits := uint32(0)
		for r := 0; r < req.Records; r++ {
			t0 := time.Now()
			if m, err = e.captureOne(ctx, cfg); err != nil {
				return res, fmt.Errorf("tile %d record %d: %w", first/tile, r, err)
			}
			t1 := time.Now()
			var crossings, rejected uint32
			if hits, crossings, rejected, err = e.sram.StackScan(ctx, first, n, hits); err != nil {
				return res, fmt.Errorf("tile %d record %d: %w", first/tile, r, err)
			}
			res.CaptureS += t1.Sub(t0).Seconds()
			res.ScanS += time.Since(t1).Seconds()
			res.Crossings += uint64(crossings)
			res.Rejected += uint64(rejected)
			res.RawWords += uint64(m.Length)
		}
		t0 := time.Now()
		if err = e.sram.StackDownload(ctx, moments[2*first:2*(first+n)]); err != nil {
			return res, err
		}
		res.DownloadS += time.Since(t0).Seconds()
		if first == 0 {
			res.Hits = hits
		}
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
			return m, fmt.Errorf("SRAM acquisition FIFO overflow")
		}
		if m.Frozen && m.Ready {
			return m, nil
		}
		if err := wait.Err(); err != nil {
			halt, cancel := context.WithTimeout(context.Background(), time.Second)
			e.sram.Halt(halt)
			cancel()
			return m, err
		}
		e.clk.Sleep(time.Millisecond)
	}
}
