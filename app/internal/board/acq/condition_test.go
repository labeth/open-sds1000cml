// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"testing"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// The screen window is the live window and, asked for, its envelope, as
// the record's words place them.
// TRLC-LINKS: REQ-SDS-010
func TestScreenWindow(t *testing.T) {
	b, _, _ := newTestBoard()
	m := sramcapture.Metadata{Length: 1 << 19, TriggerIndex: 1 << 18, SamplesPerWord: 1, FractionBits: 8, SampleRateHz: 500e6 / 16}
	b.SetRecord(m)
	b.armedPlan.WinSamples = 50000
	rd, ok := b.Readout().(hal.ScreenReader)
	if !ok {
		t.Fatal("the readout places no screen window")
	}
	for _, req := range []hal.ScreenRequest{{PosFrac: .5}, {PosFrac: .2, PreRoom: 3000}, {PosFrac: .5, Envelope: true}, {PosFrac: .8, PreRoom: 1000, Envelope: true}} {
		w, err := rd.ScreenWindow(req)
		if err != nil {
			t.Fatal(err)
		}
		off, words := LiveSRAMWindowPre(m, 50000, req.PosFrac, req.PreRoom)
		var bucket uint32
		if req.Envelope {
			bucket, off, words = EnvelopeWindow(off, words, sramcapture.EnvelopeBucketsQ8, m.Length, uint32(req.PreRoom))
		}
		gotOff, gotWords, gotBucket, err := WindowWords(m, w)
		if err != nil || gotOff != off || gotWords != words || gotBucket != bucket {
			t.Errorf("%+v: %d+%d /%d (%v), want %d+%d /%d", req, gotOff, gotWords, gotBucket, err, off, words, bucket)
		}
	}
}

// Read conditions what it read: a full-rate main window gets the
// calibration's precision words in the frame's storage, an envelope main
// window loses the converters' offsets and is in time order, a view
// window keeps no precision words, and a caller's empty storage stays
// none where nothing wrote it.
// TRLC-LINKS: REQ-SDS-016, REQ-SDS-040
func TestReadConditions(t *testing.T) {
	m := &fakeMem{freezeAt: 1, caps: map[string]bool{}}
	b := New(Config{Memory: rampMem{m}, Panel: &cs3Log{}})
	b.ProbeImage(func(string, ...any) {})
	cal := &InterleaveCalibration{Vdiv: [2]float64{1, 1}, Offset: [2][5]float64{{1, -1, 0, 0, 0}, {2, -2, 0, 0, 0}}}
	b.SetInterleaveCalibration(cal)
	b.SetRecordScale([2]float64{1, 1})
	md := sramcapture.Metadata{Length: 8192, TriggerIndex: 4096, SamplesPerWord: 2, Decimation: 1, SampleRateHz: 500e6, Revision: 10}
	b.SetRecord(md)
	var raw [2][]uint8
	b.SetRawObserver(func(c1, c2 []uint8) { raw = [2][]uint8{append([]uint8(nil), c1...), append([]uint8(nil), c2...)} })
	read := func(w hal.Window, f *sample.Frame) {
		t.Helper()
		if err := b.Readout().Read(context.Background(), hal.ReadRequest{Windows: []hal.Window{w}}, []*sample.Frame{f}, nil); err != nil {
			t.Fatal(err)
		}
	}
	f := RecordFrame(4096)
	store := make([]uint16, 0, 4096)
	f.Traces[0].Precision, f.Traces[1].Precision = store, store
	f.SampleS = 2e-9
	read(Window(md, sample.Main, 3072, 2048, 0), f)
	if len(raw[0]) != 4096 {
		t.Fatal("the raw codes were not observed")
	}
	if f.Cond.Filter == "" {
		t.Fatal("a full-rate main window not conditioned")
	}
	if p := f.Precision(0); p != nil && (len(p) != 4096 || &p[:1][0] != &store[:1][0]) {
		t.Fatalf("precision words %d, not in the caller's storage", len(p))
	}
	env := RecordFrame(2 * 512)
	env.SampleS = 2e-9
	read(Window(md, sample.Main, 0, 8192, 16), env)
	if env.Content != sample.PeakPairs || !env.Flags.Has(sample.Ordered) {
		t.Fatalf("envelope %v flags %v", env.Content, env.Flags)
	}
	view := RecordFrame(2048)
	view.Traces[0].Precision = make([]uint16, 2048)
	read(Window(md, sample.RoleView(0), 3584, 1024, 0), view)
	if view.Precision(0) != nil {
		t.Fatal("a view window kept precision words")
	}
}
