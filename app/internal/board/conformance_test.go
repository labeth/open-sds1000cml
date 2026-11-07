package board_test

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/hal/conformance"
	scope "github.com/labeth/open-sds1000cml/app"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
	"github.com/labeth/open-sds1000cml/app/internal/board"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/bus"
	cmlpanel "github.com/labeth/open-sds1000cml/app/internal/panel"
	"github.com/labeth/open-sds1000cml/app/internal/settings"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// fabric is the SRAM capture fabric with a mid-scale signal on its inputs:
// a capture freezes a few polls after its arm once the trigger fires, which
// a level code inside the signal's swing does, an AUTO (not Normal)
// capture does by itself, and a forced one does at once. Its records hold
// the signal's codes.
type fabric struct {
	acq.Memory
	mu     sync.Mutex
	armed  sramcapture.Config
	polls  int
	forced bool
	seq    uint32
}

// The signal's swing in codes: a square between them.
const lowCode, highCode = 78, 178

func (f *fabric) Arm(_ context.Context, c sramcapture.Config) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.armed, f.polls, f.forced = c, 0, false
	f.seq++
	return nil
}

func (f *fabric) fires() bool {
	l := f.armed.TriggerLevel
	return l > lowCode && l < highCode
}

func (f *fabric) meta() sramcapture.Metadata {
	words := f.armed.PreWords + f.armed.PostWords
	return sramcapture.Metadata{
		Frozen: true, Ready: true, Triggered: f.fires() && !f.forced, Length: words, TriggerIndex: f.armed.PreWords,
		SamplesPerWord: 2, RecordID: f.seq, EventEpoch: 1, HasRecordIdentity: true, Revision: 10,
	}
}

func (f *fabric) Poll() (sramcapture.Metadata, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	if f.polls >= 3 && (f.forced || f.fires() || !f.armed.Normal) {
		return f.meta(), nil
	}
	return sramcapture.Metadata{Running: true, Triggered: f.polls > 1 && f.fires()}, nil
}

func (f *fabric) Status() (sramcapture.Metadata, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.meta(), nil
}

func (f *fabric) Force(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forced = true
	return nil
}

func (f *fabric) Halt(context.Context) error { return nil }
func (f *fabric) SetSpanHook(func(int, int)) {}

// square writes n words of the signal: each word one sample of C1 and C2.
func square(dst io.Writer, n uint32, bytesPerWord int) error {
	buf := make([]byte, int(n)*bytesPerWord)
	for i := range buf {
		buf[i] = lowCode
		if (i/bytesPerWord/64)%2 == 1 {
			buf[i] = highCode
		}
	}
	_, err := dst.Write(buf)
	return err
}

func (f *fabric) Recall(_ context.Context, _, count uint32, dst io.Writer) (int64, error) {
	return int64(4 * count), square(dst, count, 4)
}

func (f *fabric) RecallForward(_ context.Context, _, count uint32, dst io.Writer) (int64, error) {
	return int64(4 * count), square(dst, count, 4)
}

func (f *fabric) RecallReadsOf(_ context.Context, _ sramcapture.Metadata, reads ...sramcapture.RecordRead) error {
	for _, r := range reads {
		if err := square(r.Dst, r.Count, 4); err != nil {
			return err
		}
	}
	return nil
}

func (f *fabric) SupportsUART() (bool, error)                 { return false, nil }
func (f *fabric) SupportsSampleUART() (bool, error)           { return false, nil }
func (f *fabric) SupportsI2C() (bool, error)                  { return false, nil }
func (f *fabric) SupportsSampleI2C() (bool, error)            { return false, nil }
func (f *fabric) SupportsSPI() (bool, error)                  { return false, nil }
func (f *fabric) SupportsSampleSPI() (bool, error)            { return false, nil }
func (f *fabric) SupportsSENT() (bool, error)                 { return false, nil }
func (f *fabric) SupportsUSBLS() (bool, error)                { return false, nil }
func (f *fabric) SupportsMIL1553() (bool, error)              { return false, nil }
func (f *fabric) SupportsManchester() (bool, error)           { return false, nil }
func (f *fabric) SupportsSequence() (bool, error)             { return false, nil }
func (f *fabric) SequenceOnly() (bool, error)                 { return false, nil }
func (f *fabric) SupportsEnvelope() (bool, error)             { return true, nil }
func (f *fabric) SupportsQualifier() (bool, error)            { return false, nil }
func (f *fabric) SupportsSymbol() (bool, error)               { return false, nil }
func (f *fabric) SupportsPanel() (bool, error)                { return false, nil }
func (f *fabric) SupportsDecodedCapture() (bool, error)       { return false, nil }
func (f *fabric) PacketProtocols() (uint16, error)            { return 0, nil }
func (f *fabric) RawOnly() (bool, error)                      { return false, nil }
func (f *fabric) BoxDecimation() (bool, error)                { return false, nil }
func (f *fabric) PickDecimation() (bool, error)               { return false, nil }
func (f *fabric) PickOnAsking() (bool, error)                 { return false, nil }
func (f *fabric) SymbolMatches() (uint16, error)              { return 0, nil }
func (f *fabric) EnvelopeDiagnostics() (int, string)          { return 0, "" }
func (f *fabric) StreamMarksSupported() bool                  { return false }
func (f *fabric) StreamRingSupported() bool                   { return false }
func (f *fabric) Locked(fn func())                            { fn() }
func (f *fabric) Snapshot(context.Context) ([10]uint8, error) { return [10]uint8{}, nil }

// spi is the front end's SPI with nothing on it.
type spi struct{}

func (spi) WriteRelay(uint32) error      { return nil }
func (spi) WriteGain(uint8, uint8) error { return nil }

// plane is the CS3 plane: lamps and DACs, written nowhere.
type plane struct{ bus.Bus }

func (plane) Write(uint8, uint16, uint16) error { return nil }

// matrix is the key matrix with no key down.
type matrix struct{}

func (matrix) ReadMatrix() ([5]uint16, bool) { return [5]uint16{}, true }
func (matrix) SetLEDs(uint16)                {}

// images is the image switcher of a unit that has only the general image
// on its stick.
type images struct{}

func (images) LoadStack() error   { return nil }
func (images) LoadGeneral() error { return nil }
func (images) LoadPacket() error  { return nil }
func (images) LoadLine() error    { return nil }
func (images) LoadFrame() error   { return nil }
func (images) LoadStream() error  { return nil }
func (images) LoadQualify() error { return nil }
func (images) LoadFast() error    { return nil }
func (images) HasStack() bool     { return false }
func (images) HasPacket() bool    { return false }
func (images) HasLine() bool      { return false }
func (images) HasFrame() bool     { return false }
func (images) HasStream() bool    { return false }
func (images) HasQualify() bool   { return false }
func (images) HasFast() bool      { return false }

// cmlBoard is the SDS1000CML+'s board as its main composes it, over the
// fabric and front-end fakes: the scope's profile, the analog front end,
// the trigger levels, the panel input and the settings store.
func cmlBoard(t *testing.T) *acq.Board {
	t.Helper()
	p, err := scope.Profile(scope.Identity())
	if err != nil {
		t.Fatal(err)
	}
	b := acq.New(acq.Config{Memory: &fabric{}, Panel: plane{}})
	fe := analog.New(spi{}, func(time.Duration) {}, nil)
	for ch := range 2 {
		if err := fe.SetVdiv(ch, 8); err != nil {
			t.Fatal(err)
		}
	}
	b.SetProfile(p)
	b.SetFrontEnd(fe)
	b.SetLevelGrid(board.New(nil, fe).TrigLevelGrid)
	b.SetDevices(nil, cmlpanel.NewInput(matrix{}, -1, t.Logf), settings.Storage{Dir: t.TempDir(), Cal: hal.CalSource{Source: "defaults", Valid: true}})
	b.SetImages(images{})
	b.ProbeImage(t.Logf)
	return b
}

// The SDS1000CML+'s board keeps the HAL contract over its fabric and front
// end fakes.
// TRLC-LINKS: REQ-SDS-001
func TestHALConformance(t *testing.T) {
	b := cmlBoard(t)
	readout := "the board reads only windows of an explicit span into frames the caller sized; the core's main window (Span 0, an empty frame) is refused"
	conformance.Run(t, b, conformance.Options{Skip: map[string]string{
		"devices":         "the LCD is driven outside the board: Display() is nil",
		"planner":         "a plan's depth counts full-rate samples past the profile's max_depth (1 Mi at 10 µs/div)",
		"need-refusal":    "the planner does not check Request.Need",
		"stage-commit":    "the analog front end applies a setting at Set, not at the next Arm",
		"freerun":         readout,
		"readout":         readout,
		"readout-windows": readout,
		"readout-refusal": readout,
		"stopped-record":  readout,
	}})
}
