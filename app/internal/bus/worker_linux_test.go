// ENGMODEL-OWNER-UNIT: FU-APP-BUS
package bus

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"open-sds/app/internal/sramcapture"
)

type workerFake struct {
	active, remaining, writes atomic.Int32
	sequence                  uint32
}

// TRLC-LINKS: REQ-SDS-013
func (f *workerFake) Read(plane uint8, sel uint16) (uint16, error) {
	if plane == 3 {
		return sel ^ 0x55, nil
	}
	switch sel {
	case 0:
		return sramcapture.FabricID, nil
	case 13:
		return 10, nil
	case 14:
		return sramcapture.QualifiedMapID, nil
	case 57:
		return uint16(f.active.Load()), nil
	case 58, 62:
		return 0, nil
	case 61:
		return 7, nil
	case 63:
		return 0x4501, nil
	case 73:
		return 0x5201, nil
	case 74:
		return 0x4503, nil
	case 75:
		return uint16(min(f.remaining.Load(), 512)), nil
	}
	return 0, fmt.Errorf("unexpected fake read %d", sel)
}

// TRLC-LINKS: REQ-SDS-013
func (f *workerFake) RawWrite(sel, value uint16) error {
	f.writes.Add(1)
	if sel == 57 {
		f.active.Store(int32(value))
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-013
func (f *workerFake) Write(_ uint8, sel, value uint16) error { return f.RawWrite(sel, value) }

// TRLC-LINKS: REQ-SDS-013
func (f *workerFake) FastDrain() bool { return true }

// TRLC-LINKS: REQ-SDS-013
func (f *workerFake) PopBytesChecked(sel uint16, dst []byte) error {
	if sel == 25 {
		for i := range dst {
			dst[i] = byte(i + 17)
		}
		return nil
	}
	if sel != 59 || len(dst)%32 != 0 {
		return fmt.Errorf("bad fake pop")
	}
	for i := 0; i < len(dst); i += 32 {
		e := sramcapture.DecodedEvent{Epoch: 7, Sequence: f.sequence, Sample: 0xfffffff0 + uint64(f.sequence)*4, Kind: sramcapture.EventData, Protocol: 9, Value: f.sequence & 255, Valid: true}
		if err := e.MarshalTo(dst[i : i+32]); err != nil {
			return err
		}
		f.sequence++
	}
	f.remaining.Add(-int32(len(dst) / 32))
	return nil
}

// TRLC-LINKS: REQ-SDS-001, REQ-SDS-013
func workerFixture(t *testing.T) (*Dev, *workerFake) {
	t.Helper()
	f := &workerFake{}
	capture, err := sramcapture.New(f)
	if err != nil {
		t.Fatal(err)
	}
	w := &acquisitionWorker{bus: f, capture: capture, ring: make([]byte, workerQueueEvents*32)}
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_SEQPACKET, 0)
	if err != nil {
		t.Fatal(err)
	}
	p := os.NewFile(uintptr(fds[0]), "worker-test")
	conn, err := net.FileConn(p)
	p.Close()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- w.serve(fds[1]); syscall.Close(fds[1]) }()
	d := &Dev{fd: -1, remote: &workerClient{conn: conn, fast: true}}
	t.Cleanup(func() {
		_ = d.RawWrite(57, 0)
		conn.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("worker did not stop after peer closed")
		}
	})
	return d, f
}

// TRLC-LINKS: REQ-SDS-001, REQ-SDS-013, REQ-SDS-033
func TestWorkerDrainsWhileClientIsIdleAndServesRetainedBytes(t *testing.T) {
	d, f := workerFixture(t)
	f.remaining.Store(2000)
	if err := d.RawWrite(57, 1); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Second)
	for f.remaining.Load() != 0 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if f.remaining.Load() != 0 {
		t.Fatal("ingestion depended on client requests")
	}
	raw := make([]byte, workerPayload)
	if err := d.PopBytesChecked(25, raw); err != nil {
		t.Fatal(err)
	}
	for i, v := range raw {
		if v != byte(i+17) {
			t.Fatal("retained IPC data changed", i)
		}
	}
	capture, err := sramcapture.New(d)
	if err != nil {
		t.Fatal(err)
	}
	storage := make([]sramcapture.DecodedEvent, 512)
	sequence := uint32(0)
	for sequence < 2000 {
		events, err := capture.ReadDecodedEventsInto(7, storage)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) == 0 {
			t.Fatal("lost queued events")
		}
		for _, event := range events {
			if event.Sequence != sequence || event.Sample != 0xfffffff0+uint64(sequence)*4 {
				t.Fatal("event ordering/timestamp", event)
			}
			sequence++
		}
	}
	if _, err := capture.ReadDecodedEventsInto(8, storage); err == nil {
		t.Fatal("stale epoch accepted")
	}
	if _, err := d.Read(1, 59); err == nil {
		t.Fatal("competing event consumer accepted")
	}
	if err := d.RawWrite(76, 1); err == nil {
		t.Fatal("competing FIFO reservation accepted")
	}
	if v, err := d.Read(3, 0x14); err != nil || v != 0x41 {
		t.Fatal("panel/control read failed", v, err)
	}
}

// TRLC-LINKS: REQ-SDS-013
func TestWorkerRejectsOversizedAndForbiddenRequests(t *testing.T) {
	f := &workerFake{}
	w := &acquisitionWorker{bus: f}
	var request [workerHeader]byte
	binary.LittleEndian.PutUint16(request[8:10], workerWrite)
	request[10] = 3
	binary.LittleEndian.PutUint16(request[12:14], CS3ConfigPort)
	w.handle(request[:])
	if w.response[10] != 1 || f.writes.Load() != 0 {
		t.Fatal("configuration write escaped guard")
	}
	binary.LittleEndian.PutUint16(request[8:10], workerPop)
	binary.LittleEndian.PutUint32(request[16:20], workerPayload+2)
	w.handle(request[:])
	if w.response[10] != 1 {
		t.Fatal("oversized request accepted")
	}
}

// TRLC-LINKS: REQ-SDS-013
func TestWorkerRingWrapAndOverflowAreExplicit(t *testing.T) {
	f := &workerFake{}
	f.active.Store(1)
	f.remaining.Store(3)
	c, err := sramcapture.New(f)
	if err != nil {
		t.Fatal(err)
	}
	w := &acquisitionWorker{bus: f, capture: c, active: true, epoch: 7, head: workerQueueEvents - 1, ring: make([]byte, workerQueueEvents*32)}
	w.pump()
	if w.fault != nil || w.count != 3 {
		t.Fatal(w.fault, w.count)
	}
	var req [workerHeader]byte
	binary.LittleEndian.PutUint16(req[8:10], workerEvents)
	binary.LittleEndian.PutUint32(req[16:20], 96)
	binary.LittleEndian.PutUint32(req[20:24], 7)
	if n := w.handle(req[:]); n != workerHeader+96 || w.response[10] != 0 {
		t.Fatal("wrapped batch", n)
	}
	for i := 0; i < 3; i++ {
		e, err := sramcapture.ParseDecodedEvent(w.response[workerHeader+i*32 : workerHeader+(i+1)*32])
		if err != nil || e.Sequence != uint32(i) {
			t.Fatal(e, err)
		}
	}
	w.count = workerQueueEvents
	f.remaining.Store(1)
	w.pump()
	if w.fault == nil || !strings.Contains(w.fault.Error(), "overflow") {
		t.Fatal("queue overflow hidden")
	}
	w.handle(req[:])
	if w.response[10] != 1 {
		t.Fatal("failed transcript looked complete")
	}
}

// TRLC-LINKS: REQ-SDS-001, REQ-SDS-013
func TestWorkerDisconnectNeverFallsBackToParentFD(t *testing.T) {
	d, _ := workerFixture(t)
	d.remote.conn.Close()
	for i := 0; i < 2; i++ {
		_, err := d.Read(1, 0)
		if err == nil || !strings.Contains(err.Error(), "worker transport failed") {
			t.Fatal("did not fail closed", err)
		}
	}
}
