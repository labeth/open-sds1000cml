// The stream ring shared by the acquisition worker and the app
// (ADR-STREAM-IMAGE): the worker's EDMA pops each stream bank straight into
// it and the app's reader takes the words where they landed. Before it, a
// stream word was copied five times on its way to the decoder (bounce
// buffer, bank buffer, worker ring, socket reply, the kernel's socket buffer
// and out of it), and on the Cortex-A8 a copy of the stream costs about as
// much CPU as the DMA saves.
//
// The ring is an unlinked RAM file the app creates before it starts the
// worker and hands it as a fourth descriptor. The worker maps it shared,
// pins it and resolves its pages for the DMA (PinSharedRing); the app maps
// it read-only. The worker's ring counters stay the only truth: the app asks
// for the words ready (workerStreamPeek) and in the same request gives back
// the bytes it finished with, which the worker may then fill again.
// ENGMODEL-OWNER-UNIT: FU-APP-BUS
package bus

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
)

// sharedRingFD is the shared ring's descriptor in the worker (after the
// GPMC descriptor 3 and the socket 4).
const sharedRingFD = 5

// ErrNoSharedRing: the worker's stream ring is not shared with the app; the
// reader takes the words through ReadIsolatedStream.
var ErrNoSharedRing = errors.New("bus: no shared stream ring")

// createSharedRing makes the ring's file in a RAM file system and unlinks it:
// it lives as long as a process holds it.
// TRLC-LINKS: REQ-SDS-035
func createSharedRing() (*os.File, error) {
	var last error
	for _, dir := range []string{"/dev/shm", "/dev"} {
		f, err := os.CreateTemp(dir, ".sds-stream-ring-")
		if err != nil {
			last = err
			continue
		}
		_ = os.Remove(f.Name())
		if err = f.Truncate(workerStreamBytes); err != nil {
			f.Close()
			last = err
			continue
		}
		return f, nil
	}
	return nil, last
}

// mapSharedRing maps the ring read-only in the app.
// TRLC-LINKS: REQ-SDS-035
func mapSharedRing(f *os.File) ([]byte, error) {
	return syscall.Mmap(int(f.Fd()), 0, workerStreamBytes, syscall.PROT_READ, syscall.MAP_SHARED)
}

// workerHasSharedRing reports whether the app handed the worker a ring.
// TRLC-LINKS: REQ-SDS-035
func workerHasSharedRing() bool {
	var st syscall.Stat_t
	return syscall.Fstat(sharedRingFD, &st) == nil && st.Mode&syscall.S_IFMT == syscall.S_IFREG && st.Size == workerStreamBytes
}

// Reserve hands DrainStream the ring's free room for n bytes, in two pieces
// where it wraps.
// TRLC-LINKS: REQ-SDS-035
func (r streamRingWriter) Reserve(n int) (a, b []byte, err error) {
	w := r.w
	if w.sCount+n > len(w.sring) {
		return nil, nil, fmt.Errorf("isolated stream ring overflow; words lost")
	}
	at := (w.sHead + w.sCount) % len(w.sring)
	a = w.sring[at:min(len(w.sring), at+n)]
	return a, w.sring[:n-len(a)], nil
}

// Commit makes the reserved bytes readable.
// TRLC-LINKS: REQ-SDS-035
func (r streamRingWriter) Commit(n int) { r.w.sCount += n }

// streamPeek releases the bytes the app read and says where the ready words
// are: their first ordinal, the ring offset and the byte count.
// TRLC-LINKS: REQ-SDS-035
func (w *acquisitionWorker) streamPeek(release uint32, data []byte) (int, error) {
	if !w.sOn || !w.sShared {
		return 0, ErrNoSharedRing
	}
	if int(release) > w.sCount || release%4 != 0 {
		return 0, fmt.Errorf("invalid stream release %d of %d", release, w.sCount)
	}
	w.sHead = (w.sHead + int(release)) % len(w.sring)
	w.sCount -= int(release)
	w.sFirst += uint64(release / 4)
	if w.sFault != nil {
		return 0, w.sFault
	}
	binary.LittleEndian.PutUint64(data[0:], w.sFirst)
	binary.LittleEndian.PutUint32(data[8:], uint32(w.sHead))
	binary.LittleEndian.PutUint32(data[12:], uint32(w.sCount))
	return 16, nil
}

// PeekIsolatedStream gives back the release bytes of the last peek and
// returns the words the worker has drained since, in place in the shared
// ring (b is the part past its wrap): valid until the next peek or the end
// of the pump. ErrNoSharedRing means the app reads the words through
// ReadIsolatedStream instead.
// TRLC-LINKS: REQ-SDS-035
func (d *Dev) PeekIsolatedStream(release int) (first uint64, a, b []byte, err error) {
	if d.remote == nil || d.remote.shared == nil {
		return 0, nil, nil, ErrNoSharedRing
	}
	var reply [16]byte
	n, err := d.remote.call(workerStreamPeek, 0, 0, 0, uint32(release), reply[:])
	if err != nil {
		if strings.HasSuffix(err.Error(), ErrNoSharedRing.Error()) {
			err = ErrNoSharedRing
		}
		return 0, nil, nil, err
	}
	if n != 16 {
		return 0, nil, nil, fmt.Errorf("bus: short worker peek reply")
	}
	ring := d.remote.shared
	first = binary.LittleEndian.Uint64(reply[0:])
	at, count := int(binary.LittleEndian.Uint32(reply[8:])), int(binary.LittleEndian.Uint32(reply[12:]))
	if at >= len(ring) || count > len(ring) {
		return 0, nil, nil, fmt.Errorf("bus: worker peek out of the ring (%d+%d)", at, count)
	}
	a = ring[at:min(len(ring), at+count)]
	return first, a, ring[:count-len(a)], nil
}
