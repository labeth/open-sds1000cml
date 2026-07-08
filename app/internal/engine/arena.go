package engine

import "sync"

// Frame is one drained capture plus everything a consumer needs to render it.
// The three arena frames are reused in place: every producer path must set or
// clear ALL metadata every frame (spec 01 §2 — a stale flag renders wrong
// output from correct data).
type Frame struct {
	C1, C2 []uint8 // full-capacity backing arrays; valid prefix is [:Valid]

	Seq      uint64  // advances only on a real publish
	Valid    int     // drained sample count; the tail beyond it is stale
	WinCols  int     // samples spanning the 10-division screen
	EdgeX    float64 // software crossing position, -1 = flat rail
	Interp   bool    // native-fast: consumer should linearly interpolate
	IsEnv    bool    // envelope/roll band: render Env* instead of traces
	EnvCols  int     // envelope column count (800) when IsEnv, else 0
	Ptp      int     // peak-to-peak of the discrimination channel
	Trigd    bool    // HW comparator fired (0x39 bit1)
	TrigPos  int     // HW trigger-position latch (telemetry only)
	Coherent bool
	HaltOK   bool

	// Per-column (min,max) envelope bands, valid [:EnvCols] when IsEnv.
	// Every value is a real ADC min or max — nothing synthesized.
	EnvMin, EnvMax   []uint8 // C1
	EnvMin2, EnvMax2 []uint8 // C2

	// RollCodes marks samples popped from the roll FIFO (~Vdiv/25 half
	// scale) rather than the deep drain (Vdiv/50): WF? export must rescale.
	RollCodes bool

	// Config snapshot at drain time, so a mid-flight band change cannot tear
	// the served frame.
	TdivS      float64
	DisplayedS float64
	SampleS    float64 // per-sample capture interval in seconds
	Norm       bool

	// Stream/stitch mode continuity metadata: the client places consecutive
	// windows on one time axis and marks the blackout (GapNs) between them.
	StreamSeq uint64 // monotonic stream window counter (0 = not a stream frame)
	WindowNs  int64  // this window's captured duration (= Valid × SampleS)
	GapNs     int64  // measured blackout (drain+re-arm) before this window
}

// arena is the triple buffer between the engine owner and consumers
// (spec 01 §2): write = producer's private drain target, ready = most recent
// published, read = consumer's private slot. Double-buffering tears against
// the immediate-re-arm invariant; three slots are required.
type arena struct {
	mu    sync.Mutex
	write *Frame
	ready *Frame
	read  *Frame
	dirty bool
}

func newArena(capacity int) *arena {
	mk := func() *Frame {
		return &Frame{
			C1: make([]uint8, capacity), C2: make([]uint8, capacity),
			EnvMin: make([]uint8, envDisplayCols), EnvMax: make([]uint8, envDisplayCols),
			EnvMin2: make([]uint8, envDisplayCols), EnvMax2: make([]uint8, envDisplayCols),
		}
	}
	return &arena{write: mk(), ready: mk(), read: mk()}
}

// Write returns the producer's private slot. Owner-only.
func (a *arena) Write() *Frame { return a.write }

// Publish swaps the drained write slot into ready. The mutex guards only the
// pointer swap — never held across bus access. If the consumer hasn't taken
// the previous frame it is overwritten (drop-newest backpressure).
func (a *arena) Publish() {
	a.mu.Lock()
	a.write, a.ready = a.ready, a.write
	a.dirty = true
	a.mu.Unlock()
}

// Consume returns the newest published frame. fresh=false means nothing new
// was published since the last call — the caller re-presents the held frame
// (a quiet NORM display, not an error).
func (a *arena) Consume() (f *Frame, fresh bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.dirty {
		a.read, a.ready = a.ready, a.read
		a.dirty = false
		return a.read, true
	}
	return a.read, false
}
