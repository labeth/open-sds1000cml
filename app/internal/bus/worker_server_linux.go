// ENGMODEL-OWNER-UNIT: FU-APP-BUS

package bus

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"open-sds/app/internal/decodedlines"
	"open-sds/app/internal/sramcapture"
	"syscall"
	"time"
	"unsafe"
)

const workerQueueEvents = 131072

type workerHardware interface {
	Read(uint8, uint16) (uint16, error)
	RawWrite(uint16, uint16) error
	Write(uint8, uint16, uint16) error
	PopBytesChecked(uint16, []byte) error
	FastDrain() bool
}

type acquisitionWorker struct {
	bus         workerHardware
	capture     *sramcapture.Capture
	deadline    *DecodedDeadline
	active      bool
	epoch       uint32
	fault       error
	queueLost   uint64 // events the full queue dropped (errWorkerQueueOverflow)
	head, count int
	ring        []byte
	events      [512]sramcapture.DecodedEvent
	response    [workerHeader + workerPayload]byte
	// lines, when set, receives the drained events instead of the ring: the
	// decoded-stream transcript built here (ADR-STREAM-LINES-IN-WORKER).
	lines      *decodedlines.Transcript
	linesAt    time.Time                // when the transcript last took events
	linesStore *decodedlines.Transcript // reused across streams
	// Stream-bank pump (ADR-STREAM-IMAGE): drained words wait in sring.
	sOn                bool
	sExpected          uint64 // next ordinal to drain
	sHead, sCount      int    // bytes
	sFirst             uint64 // ordinal of the word at sHead
	sFault             error
	sring              []byte
	sLastPump          time.Time     // diagnostics for a lost stream:
	sMaxGap, sMaxDrain time.Duration // longest pump-to-pump gap and bank drain
	sShared            bool          // sring is the ring shared with the app
	sRate              float64       // input words a second, as the pump has drained them
	sEvery             time.Duration // the pump's look interval at that rate (0: streamPumpEvery)
	sBank              int           // words in a drained bank
	// sBacklog: the drum held a backlog at the last look; sSpin is when
	// the worker began looking without pause for it (zero: not spinning).
	sBacklog bool
	sSpin    time.Time
	logf     func(string, ...any)
}

// workerStreamBytes bounds the stream ring: two million words, two seconds
// of the 2^9 stream a software decode reads gap-free (its reader falls
// half a second behind now and then, while it decodes and the display
// draws) and a minute of a 10 ms/div roll.
const workerStreamBytes = 8 << 20

// TRLC-LINKS: REQ-SDS-035
type streamRingWriter struct{ w *acquisitionWorker }

// TRLC-LINKS: REQ-SDS-035
func (r streamRingWriter) Write(b []byte) (int, error) {
	w := r.w
	if w.sCount+len(b) > len(w.sring) {
		return 0, errors.New("isolated stream ring overflow; words lost")
	}
	for done := 0; done < len(b); {
		at := (w.sHead + w.sCount) % len(w.sring)
		k := copy(w.sring[at:min(len(w.sring), at+len(b)-done)], b[done:])
		done += k
		w.sCount += k
	}
	return len(b), nil
}

// streamPumpEvery is how often the stream pump looks for published banks
// when nothing else wakes the worker. Each look costs register reads and two
// clock reads (no vDSO on this kernel): at the 500 us FIFO service rate the
// looks alone took a tenth of the core.
const streamPumpEvery = 2 * time.Millisecond

// streamPumpFastest bounds the look interval from below at the fastest
// streams: a bank of the 2^8 stream fills in about a millisecond, and the
// host holds only two.
const streamPumpFastest = 400 * time.Microsecond

// pumpWait is the pump's current look interval.
// TRLC-LINKS: REQ-SDS-035
func (w *acquisitionWorker) pumpWait() time.Duration {
	if w.sEvery == 0 {
		return streamPumpEvery
	}
	return w.sEvery
}

// pumpEvery is the look interval: nine tenths of a bank's fill time at the
// measured input rate, between streamPumpFastest and streamPumpEvery, so a
// slow stream is looked at rarely and a fast one before its two banks fill.
// TRLC-LINKS: REQ-SDS-035
func pumpEvery(rate float64, bankWords int) time.Duration {
	if rate <= 0 || bankWords <= 0 {
		return streamPumpEvery
	}
	d := time.Duration(0.9 * float64(bankWords) / rate * float64(time.Second))
	return min(max(d, streamPumpFastest), streamPumpEvery)
}

// streamPump drains every published stream bank, in ordinal order.
// TRLC-LINKS: REQ-SDS-035
func (w *acquisitionWorker) streamPump() {
	if !w.sOn || w.sFault != nil {
		return
	}
	now := time.Now()
	if !w.sLastPump.IsZero() && now.Sub(w.sLastPump) < w.pumpWait() {
		// Every app request wakes the loop: the banks are not looked at
		// more often than this for it (the drum holds about a second).
		return
	}
	since := now.Sub(w.sLastPump)
	if !w.sLastPump.IsZero() {
		w.sMaxGap = max(w.sMaxGap, since)
	}
	w.sLastPump = now
	from := w.sExpected
	banks := 0
	defer func() {
		// The input rate, from the words this look drained over the time
		// since the last (a running average: one look sees 0, 1 or 2 banks).
		if since > 0 && since < time.Second {
			r := float64(w.sExpected-from) / since.Seconds()
			w.sRate += (r - w.sRate) / 8
			w.sEvery = pumpEvery(w.sRate, w.sBank)
		}
		if banks >= 2 {
			// Both banks were full: the drum holds a backlog and refills the
			// banks once a ring lap, so a look a fill time away takes two
			// banks a lap and falls behind a stream near the drum's rate.
			// The next look comes as soon as a bank can be ready again.
			w.sEvery = streamPumpFastest
		}
	}()
	// The banks' DMA sleeps through its transfers: the app decodes the
	// stream meanwhile (the worker runs at real-time priority). Not while
	// the drum holds a backlog: this kernel does not preempt a system call,
	// and a sleep wakes up to milliseconds late behind the app's.
	if d, ok := w.bus.(*Dev); ok && !w.spinning() && d.SetStreamYield(true) {
		defer d.SetStreamYield(false)
	}
	defer func() { w.sBacklog = w.capture.StreamBacklog() }()
	// No deadline: a bank's drain never waits (the DMA's own poll bounds
	// it), and a timer a pass cost a tenth of the worker at the fastest
	// stream.
	ctx := context.Background()
	for range 8 {
		t0 := time.Now()
		block, err := w.capture.DrainStream(ctx, w.sExpected, streamRingWriter{w})
		// A pump started ahead of its stream waits for it to be enabled; once
		// words came, a disabled stream is a loss like any other.
		if errors.Is(err, sramcapture.ErrNoStreamBlock) || errors.Is(err, sramcapture.ErrStreamDisabled) && w.sExpected == 0 {
			return
		}
		if err != nil {
			st, _ := w.capture.StreamStatus()
			w.sFault = fmt.Errorf("%w (after %d words; status %+v; longest pump gap %v, drain %v)", err, w.sExpected, st,
				w.sMaxGap.Round(time.Microsecond), w.sMaxDrain.Round(time.Microsecond))
			return
		}
		w.sMaxDrain = max(w.sMaxDrain, time.Since(t0))
		w.sExpected = block.FirstWord + uint64(block.Words)
		w.sBank = max(w.sBank, int(block.Words))
		banks++
	}
}

// streamSpinFor bounds how long the pump looks without pause while the
// drum holds a backlog: a gap of 30 ms at 2^8 is caught up in about a
// tenth of a second; a stream the drum cannot keep up with falls back to
// paced looks, so the app is not starved until the drum fills.
const streamSpinFor = 500 * time.Millisecond

// spinning reports whether the pump looks for banks without pause: the
// drum holds a backlog and refills the banks once a ring lap (2.1 ms), so
// a look that comes late halves the rate the backlog drains at, and a
// look after a sleep came late.
// TRLC-LINKS: REQ-SDS-035
func (w *acquisitionWorker) spinning() bool {
	if !w.sBacklog {
		w.sSpin = time.Time{}
		return false
	}
	if w.sSpin.IsZero() {
		w.sSpin = time.Now()
	}
	return time.Since(w.sSpin) < streamSpinFor
}

// spinPause is the wait between looks while spinning: none, unless the
// shared ring is over half full. The worker runs at real-time priority and
// the app reads the ring at normal priority on the same core: a worker that
// never sleeps starves the reader it fills the ring for, and the ring
// overflowed (after an 80 ms pump gap, half a second of catching up). Then
// it waits half as long as the pump took, a third of the core for the app.
// TRLC-LINKS: REQ-SDS-035
func (w *acquisitionWorker) spinPause(busy time.Duration) time.Duration {
	if !w.sShared || w.sCount <= len(w.sring)/2 {
		return 0
	}
	return max(streamPumpFastest, busy/2)
}

// streamPumpOn starts or stops the stream pump and its FIFO service.
// TRLC-LINKS: REQ-SDS-035
func (w *acquisitionWorker) streamPumpOn(on bool) error {
	w.sOn, w.sExpected, w.sHead, w.sCount, w.sFirst, w.sFault = on, 0, 0, 0, 0, nil
	w.sLastPump, w.sMaxGap, w.sMaxDrain = time.Time{}, 0, 0
	w.sRate, w.sEvery, w.sBank = 0, 0, 0
	w.sBacklog, w.sSpin = false, time.Time{}
	if on && w.sring == nil {
		w.pinStreamRing()
	}
	if on && w.deadline == nil {
		if d, ok := w.bus.(*Dev); ok {
			var err error
			if w.deadline, err = d.StartDecodedDeadline(); err != nil {
				return err
			}
			w.deadline.RaiseProcess()
		}
	}
	if !on && !w.active && w.deadline != nil {
		if err := w.deadline.Close(); err != nil {
			return err
		}
		w.deadline = nil
	}
	return nil
}

// workerLines bounds the worker's transcript: about a million 8-bit units.
const workerLineCapacity = 65536

// RunAcquisitionWorker is an alternate entry point in the same release binary.
// Only the worker touches GPMC after the parent's startup handshake. Socket EOF
// ends ownership, including when the GUI exits without a graceful shutdown.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-002, REQ-SDS-013
func RunAcquisitionWorker(gpmcFD, socketFD int, logf func(string, ...any)) error {
	d, err := New(gpmcFD)
	if err != nil {
		return err
	}
	if !d.EnableEDMA(8192, logf) {
		return errors.New("worker requires coherent EDMA")
	}
	c, err := sramcapture.New(d)
	if err != nil {
		return err
	}
	w := &acquisitionWorker{
		bus: d, capture: c, logf: logf, ring: make([]byte, workerQueueEvents*sramcapture.DecodedEventBytes),
		linesStore: decodedlines.New(0, workerLineCapacity),
	}
	// The stream ring is pinned before any stream: pinning took about 0.19 s,
	// and a stream started meanwhile filled the drum's ring at 2.6M words/s.
	w.pinStreamRing()
	return w.serve(socketFD)
}

// pinStreamRing maps the ring the app shares for in-place DMA, or makes a
// private one.
// TRLC-LINKS: REQ-SDS-035
func (w *acquisitionWorker) pinStreamRing() {
	if d, ok := w.bus.(*Dev); ok && workerHasSharedRing() {
		if ring, err := d.PinSharedRing(sharedRingFD, workerStreamBytes); err == nil {
			w.sring, w.sShared = ring, true
		} else {
			w.logf("acquisition worker: shared stream ring: %v", err)
		}
	}
	if w.sring == nil {
		w.sring = make([]byte, workerStreamBytes)
	}
}

// errWorkerQueueOverflow is the event queue's overflow: an explicit end of a
// transcript of events, a marked loss once the worker builds the lines.
var errWorkerQueueOverflow = errors.New("isolated decoded queue overflow; transcript incomplete")

// pump runs independently of GUI requests. The bounded ring can absorb client
// scheduling gaps, but exhaustion is explicit and never masquerades as lossless.
// lineIdleFlush is how long the bus may go quiet before the worker ends the
// transcript's half-full line: a burst's last units reach the reader then,
// not when a later burst fills the line.
const lineIdleFlush = 30 * time.Millisecond

// TRLC-LINKS: REQ-SDS-013
func (w *acquisitionWorker) pump() {
	if !w.active {
		return
	}
	if w.fault != nil {
		_, _ = w.capture.SupportsDecodedCapture()
		return
	}
	events, err := w.capture.ReadDecodedEventsInto(w.epoch, w.events[:])
	if err != nil {
		w.fault = err
		return
	}
	if w.lines != nil {
		now := time.Now()
		if len(events) > 0 {
			w.lines.Add(events)
			w.linesAt = now
		} else if w.lines.Pending() && now.Sub(w.linesAt) >= lineIdleFlush {
			// the bus went quiet with a line half full (a byte stream fills
			// sixteen slowly): end it, so a reader sees the burst now
			w.lines.Flush()
		}
		return
	}
	for _, event := range events {
		if w.count == workerQueueEvents {
			w.fault = errWorkerQueueOverflow
			w.queueLost += uint64(len(events))
			return
		}
		index := (w.head + w.count) % workerQueueEvents
		if err := event.MarshalTo(w.ring[index*32 : (index+1)*32]); err != nil {
			w.fault = err
			return
		}
		w.count++
	}
}

// TRLC-LINKS: REQ-SDS-001, REQ-SDS-013
func (w *acquisitionWorker) stream(enabled bool) error {
	w.lines = nil
	w.active = false
	w.head = 0
	w.count = 0
	w.fault = nil
	if w.deadline != nil && !w.sOn {
		if err := w.deadline.Close(); err != nil {
			return err
		}
		w.deadline = nil
	}
	if !enabled {
		return nil
	}
	lo, err := w.bus.Read(1, 61)
	if err != nil {
		return err
	}
	hi, err := w.bus.Read(1, 62)
	if err != nil {
		return err
	}
	w.epoch = uint32(lo) | uint32(hi)<<16
	if d, ok := w.bus.(*Dev); ok {
		w.deadline, err = d.StartDecodedDeadline()
		if err != nil {
			return err
		}
	}
	w.active = true
	return nil
}

// handle executes one bounded hardware operation. The response buffer is held
// until its socket write succeeds; a blocked GUI never blocks pump.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-013
func (w *acquisitionWorker) handle(request []byte) int { //nolint:gocyclo,funlen // the worker's request dispatch: one case per operation of the socket protocol
	copy(w.response[:workerHeader], request)
	w.response[10] = 0
	op := binary.LittleEndian.Uint16(request[8:10])
	plane := request[10]
	sel := binary.LittleEndian.Uint16(request[12:14])
	value := binary.LittleEndian.Uint16(request[14:16])
	count := int(binary.LittleEndian.Uint32(request[16:20]))
	epoch := binary.LittleEndian.Uint32(request[20:24])
	data := w.response[workerHeader:]
	n := 0
	var err error
	if count > workerPayload { //nolint:nestif // one request's handling, entered only when its size is valid
		err = errors.New("oversized worker request")
	} else {
		switch op {
		case workerHello:
			if count != 1 {
				err = errors.New("invalid ready request")
				break
			}
			if w.bus.FastDrain() {
				data[0] = 1
			} else {
				data[0] = 0
			}
			n = 1
		case workerRead:
			if count != 2 || !validPlane(plane) || sel > 127 {
				err = errors.New("invalid register read")
				break
			}
			if w.active && plane == 1 && sel == 59 {
				err = errors.New("decoded event port belongs to worker")
				break
			}
			var v uint16
			v, err = w.bus.Read(plane, sel)
			binary.LittleEndian.PutUint16(data, v)
			n = 2
		case workerReadMany:
			params := request[min(len(request), workerHeader):]
			if len(params) < 2 || len(params)%2 != 0 || count != len(params) || !validPlane(plane) {
				err = errors.New("invalid register reads")
				break
			}
			for i := 0; i < len(params) && err == nil; i += 2 {
				s := binary.LittleEndian.Uint16(params[i:])
				if s > 127 || w.active && plane == 1 && s == 59 {
					err = fmt.Errorf("invalid register read %d", s)
					break
				}
				var v uint16
				v, err = w.bus.Read(plane, s)
				binary.LittleEndian.PutUint16(data[i:], v)
			}
			n = len(params)
		case workerWriteMany:
			params := request[min(len(request), workerHeader):]
			if count != 0 || len(params) < 4 || len(params)%4 != 0 || plane != PlaneCS1 {
				err = errors.New("invalid register writes")
				break
			}
			for i := 0; i < len(params) && err == nil; i += 4 {
				s, v := binary.LittleEndian.Uint16(params[i:]), binary.LittleEndian.Uint16(params[i+2:])
				if s > 127 || w.active && (s == 60 || s == 76) {
					err = fmt.Errorf("invalid register write %d", s)
					break
				}
				if err = w.bus.RawWrite(s, v); err == nil && s == 57 {
					err = w.stream(v&1 != 0)
				}
			}
		case workerWrite:
			if count != 0 || !validPlane(plane) || sel > 127 || (plane == 3 && !Writable(plane, sel)) {
				err = errors.New("invalid register write")
				break
			}
			if w.active && plane == 1 && (sel == 60 || sel == 76) {
				err = errors.New("decoded FIFO control belongs to worker")
				break
			}
			if plane == 1 {
				err = w.bus.RawWrite(sel, value)
			} else {
				err = w.bus.Write(plane, sel, value)
			}
			if err == nil && plane == 1 && sel == 57 {
				err = w.stream(value&1 != 0)
			}
		case workerPop:
			if count == 0 || count%2 != 0 || sel > 127 || (w.active && sel == 59) {
				err = errors.New("invalid or worker-owned pop")
				break
			}
			err = w.bus.PopBytesChecked(sel, data[:count])
			n = count
		case workerEvents:
			if count == 0 || count%32 != 0 || !w.active || epoch != w.epoch {
				err = errors.New("inactive or stale decoded epoch")
				break
			}
			if w.fault != nil {
				err = w.fault
				break
			}
			amount := min(count/32, w.count)
			first := min(amount, workerQueueEvents-w.head)
			copy(data[:first*32], w.ring[w.head*32:(w.head+first)*32])
			copy(data[first*32:amount*32], w.ring[:(amount-first)*32])
			w.head = (w.head + amount) % workerQueueEvents
			w.count -= amount
			n = amount * 32
		case workerLineMode:
			if !w.active || count != 0 {
				err = errors.New("line mode needs an active decoded stream")
				break
			}
			if value == 0 {
				w.lines = nil
				break
			}
			if w.linesStore == nil { // first use: the one allocation, before any streaming load
				w.linesStore = decodedlines.New(int(value), workerLineCapacity)
			}
			w.linesStore.Reset(int(value))
			w.lines = w.linesStore
			if errors.Is(w.fault, errWorkerQueueOverflow) {
				// The queue overflowed before the switch (a busy bus fills it
				// while the stream is being set up, about a second): the
				// transcript says so and goes on, as it does for the FIFO's
				// own losses.
				w.fault = nil
				w.lines.Add([]sramcapture.DecodedEvent{{Kind: sramcapture.EventLoss, Count: uint32(min(w.queueLost, 1<<31))}})
			}
			w.queueLost = 0
			// Events drained before the switch go into the transcript too.
			for ; w.count > 0; w.count-- {
				e, perr := sramcapture.ParseDecodedEvent(w.ring[w.head*32 : (w.head+1)*32])
				if perr == nil {
					w.lines.Add([]sramcapture.DecodedEvent{e})
				}
				w.head = (w.head + 1) % workerQueueEvents
			}
		case workerLines:
			if !w.active || w.lines == nil || epoch != w.epoch {
				err = errors.New("no decoded line stream")
				break
			}
			if w.fault != nil {
				err = w.fault
				break
			}
			if plane&1 != 0 {
				w.lines.Flush()
			}
			n = w.lines.EncodeLines(int(sel)<<16|int(value), data[:count])
		case workerRecall:
			params := request[min(len(request), workerHeader):]
			if count == 0 || len(params) < 16 || (len(params)-4)%12 != 0 {
				err = errors.New("invalid recall request")
				break
			}
			out := sliceWriter{b: data[:count]}
			var reads [sramcapture.RecordReadsMax]sramcapture.RecordRead
			k := (len(params) - 4) / 12
			for i := range k {
				at := params[4+12*i:]
				reads[i] = sramcapture.RecordRead{Offset: binary.LittleEndian.Uint32(at), Bucket: binary.LittleEndian.Uint32(at[4:]), Count: binary.LittleEndian.Uint32(at[8:]), Dst: &out}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			err = w.capture.RecallRecord(ctx, sramcapture.RecordIdentity{Epoch: epoch, Record: binary.LittleEndian.Uint32(params)}, reads[:k])
			cancel()
			n = out.n
			if err == nil && n != count {
				err = fmt.Errorf("recall produced %d of %d bytes", n, count)
			}
		case workerRecallShared:
			params := request[min(len(request), workerHeader):]
			size := int(sel)<<16 | int(value)
			if count != 4 || len(params) < 16 || (len(params)-4)%12 != 0 || size == 0 {
				err = errors.New("invalid shared recall request")
				break
			}
			n = 4
			if w.sOn || !w.sShared || size > len(w.sring) {
				binary.LittleEndian.PutUint32(data, 0) // the app reads through the packets
				break
			}
			out := sliceWriter{b: w.sring[:size]}
			var reads [sramcapture.RecordReadsMax]sramcapture.RecordRead
			k := (len(params) - 4) / 12
			for i := range k {
				at := params[4+12*i:]
				reads[i] = sramcapture.RecordRead{Offset: binary.LittleEndian.Uint32(at), Bucket: binary.LittleEndian.Uint32(at[4:]), Count: binary.LittleEndian.Uint32(at[8:]), Dst: &out}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err = w.capture.RecallRecord(ctx, sramcapture.RecordIdentity{Epoch: epoch, Record: binary.LittleEndian.Uint32(params)}, reads[:k])
			cancel()
			if err == nil && out.n != size {
				err = fmt.Errorf("shared recall produced %d of %d bytes", out.n, size)
			}
			binary.LittleEndian.PutUint32(data, uint32(out.n)) //nolint:gosec // G115: a reply length bounded by the response buffer
		case workerStackDownload:
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err = w.capture.StackDownloadWords(ctx, int(value), data[:count])
			cancel()
			n = count
		case workerPanel:
			if count != 22 {
				err = errors.New("invalid panel request")
				break
			}
			var words [11]uint16
			words, err = w.capture.PanelWords()
			for i, v := range words {
				binary.LittleEndian.PutUint16(data[2*i:], v)
			}
			n = 22
		case workerStreamPump:
			if count != 0 {
				err = errors.New("invalid stream pump request")
				break
			}
			err = w.streamPumpOn(value&1 != 0)
		case workerStreamWords:
			if count < 8 || !w.sOn {
				err = errors.New("no stream pump")
				break
			}
			if w.sFault != nil {
				err = w.sFault
				break
			}
			amount := min(count-8, w.sCount) &^ 3
			binary.LittleEndian.PutUint64(data[:8], w.sFirst)
			for done := 0; done < amount; {
				k := copy(data[8+done:8+amount], w.sring[w.sHead:min(len(w.sring), w.sHead+amount-done)])
				done += k
				w.sHead = (w.sHead + k) % len(w.sring)
			}
			w.sCount -= amount
			w.sFirst += uint64(amount / 4) //nolint:gosec // G115: bytes consumed, never negative
			n = 8 + amount
		case workerStreamPeek:
			n, err = w.streamPeek(epoch, data)
		default:
			err = errors.New("unknown worker operation")
		}
	}
	if err != nil {
		w.response[10] = 1
		n = copy(data, err.Error())
	}
	binary.LittleEndian.PutUint32(w.response[16:20], uint32(n))
	return workerHeader + n
}

// sliceWriter fills b, failing past its end.
// TRLC-LINKS: REQ-SDS-001
type sliceWriter struct {
	b []byte
	n int
}

// Room hands out the writer's next n bytes for a recall to read into in
// place (sramcapture.RecallRoom).
// TRLC-LINKS: REQ-SDS-033
func (w *sliceWriter) Room(n int) []byte {
	if n > len(w.b)-w.n {
		return nil
	}
	return w.b[w.n : w.n+n]
}

// Fill makes the n bytes Room handed out the writer's.
// TRLC-LINKS: REQ-SDS-033
func (w *sliceWriter) Fill(n int) { w.n += n }

// TRLC-LINKS: REQ-SDS-001
func (w *sliceWriter) Write(p []byte) (int, error) {
	k := copy(w.b[w.n:], p)
	w.n += k
	if k != len(p) {
		return k, io.ErrShortWrite
	}
	return k, nil
}

// workerPollWait is how long the worker waits for a request after a pump
// that took busy: at least 500 us, and as long as the pump took, so the
// pumps take at most half the core. During a decoded stream the worker runs
// at real-time priority; a bus whose events never let up (a 104 MHz SPI
// flash read without pause) kept it pumping nine tenths of the time, and
// the app, the panel and the OTA agent starved until the agent killed the
// app for a stale health token. An event FIFO that overflows meanwhile
// marks the loss in the transcript.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-001
func workerPollWait(busy time.Duration) time.Duration {
	return max(500*time.Microsecond, busy)
}

// serve polls a local packet socket with a 500 us deadline. Commands wake it
// immediately; absent commands still leave the FPGA drain independent of Go's
// GUI goroutines. All packet and event buffers are allocated before streaming.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-013
func (w *acquisitionWorker) serve(fd int) error {
	if err := syscall.SetNonblock(fd, true); err != nil {
		return err
	}
	// Room for several full event packets; the kernel caps it at wmem_max.
	_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_SNDBUF, 512*1024)
	defer func() {
		if w.active {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = w.capture.Halt(ctx)
			_, _ = w.capture.EnableDecodedEvents(false)
		}
		if w.deadline != nil {
			_ = w.deadline.Close()
		}
	}()
	var request [workerHeader + workerRecallParams + 1]byte
	pending := 0
	for {
		t0 := time.Now()
		w.pump()
		w.streamPump()
		busy := time.Since(t0)
		if pending != 0 {
			n, _, errno := syscall.RawSyscall(syscall.SYS_WRITE, uintptr(fd), uintptr(unsafe.Pointer(&w.response[0])), uintptr(pending)) //nolint:gosec // G103: the system call takes its argument by address
			if errno == 0 {
				if int(n) != pending {
					return errors.New("short worker response")
				}
				pending = 0
			} else if errno != syscall.EAGAIN && errno != syscall.EINTR && errno != syscall.ENOBUFS {
				// ENOBUFS is transient on a packet socket: the kernel could not
				// buffer this response yet. Exiting on it kills the app's bus
				// at 1.5 MBd; the next iteration retries.
				return errno
			}
		}
		if pending == 0 { //nolint:nestif // the socket's read path, taken only with no reply pending
			n, _, errno := syscall.RawSyscall(syscall.SYS_READ, uintptr(fd), uintptr(unsafe.Pointer(&request[0])), uintptr(len(request))) //nolint:gosec // G103: the system call takes its argument by address
			if errno == 0 {
				if n == 0 {
					return nil
				}
				op := binary.LittleEndian.Uint16(request[8:10])
				withParams := int(n) > workerHeader && int(n) <= workerHeader+workerRecallParams && (op == workerRecall || op == workerRecallShared || op == workerReadMany || op == workerWriteMany)
				if int(n) != workerHeader && !withParams || binary.LittleEndian.Uint32(request[:4]) != workerMagic || request[11] != 1 {
					return errors.New("invalid worker request framing")
				}
				pending = w.handle(request[:n])
				// Answer before the next FPGA drain: a pump between request and
				// reply puts a FIFO read into every register round trip and holds
				// a decoded stream below 921600 Bd.
				n, _, errno := syscall.RawSyscall(syscall.SYS_WRITE, uintptr(fd), uintptr(unsafe.Pointer(&w.response[0])), uintptr(pending)) //nolint:gosec // G103: the system call takes its argument by address
				if errno == 0 {
					if int(n) != pending {
						return errors.New("short worker response")
					}
					pending = 0
				} else if errno != syscall.EAGAIN && errno != syscall.EINTR && errno != syscall.ENOBUFS {
					return errno
				}
				continue
			} else if errno != syscall.EAGAIN && errno != syscall.EINTR {
				return errno
			}
		}
		poll := struct {
			FD              int32
			Events, Revents int16
		}{FD: int32(fd), Events: 1} //nolint:gosec // G115: an open descriptor, small and non-negative
		if pending != 0 {
			poll.Events = 4
		}
		wait := workerPollWait(busy)
		if w.sOn && !w.active {
			// Only the stream banks to serve: the look comes a bank's fill
			// time on (a slow stream is not looked at every 500 us), and
			// not after a wait as long as the drain took: the drum's FIFO
			// and the host's two banks hold under 5 ms of a 2 M words a
			// second stream, a stream that spills into the ring is read
			// back about a burst a lap, no faster than it comes, and the
			// drain itself is bus time the stream's rate bounds.
			wait = w.pumpWait()
			if w.spinning() {
				wait = w.spinPause(busy)
			} else if w.sEvery == streamPumpFastest {
				// A backlog: the drum refills a bank as soon as one is free,
				// so the next look comes at once, not after a wait as long
				// as the drain took (the loop's own pacing halved the
				// drain's rate and the drum's FIFO filled at 2^8).
				wait = streamPumpFastest
			}
		}
		timeout := syscall.NsecToTimespec(int64(wait))
		_, _, errno := syscall.RawSyscall6(syscall.SYS_PPOLL, uintptr(unsafe.Pointer(&poll)), 1, uintptr(unsafe.Pointer(&timeout)), 0, 0, 0) //nolint:gosec // G103: the system call takes its argument by address
		if errno != 0 && errno != syscall.EINTR {
			return errno
		}
		if poll.Revents&(8|16|32) != 0 {
			return nil
		}
	}
}
