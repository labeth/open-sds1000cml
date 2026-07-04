package web

import (
	"encoding/binary"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"

	"open-sds/app/internal/engine"
)

// getBin fetches /api/frame.bin and returns the decoded header + raw payload.
func getBin(t *testing.T, s *Server, url string) (frameReply, byte, []byte) {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	b := rec.Body.Bytes()
	if len(b) < 8 || b[0] != binMagic {
		t.Fatalf("bad bin reply: %d bytes, magic %#x", len(b), b[0])
	}
	h := binary.LittleEndian.Uint32(b[4:8])
	if 8+int(h) > len(b) {
		t.Fatalf("header length %d overruns %d-byte reply", h, len(b))
	}
	var rep frameReply
	if err := json.Unmarshal(b[8:8+h], &rep); err != nil {
		t.Fatalf("header json: %v", err)
	}
	return rep, b[1], b[8+int(h):]
}

// getJSON fetches /api/frame with the same params for the parity oracle.
func getJSON(t *testing.T, s *Server, url string) frameReply {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var rep frameReply
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("json reply: %v", err)
	}
	return rep
}

// reconstruct rebuilds an int16 array from a binary payload segment the way
// binframe.js does: fill -1 margins, widen the uint8 body.
func reconstruct(seg []byte, cols, head, tail int) []int16 {
	out := make([]int16, cols)
	for i := range out {
		out[i] = -1
	}
	for i, v := range seg {
		out[head+i] = int16(v)
	}
	_ = tail
	return out
}

// TestBinFrameParity: for every frame shape, the binary reply must
// reconstruct element-wise to the JSON reply's arrays, and the header must
// carry the same scalar fields.
func TestBinFrameParity(t *testing.T) {
	deepC := func(n int) ([]uint8, []uint8) {
		c1, c2 := make([]uint8, n), make([]uint8, n)
		for i := range c1 {
			c1[i] = uint8(i % 251)
			c2[i] = uint8((i * 7) % 249)
		}
		return c1, c2
	}
	shapes := []struct {
		name string
		f    *engine.Frame
		want byte // expected flag bits
	}{
		{"native-windowed", func() *engine.Frame {
			c1, c2 := deepC(2048)
			return &engine.Frame{C1: c1, C2: c2, Seq: 11, Valid: 2048, WinCols: 2048,
				EdgeX: 1024, TdivS: 500e-6, DisplayedS: 500e-6, SampleS: 800e-9}
		}(), 0},
		{"native-interp", func() *engine.Frame {
			c1, c2 := deepC(400)
			return &engine.Frame{C1: c1, C2: c2, Seq: 12, Valid: 400, WinCols: 320,
				EdgeX: 200, Interp: true, TdivS: 1e-6, DisplayedS: 1e-6, SampleS: 25e-9}
		}(), 0},
		{"deep-trig-margins", func() *engine.Frame {
			c1, c2 := deepC(6144)
			// Edge near the record start → head margin after re-centering.
			return &engine.Frame{C1: c1, C2: c2, Seq: 13, Valid: 6144, WinCols: 2048,
				EdgeX: 100, TdivS: 1e-3, DisplayedS: 1e-3, SampleS: 1.6e-6}
		}(), binDeep},
		{"deep-trig-tail", func() *engine.Frame {
			c1, c2 := deepC(6144)
			// Edge near the record end → tail margin.
			return &engine.Frame{C1: c1, C2: c2, Seq: 14, Valid: 6144, WinCols: 2048,
				EdgeX: 6100, TdivS: 1e-3, DisplayedS: 1e-3, SampleS: 1.6e-6}
		}(), binDeep},
		{"deep-freerun", func() *engine.Frame {
			c1, c2 := deepC(6144)
			return &engine.Frame{C1: c1, C2: c2, Seq: 15, Valid: 6144, WinCols: 2048,
				EdgeX: -1, TdivS: 1e-3, DisplayedS: 1e-3, SampleS: 1.6e-6}
		}(), binDeep},
		{"envelope", func() *engine.Frame {
			mk := func(base int) []uint8 {
				e := make([]uint8, 800)
				for i := range e {
					e[i] = uint8((base + i) % 255)
				}
				return e
			}
			c1, c2 := deepC(800)
			return &engine.Frame{C1: c1, C2: c2, Seq: 16, Valid: 800, WinCols: 800, EdgeX: -1,
				IsEnv: true, EnvCols: 800, EnvMin: mk(0), EnvMax: mk(50), EnvMin2: mk(100), EnvMax2: mk(150),
				TdivS: 0.01, DisplayedS: 0.01, SampleS: 1e-4}
		}(), binEnv},
	}
	for _, tc := range shapes {
		t.Run(tc.name, func(t *testing.T) {
			fs := &fakeScope{frame: tc.f, fresh: true}
			s := New(fs, nil, nil, nil)
			url := "/api/frame?since=0&cols=2048&full=1"
			want := getJSON(t, s, url)
			got, flags, pay := getBin(t, s, "/api/frame.bin?since=0&cols=2048&full=1")

			if flags&^binEmpty != tc.want {
				t.Fatalf("flags = %#x, want %#x", flags, tc.want)
			}
			// Header scalar parity (arrays compared separately).
			gh, wh := got, want
			gh.Head, gh.Tail = 0, 0
			gh.C1, gh.C2, gh.E1Min, gh.E1Max, gh.E2Min, gh.E2Max = nil, nil, nil, nil, nil, nil
			wh.C1, wh.C2, wh.E1Min, wh.E1Max, wh.E2Min, wh.E2Max = nil, nil, nil, nil, nil, nil
			if !reflect.DeepEqual(gh, wh) {
				t.Fatalf("header mismatch:\n got %+v\nwant %+v", gh, wh)
			}
			if tc.f.IsEnv {
				if len(pay) != 4*got.Cols {
					t.Fatalf("env payload %d bytes, want %d", len(pay), 4*got.Cols)
				}
				for i, wantArr := range [][]int16{want.E1Min, want.E1Max, want.E2Min, want.E2Max} {
					seg := pay[i*got.Cols : (i+1)*got.Cols]
					if !reflect.DeepEqual(reconstruct(seg, got.Cols, 0, 0), wantArr) {
						t.Fatalf("env segment %d mismatch", i)
					}
				}
				return
			}
			body := got.Cols - got.Head - got.Tail
			if len(pay) != 2*body {
				t.Fatalf("payload %d bytes, want %d (cols %d head %d tail %d)",
					len(pay), 2*body, got.Cols, got.Head, got.Tail)
			}
			c1 := reconstruct(pay[:body], got.Cols, got.Head, got.Tail)
			c2 := reconstruct(pay[body:], got.Cols, got.Head, got.Tail)
			if !reflect.DeepEqual(c1, want.C1) {
				t.Fatalf("c1 reconstruction mismatch (head %d tail %d)", got.Head, got.Tail)
			}
			if !reflect.DeepEqual(c2, want.C2) {
				t.Fatalf("c2 reconstruction mismatch (head %d tail %d)", got.Head, got.Tail)
			}
			// Deep margin shapes must actually exercise margins.
			if tc.name == "deep-trig-margins" && got.Head == 0 {
				t.Fatal("expected a head margin, got none")
			}
			if tc.name == "deep-trig-tail" && got.Tail == 0 {
				t.Fatal("expected a tail margin, got none")
			}
		})
	}
}

func TestBinFrameUnchanged(t *testing.T) {
	c := make([]uint8, 2048)
	f := &engine.Frame{C1: c, C2: c, Seq: 9, Valid: 2048, WinCols: 2048, EdgeX: 100, SampleS: 1e-6}
	fs := &fakeScope{frame: f, fresh: true}
	s := New(fs, nil, nil, nil)
	rep, flags, pay := getBin(t, s, "/api/frame.bin?since=9&cols=2048&full=1")
	if !rep.Unchanged || flags&binUnchanged == 0 || len(pay) != 0 {
		t.Fatalf("unchanged: rep=%+v flags=%#x pay=%d", rep, flags, len(pay))
	}
	// No frame published yet → unchanged with seq 0.
	fs2 := &fakeScope{}
	s2 := New(fs2, nil, nil, nil)
	rep2, _, _ := getBin(t, s2, "/api/frame.bin?since=0")
	if !rep2.Unchanged || rep2.Seq != 0 {
		t.Fatalf("nil frame: %+v", rep2)
	}
}

// TestBinFrameLongPollDegrade: without a frameWaiter the handler seq-polls;
// with since==seq and a short waitms it must return unchanged after the wait
// rather than hanging.
func TestBinFrameLongPollDegrade(t *testing.T) {
	c := make([]uint8, 256)
	f := &engine.Frame{C1: c, C2: c, Seq: 3, Valid: 256, WinCols: 256, EdgeX: 128, SampleS: 1e-6}
	fs := &fakeScope{frame: f, fresh: true}
	s := New(fs, nil, nil, nil)
	rep, _, _ := getBin(t, s, "/api/frame.bin?since=3&waitms=50")
	if !rep.Unchanged || rep.Seq != 3 {
		t.Fatalf("degraded long-poll: %+v", rep)
	}
}

// TestMeasCacheReuse: buildReply must compute measurements once per
// (seq, coupling, scale) and reuse the pointers on repeat calls.
func TestMeasCacheReuse(t *testing.T) {
	c1, c2 := make([]uint8, 512), make([]uint8, 512)
	for i := range c1 {
		if i%100 < 50 {
			c1[i], c2[i] = 60, 70
		} else {
			c1[i], c2[i] = 200, 210
		}
	}
	f := &engine.Frame{C1: c1, C2: c2, Seq: 21, Valid: 512, WinCols: 512, EdgeX: 256, SampleS: 1e-6}
	fs := &fakeScope{frame: f, fresh: true}
	s := New(fs, nil, nil, nil)

	r1 := s.buildReply(f, 512, false, 0, [2]float64{}, [2]float64{1. / 32, 1. / 32}, 0.5)
	r2 := s.buildReply(f, 512, false, 0, [2]float64{}, [2]float64{1. / 32, 1. / 32}, 0.5)
	if r1.M1 == nil || r1.M1 != r2.M1 || r1.M2 != r2.M2 {
		t.Fatalf("cache miss on identical inputs: %p vs %p", r1.M1, r2.M1)
	}
	// Any scale change must refresh.
	r3 := s.buildReply(f, 512, false, 0, [2]float64{}, [2]float64{1. / 16, 1. / 32}, 0.5)
	if r3.M1 == r1.M1 {
		t.Fatal("vpc change did not refresh the measurement cache")
	}
	// New seq must refresh.
	f.Seq = 22
	r4 := s.buildReply(f, 512, false, 0, [2]float64{}, [2]float64{1. / 16, 1. / 32}, 0.5)
	if r4.M1 == r3.M1 {
		t.Fatal("seq change did not refresh the measurement cache")
	}
}
