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
func workerFixture(t testing.TB) (*Dev, *workerFake) {
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

// A whole read of a record goes to the worker as one request carrying its
// windows after the header, and reaches the worker's capture: the fake has no
// SRAM, so its status read fails there and the error comes back. A request
// whose parameters are not whole windows is refused; without a worker, or
// beyond a packet, the read is the caller's to make.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-033
func TestWorkerRecallRequest(t *testing.T) {
	d, _ := workerFixture(t)
	dst := make([]byte, 64)
	owned, err := d.RecallIsolated(7, 0x12345, []uint32{10, 4, 8, 500, 0, 8}, dst)
	if !owned || err == nil || !strings.Contains(err.Error(), "unexpected fake read 1") {
		t.Fatalf("owned %v, error %v: want the worker's capture to have read the status", owned, err)
	}
	if owned, _ := d.RecallIsolated(7, 1, []uint32{0, 0, 1}, make([]byte, workerPayload+4)); owned {
		t.Fatal("a read beyond a worker packet was sent")
	}
	if owned, _ := d.RecallIsolated(7, 1, make([]uint32, 3*(sramcapture.RecordReadsMax+1)), dst); owned {
		t.Fatal("more windows than a request carries were sent")
	}
	if owned, _ := (&Dev{fd: -1}).RecallIsolated(7, 1, []uint32{0, 0, 1}, dst); owned {
		t.Fatal("a read was sent without a worker")
	}
	w := &acquisitionWorker{bus: &workerFake{}}
	var request [workerHeader + 10]byte
	binary.LittleEndian.PutUint16(request[8:10], workerRecall)
	binary.LittleEndian.PutUint32(request[16:20], 16)
	w.handle(request[:])
	if w.response[10] != 1 || !strings.Contains(string(w.response[workerHeader:]), "invalid recall request") {
		t.Fatal("a recall request without whole windows was served")
	}
}

// ReadMany through a worker reads the registers in order in one round trip,
// reports a failed read, refuses the worker's decoded port, and without a
// worker reads them one by one.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-129
func TestWorkerReadMany(t *testing.T) {
	d, f := workerFixture(t)
	got := make([]uint16, 4)
	if err := d.ReadMany(1, []uint16{13, 61, 73, 14}, got); err != nil {
		t.Fatal(err)
	}
	if want := []uint16{10, 7, 0x5201, sramcapture.QualifiedMapID}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if err := d.ReadMany(3, []uint16{1, 2}, got); err != nil || got[0] != 1^0x55 || got[1] != 2^0x55 {
		t.Fatalf("CS3 reads %v, %v", got[:2], err)
	}
	if err := d.ReadMany(1, []uint16{13, 1}, got); err == nil || !strings.Contains(err.Error(), "unexpected fake read 1") {
		t.Fatalf("a failed read: %v", err)
	}
	if err := d.ReadMany(1, []uint16{13, 200}, got); err == nil {
		t.Fatal("a selector past the schema was read")
	}
	f.active.Store(0)
	w := &acquisitionWorker{bus: f, active: true}
	var request [workerHeader + 4]byte
	binary.LittleEndian.PutUint16(request[8:10], workerReadMany)
	request[10] = 1
	binary.LittleEndian.PutUint32(request[16:20], 4)
	binary.LittleEndian.PutUint16(request[workerHeader:], 13)
	binary.LittleEndian.PutUint16(request[workerHeader+2:], 59)
	w.handle(request[:])
	if w.response[10] != 1 {
		t.Fatal("the worker's decoded event port was read for the parent")
	}
	if err := (&Dev{fd: -1}).ReadMany(1, []uint16{13, 14}, got); err == nil {
		t.Fatal("without a worker or a device, a read succeeded")
	}
	if err := d.ReadMany(1, []uint16{13, 14}, got[:1]); err == nil {
		t.Fatal("a read into a short buffer succeeded")
	}
}

// RawWriteMany through a worker makes every write in order, in batches past
// a packet's room, and refuses the worker's FIFO control.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-129
func TestWorkerRawWriteMany(t *testing.T) {
	d, f := workerFixture(t)
	regs := make([][2]uint16, 30)
	for i := range regs {
		regs[i] = [2]uint16{uint16(80 + i%10), uint16(i)}
	}
	before := f.writes.Load()
	if err := d.RawWriteMany(regs); err != nil {
		t.Fatal(err)
	}
	if got := f.writes.Load() - before; got != 30 {
		t.Fatalf("%d writes, want 30", got)
	}
	if err := d.RawWriteMany([][2]uint16{{57, 1}, {80, 1}}); err != nil {
		t.Fatal(err)
	}
	if err := d.RawWriteMany([][2]uint16{{81, 1}, {60, 1}}); err == nil {
		t.Fatal("the worker's FIFO control was written for the parent")
	}
	if err := d.RawWriteMany([][2]uint16{{81, 1}, {200, 1}}); err == nil {
		t.Fatal("a selector past the schema was written")
	}
}
