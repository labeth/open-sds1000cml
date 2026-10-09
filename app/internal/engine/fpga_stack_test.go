// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"open-sds/app/internal/bus"
	"open-sds/app/internal/sramcapture"
	"open-sds/app/internal/superres"
)

// stackEngineBus emulates the SRAM capture ABI in both images and the stack
// port only while the stacking image is loaded. Arm freezes immediately.
type stackEngineBus struct {
	bus.Bus
	image          string
	ack, frozen    bool
	grant          bool
	opError        bool
	failScan       bool
	recordDrift    bool
	mismatch       bool
	regs           map[uint16]uint16
	mailbox        [32]uint16
	mailIndex      int
	tile           [32][2]superres.FPGAStackMoments
	hits           uint32
	arms, stackOps int
}

// TRLC-LINKS: REQ-SDS-141
func (b *stackEngineBus) Read(_ uint8, s uint16) (uint16, error) {
	stack := b.image == "stack"
	switch {
	case s == 0:
		return sramcapture.FabricID, nil
	case s == 1:
		v := uint16(4 | 64)
		if b.frozen {
			v |= 16
		}
		if b.ack {
			v |= 512
		}
		return v, nil
	case s == 2:
		return 1000, nil
	case s == 13:
		return 8, nil
	case s == 14:
		return sramcapture.QualifiedMapID, nil
	case s == 15:
		return 500, nil
	case s == 19:
		return 2, nil
	case s == 26:
		return 4096, nil
	case stack && s == 109:
		if b.grant {
			return 3, nil
		}
		return 0, nil
	case stack && s == 111:
		v := b.mailbox[b.mailIndex%32]
		b.mailIndex++
		return v, nil
	case stack && s == 112:
		v := uint16(16)
		if b.opError {
			v |= 4
		}
		return v, nil
	case stack && s == 113:
		return sramcapture.StackCapabilityID, nil
	case stack && s == 114:
		return 32, nil
	case stack && s == 115:
		return uint16(b.hits), nil
	case stack && s == 116:
		return uint16(b.hits >> 16), nil
	case stack && s == 117:
		return 7, nil
	case stack && s == 121:
		return 1, nil
	}
	return 0, nil
}

// TRLC-LINKS: REQ-SDS-141
func (b *stackEngineBus) RawWrite(s, v uint16) error {
	stack := b.image == "stack"
	switch {
	case s == 1:
		if v == 1 {
			b.arms++
			b.frozen = true
		}
		b.ack = !b.ack
	case stack && s == 109:
		b.grant = v&1 != 0
	case stack && s == 110:
		b.mailIndex = int(v)
	case stack && s == 111:
		b.mailbox[b.mailIndex%32] = v
		b.mailIndex++
	case stack && s == 112:
		b.stackOps++
		b.opError = false
		bin, ch := int(b.regs[127]>>1), int(b.regs[127]&1)
		switch v {
		case 5:
			b.tile = [32][2]superres.FPGAStackMoments{}
		case 1:
			if b.failScan || !b.grant || !b.frozen {
				b.opError = true
				return nil
			}
			for i := 0; i < int(b.regs[119]); i++ {
				for c := 0; c < 2; c++ {
					m := &b.tile[i][c]
					m.Count += 2
					value := i + 1
					if b.recordDrift {
						value += int(b.regs[120]) + 10*b.arms + 100*c
					}
					m.Sum[0] += 2 * uint64(value) << 24
				}
			}
			b.hits = (uint32(b.regs[123]) | uint32(b.regs[124])<<16) + 2
			if b.mismatch && b.regs[120] != 0 {
				b.hits++
			}
		case 3:
			words, err := superres.EncodeFPGAMoments(b.tile[bin][ch])
			if err != nil {
				return err
			}
			copy(b.mailbox[:], words[:])
		case 4:
			m, err := superres.DecodeFPGAMoments(b.mailbox[:superres.FPGAStackWords])
			if err != nil {
				return err
			}
			b.tile[bin][ch] = m
		}
	default:
		if b.regs == nil {
			b.regs = map[uint16]uint16{}
		}
		b.regs[s] = v
	}
	return nil
}

type fakeImages struct {
	b          *stackEngineBus
	loads      []string
	failStack  bool
	failReturn bool
}

// TRLC-LINKS: REQ-SDS-141
func (f *fakeImages) LoadStack() error {
	f.loads = append(f.loads, "stack")
	if f.failStack {
		f.b.image = "broken"
		return errors.New("load failed")
	}
	f.b.image, f.b.frozen = "stack", false
	return nil
}

// TRLC-LINKS: REQ-SDS-013
func (f *fakeImages) LoadPacket() error {
	f.loads = append(f.loads, "packet")
	return errors.New("no packet image in this fake")
}

// TRLC-LINKS: REQ-SDS-013
func (f *fakeImages) LoadLine() error { return errors.New("no line image in this fake") }

// TRLC-LINKS: REQ-SDS-013
func (f *fakeImages) HasLine() bool { return false }

// TRLC-LINKS: REQ-SDS-141
func (f *fakeImages) HasStack() bool { return true }

// TRLC-LINKS: REQ-SDS-013
func (f *fakeImages) HasPacket() bool { return false }

// TRLC-LINKS: REQ-SDS-005
func (f *fakeImages) LoadGeneral() error {
	f.loads = append(f.loads, "general")
	if f.failReturn {
		return errors.New("restore failed")
	}
	f.b.image, f.b.frozen = "general", false
	return nil
}

// TRLC-LINKS: REQ-SDS-141
func newStackTestEngine(t *testing.T) (*Engine, *stackEngineBus, *fakeImages) {
	b := &stackEngineBus{image: "general"}
	capture, err := sramcapture.New(b)
	if err != nil {
		t.Fatal(err)
	}
	images := &fakeImages{b: b}
	e := &Engine{b: b, sram: capture, sramJobs: make(chan sramJob, 1), done: make(chan struct{}), images: images,
		logf: t.Logf, clk: Clock{Now: time.Now, Sleep: func(time.Duration) {}}}
	return e, b, images
}

// runStack serves exactly one owner-side job, as the SRAM loop top does.
// TRLC-LINKS: REQ-SDS-141
func runStack(t *testing.T, e *Engine, req FPGAStackRequest) (FPGAStackResult, error) {
	type reply struct {
		res FPGAStackResult
		err error
	}
	out := make(chan reply, 1)
	go func() {
		res, err := e.FPGAStack(context.Background(), req)
		out <- reply{res, err}
	}()
	for !e.serviceSRAMJobs() {
		time.Sleep(time.Millisecond)
	}
	r := <-out
	return r.res, r.err
}

// TRLC-LINKS: REQ-SDS-141
func TestFPGAStackSessionSwitchesImagesAndReducesTraffic(t *testing.T) {
	e, b, images := newStackTestEngine(t)
	req := FPGAStackRequest{Records: 3, Stack: sramcapture.StackConfig{Level: 128, Hysteresis: 8, Bins: 64, Factor: 4, ChannelMask: 3}}
	res, err := runStack(t, e, req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(images.loads, ",") != "stack,general" || b.image != "general" || b.grant {
		t.Fatalf("image sequence %v, final %s, grant %v", images.loads, b.image, b.grant)
	}
	// Every tile stacks the same three records.
	if b.arms != 3 || res.Records != 3 || res.Hits != 6 || res.Crossings != 21 || res.RawWords != 3000 {
		t.Fatalf("result %+v arms %d", res, b.arms)
	}
	// 64 bins: two tiles, three records each, every bin sees 2 hits per record.
	if len(res.Result.Mean) != 64 || res.Result.Mean[0] != 1 || res.Result.Mean[40] != 9 || res.Result.Hits != 6 {
		t.Fatalf("mean %v", res.Result.Mean[:8])
	}
	if res.BusReads == 0 {
		t.Fatal("no bus traffic accounted")
	}
}

// TRLC-LINKS: REQ-SDS-141
func TestFPGAStackSessionAlwaysRestoresGeneral(t *testing.T) {
	e, b, images := newStackTestEngine(t)
	req := FPGAStackRequest{Records: 2, Stack: sramcapture.StackConfig{Bins: 8, Factor: 1, ChannelMask: 3}}
	b.failScan = true
	if _, err := runStack(t, e, req); err == nil || b.image != "general" || b.grant {
		t.Fatalf("scan failure: %v image %s grant %v", err, b.image, b.grant)
	}
	b.failScan = false
	images.failStack, images.loads = true, nil
	if _, err := runStack(t, e, req); err == nil || strings.Join(images.loads, ",") != "stack,general" || b.image != "general" {
		t.Fatalf("load failure: %v loads %v image %s", err, images.loads, b.image)
	}
	images.failStack, images.failReturn = false, true
	if _, err := runStack(t, e, req); err == nil || !strings.Contains(err.Error(), "restore") {
		t.Fatalf("restore failure not reported: %v", err)
	}
	for _, bad := range []FPGAStackRequest{{Records: 0, Stack: req.Stack}, {Records: 1, Stack: sramcapture.StackConfig{Bins: 8}}} {
		images.failReturn, images.loads = false, nil
		if bad.Records == 0 {
			if _, err := e.FPGAStack(context.Background(), bad); err == nil || len(images.loads) != 0 {
				t.Fatalf("accepted %+v", bad)
			}
			continue
		}
		if _, err := runStack(t, e, bad); err == nil || b.image != "general" {
			t.Fatalf("accepted %+v", bad)
		}
	}
	e.images = nil
	if _, err := e.FPGAStack(context.Background(), req); err == nil {
		t.Fatal("stacking without an embedded image")
	}
}

// TRLC-LINKS: REQ-SDS-141
func TestFPGAStackSameRecordsAcrossTiles(t *testing.T) {
	e, b, _ := newStackTestEngine(t)
	b.recordDrift = true
	res, err := runStack(t, e, FPGAStackRequest{Records: 3, Stack: sramcapture.StackConfig{Level: 128, Hysteresis: 8, Bins: 72, Factor: 4, ChannelMask: 3}})
	if err != nil {
		t.Fatal(err)
	}
	for ch, mean := range [][]float32{res.Result.Mean, res.Result.Mean2} {
		for i, v := range mean {
			if want := float32(i + 1 + 20 + 100*ch); v != want {
				t.Fatalf("channel %d bin %d: got %g want %g (acquisition-dependent tile seam)", ch, i, v, want)
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-141
func TestFPGAStackRejectsTileDisagreement(t *testing.T) {
	e, b, _ := newStackTestEngine(t)
	b.mismatch = true
	_, err := runStack(t, e, FPGAStackRequest{Records: 2, Stack: sramcapture.StackConfig{Level: 128, Hysteresis: 8, Bins: 64, Factor: 4, ChannelMask: 3}})
	if err == nil || !strings.Contains(err.Error(), "disagree") {
		t.Fatalf("got %v", err)
	}
	if b.image != "general" || b.grant {
		t.Fatal("did not restore general image and release SRAM")
	}
}
