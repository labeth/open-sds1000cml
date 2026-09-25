// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type decodedCaptureBus struct {
	*fakeBus
	epoch, record, recordEpoch uint32
	enabled                    bool
}

// TRLC-LINKS: REQ-SDS-039, REQ-SDS-041, REQ-SDS-083
type timelineBus struct {
	*decodedCaptureBus
	regs map[uint16]uint16
}

// TRLC-LINKS: REQ-SDS-039, REQ-SDS-041, REQ-SDS-083
func (b *timelineBus) Read(p uint8, s uint16) (uint16, error) {
	if s >= 83 && s <= 92 {
		return b.regs[s], nil
	}
	return b.decodedCaptureBus.Read(p, s)
}

// TRLC-LINKS: REQ-SDS-039, REQ-SDS-041, REQ-SDS-083
func TestFrozenSampleTimeline(t *testing.T) {
	for _, tc := range []struct {
		name             string
		last             uint64
		id, epoch, valid uint16
		wantErr          bool
	}{
		{"across low-word wrap", 1<<32 | 40, 7, 9, 1, false},
		{"before low-word wrap", 1<<32 - 2, 7, 9, 1, false},
		{"wrong record", 1<<32 | 40, 8, 9, 1, true},
		{"wrong epoch", 1<<32 | 40, 7, 10, 1, true},
		{"discontinuous", 1<<32 | 40, 7, 9, 0, false},
		{"underflow", 10, 7, 9, 1, true},
		{"odd word ordinal", 1<<32 | 41, 7, 9, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := frozenBus()
			f.revision = 10
			b := &timelineBus{decodedCaptureBus: &decodedCaptureBus{fakeBus: f, record: 7, recordEpoch: 9}, regs: map[uint16]uint16{83: 0x5401, 84: tc.valid, 89: tc.id, 91: tc.epoch}}
			for i := uint16(0); i < 4; i++ {
				b.regs[85+i] = uint16(tc.last >> (16 * i))
			}
			c, err := New(b)
			if err != nil {
				t.Fatal(err)
			}
			m, err := c.Status()
			if (err != nil) != tc.wantErr {
				t.Fatalf("metadata %+v error %v", m, err)
			}
			if !tc.wantErr && tc.valid != 0 && (!m.HasSampleTimeline || m.SampleFirst != tc.last-2*uint64(Words-1) || m.SampleLast != tc.last+1) {
				t.Fatalf("wrong sample range %+v", m)
			}
			if tc.valid == 0 && m.HasSampleTimeline {
				t.Fatal("discontinuous capture advertised a linear timeline")
			}
			f.flags &^= 16
			m, err = c.Status()
			if err != nil || m.HasSampleTimeline {
				t.Fatal("live capture advertised a frozen timeline", m, err)
			}
		})
	}
}

// TRLC-LINKS: REQ-SDS-013
func (b *decodedCaptureBus) Read(p uint8, s uint16) (uint16, error) {
	switch s {
	case 15:
		return 500, nil
	case 19:
		return 2, nil
	case 29:
		return 8, nil
	case 63:
		return 0x4501, nil
	case 64:
		return 0x5301, nil
	case 73:
		return 0x5201, nil
	case 57:
		if b.enabled {
			return 1, nil
		}
		return 0, nil
	case 61:
		return uint16(b.epoch), nil
	case 62:
		return uint16(b.epoch >> 16), nil
	case 69:
		return uint16(b.record), nil
	case 70:
		return uint16(b.record >> 16), nil
	case 71:
		return uint16(b.recordEpoch), nil
	case 72:
		return uint16(b.recordEpoch >> 16), nil
	}
	return b.fakeBus.Read(p, s)
}

// TRLC-LINKS: REQ-SDS-013
func (b *decodedCaptureBus) RawWrite(s, v uint16) error {
	if s == 57 {
		if v == 1 && !b.enabled {
			b.epoch++
		}
		b.enabled = v == 1
	}
	if s >= 64 && s <= 68 && b.enabled {
		return fmt.Errorf("configured decoder while streaming")
	}
	if s == 1 && v == 1 {
		b.record++
		b.recordEpoch = 0
		if b.enabled {
			b.recordEpoch = b.epoch
		}
	}
	return b.fakeBus.RawWrite(s, v)
}

// TRLC-LINKS: REQ-SDS-013
func TestDecodedCaptureEpochAndRetention(t *testing.T) {
	f := frozenBus()
	f.revision = 10
	b := &decodedCaptureBus{fakeBus: f}
	c := client(t, f)
	c.bus = b
	cfg := Config{Normal: true, PostWords: 16, DecodedEvents: true, SPI: SPITriggerConfig{Enabled: true, GapTicks: 938, MSB: true}}
	for epoch := uint32(1); epoch <= 2; epoch++ {
		if err := c.Arm(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
		if !b.enabled || b.epoch != epoch {
			t.Fatal("stream not enabled before capture")
		}
		if err := c.Force(context.Background()); err != nil {
			t.Fatal(err)
		}
		m, err := c.Status()
		if err != nil {
			t.Fatal(err)
		}
		if !m.HasRecordIdentity || m.EventEpoch != epoch || m.RecordID != epoch || !m.Frozen {
			t.Fatalf("identity %+v", m)
		}
		if _, err := c.EnableDecodedEvents(false); err != nil {
			t.Fatal(err)
		}
		after, err := c.Status()
		if err != nil {
			t.Fatal(err)
		}
		if after.RecordID != m.RecordID || after.EventEpoch != m.EventEpoch {
			t.Fatal("stream disable changed retained identity")
		}
	}
}

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-033
func TestDecodedSessionRetainsAndRejectsReplacement(t *testing.T) {
	f := frozenBus()
	f.revision = 10
	b := &decodedCaptureBus{fakeBus: f}
	c := client(t, f)
	c.bus = b
	cfg := Config{Normal: true, PostWords: 16, SPI: SPITriggerConfig{Enabled: true, GapTicks: 938, MSB: true}}
	session, err := c.StartDecodedCapture(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Force(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, err := session.WaitRetained(context.Background())
	if err != nil || m.RecordID != session.Identity.Record {
		t.Fatalf("wait: %+v %v", m, err)
	}
	var output bytes.Buffer
	n, err := session.Recall(context.Background(), 0, 16, &output)
	if err != nil || n != 64 || output.Len() != 64 {
		t.Fatalf("recall %d %v", n, err)
	}
	// A new arm invalidates the old session, even if a newer record is frozen.
	if _, err = c.StartDecodedCapture(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	_, waitErr := session.WaitRetained(waitCtx)
	cancel()
	if waitErr == nil || errors.Is(waitErr, context.DeadlineExceeded) {
		t.Fatalf("waited for replacement capture: %v", waitErr)
	}
	if err = c.Force(context.Background()); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	before := f.writes
	if _, err = session.Recall(context.Background(), 0, 16, &output); err == nil {
		t.Fatal("recalled replacement")
	}
	if output.Len() != 0 || f.writes != before {
		t.Fatal("stale session transferred raw data")
	}
	if _, err = session.WaitRetained(context.Background()); err == nil {
		t.Fatal("accepted replacement")
	}
}
