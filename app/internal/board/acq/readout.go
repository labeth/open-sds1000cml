// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/labeth/open-dso-core/dsp"
	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// FrameWriter splits recalled record words into frame F's two traces: two
// 8-bit samples a channel a word on a raw record, one Q8.8 sample a channel
// (its codes rounded beside) on a decimated one. N counts the samples
// written a channel.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-040
type FrameWriter struct {
	F *sample.Frame
	M sramcapture.Metadata
	N int
}

// Write splits a recalled payload of whole words into the frame's samples.
// TRLC-LINKS: REQ-SDS-040
func (w *FrameWriter) Write(b []byte) (int, error) {
	if len(b)%4 != 0 {
		return 0, errors.New("unaligned SRAM payload")
	}
	samples := len(b) / 4 * int(w.M.SamplesPerWord)
	if w.N+samples > w.F.Len() {
		return 0, errors.New("SRAM payload exceeds frame")
	}
	q88 := w.M.FractionBits == 8 && w.M.SamplesPerWord == 1
	packed := w.M.FractionBits != 8 && w.M.SamplesPerWord == 2
	if !q88 && !packed {
		return w.writeWords(b) // a layout other than the capture's two
	}
	// the word format chosen once; the destinations sliced to the samples
	n, f := w.N, w.F
	c1, c2 := f.Codes8(0)[n:n+samples], f.Codes8(1)[n:n+samples]
	if w.M.FractionBits == 8 {
		dsp.SplitQ8(f.Precision(0)[n:n+samples], f.Precision(1)[n:n+samples], c1, c2, b)
	} else {
		for k := 0; k+1 < len(c1); k += 2 {
			i := 2 * k
			c1[k], c2[k] = b[i], b[i+1]
			c1[k+1], c2[k+1] = b[i+2], b[i+3]
		}
	}
	w.N += samples
	return len(b), nil
}

// writeWords is Write word by word, for any samples-a-word count.
// TRLC-LINKS: REQ-SDS-040
func (w *FrameWriter) writeWords(b []byte) (int, error) {
	c1, c2, q1, q2 := w.F.Codes8(0), w.F.Codes8(1), w.F.Precision(0), w.F.Precision(1)
	for i := 0; i < len(b); i += 4 {
		if w.M.FractionBits == 8 {
			a, c := binary.LittleEndian.Uint16(b[i:]), binary.LittleEndian.Uint16(b[i+2:])
			q1[w.N], q2[w.N] = a, c
			c1[w.N], c2[w.N] = core.RoundQ8(a), core.RoundQ8(c)
			w.N++
		} else {
			c1[w.N], c2[w.N] = b[i], b[i+1]
			w.N++
			c1[w.N], c2[w.N] = b[i+2], b[i+3]
			w.N++
		}
	}
	return len(b), nil
}

// ErrShortRead is a read whose main window got fewer samples than its
// frame holds.
var ErrShortRead = errors.New("short SRAM frame")

// readTimeout bounds a read the caller gave no deadline.
const readTimeout = 10 * time.Second

type readoutFacet Board

// Read reads req's windows of the halted record into dst (one frame a
// window), each frame sized by the caller. yield runs between the bursts
// of a long read. A main window that does not fill its frame is
// ErrShortRead; another window's frame is cut to what it got.
func (r *readoutFacet) Read(req hal.ReadRequest, dst []*sample.Frame, yield func()) error {
	b := (*Board)(r)
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	if err := b.ReadContext(ctx, req, dst, yield); err != nil {
		return err
	}
	for i, f := range dst {
		if n := b.Written(i); n != f.Len() {
			if req.Windows[i].Role == sample.Main {
				return ErrShortRead
			}
			f.SetLen(n)
		}
	}
	return nil
}

// Export is the slow per-channel raw path; the SDS1000CML+ reads raw
// records through Read.
func (r *readoutFacet) Export(context.Context, hal.RecordID, int, *sample.Trace) error {
	return hal.Refuse("raw export", "the record's raw words are read through Read")
}

// Limits is what one Read does: the windows of one isolated recall.
func (r *readoutFacet) Limits() hal.ReadLimits {
	return hal.ReadLimits{MaxPoints: int(sramcapture.SamplesPerChannel), MaxWindows: sramcapture.RecordReadsMax, MaxSegments: 1, ChannelsPerWindow: 2}
}

// SetReadProgress installs the fabric's span hook: progress hears each
// burst of a long recall, then the read's yield runs (the lamp flush).
// OWNER.
func (b *Board) SetReadProgress(progress func(done, total int)) {
	b.progress = progress
	b.mem.SetSpanHook(func(done, total int) {
		if b.progress != nil {
			b.progress(done, total)
		}
		if b.yield != nil {
			b.yield()
		} else {
			b.panel.flushLamps() // a recall outside Read: the lamps all the same
		}
	})
}

// readScratch is a read's working slices, kept between reads: a live
// frame reads every few milliseconds.
type readScratch struct {
	windows []window
	writers []FrameWriter
	reads   []sramcapture.RecordRead
	written []int
}

// window is a read window in record words.
type window struct {
	offset, words, bucket uint32
}

// wordsOf is window w of record m in words. Its start and span are whole
// words of samples relative to the trigger word; its points are buckets.
func wordsOf(m sramcapture.Metadata, w hal.Window) (window, error) {
	per := int64(m.SamplesPerWord)
	if per == 0 || w.StartSample%per != 0 || w.Span%per != 0 || w.Span <= 0 {
		return window{}, fmt.Errorf("window %d+%d is not whole words of %d samples", w.StartSample, w.Span, per)
	}
	off := int64(m.TriggerIndex) + w.StartSample/per
	words := w.Span / per
	if off < 0 || off+words > int64(m.Length) {
		return window{}, fmt.Errorf("window [%d, %d) words outside the record's %d", off, off+words, m.Length)
	}
	out := window{offset: uint32(off), words: uint32(words)} //nolint:gosec // G115: bounded by the record above
	if w.Points > 0 && w.Want == sample.PeakPairs {
		if words%int64(w.Points) != 0 {
			return window{}, fmt.Errorf("%d words do not split into %d buckets", words, w.Points)
		}
		out.bucket = uint32(words / int64(w.Points)) //nolint:gosec // G115: bounded by the record
	}
	return out, nil
}

// Window is the hal.Window that reads words [offset, offset+words) of
// record m, in buckets of bucket words (0: every sample).
func Window(m sramcapture.Metadata, role sample.Role, offset, words, bucket uint32) hal.Window {
	per := int64(m.SamplesPerWord)
	w := hal.Window{Role: role, StartSample: (int64(offset) - int64(m.TriggerIndex)) * per, Span: int64(words) * per}
	if bucket != 0 {
		w.Points, w.Want = int(words/bucket), sample.PeakPairs
	}
	return w
}

// ReadContext is Read under ctx: a cancelled context abandons a long read
// between its bursts (a raw read nobody wants any more, a sound stream
// taking the fabric). One raw main window streams in bursts, yield between
// them; any other set of windows is one isolated recall of the record.
// Written(i) is how many samples a channel window i got; the frames keep
// their lengths, so the caller tells a short window from a full one.
func (b *Board) ReadContext(ctx context.Context, req hal.ReadRequest, dst []*sample.Frame, yield func()) error {
	m := b.rec
	b.scratch.written = b.scratch.written[:0]
	if len(dst) != len(req.Windows) {
		return fmt.Errorf("%d frames for %d windows", len(dst), len(req.Windows))
	}
	ws := b.scratch.windows[:0]
	for range req.Windows {
		ws = append(ws, window{})
	}
	b.scratch.windows = ws
	for i, w := range req.Windows {
		var err error
		if ws[i], err = wordsOf(m, w); err != nil {
			return err
		}
	}
	b.reading, b.yield = true, yield
	defer func() { b.reading, b.yield = false, nil }()
	b.count(func(d *boardDiag) { d.Reads++ })
	if len(ws) == 1 && req.Windows[0].Role == sample.Main && ws[0].bucket == 0 {
		w := FrameWriter{F: dst[0], M: m}
		recall := b.mem.Recall
		if m.Revision == 10 {
			recall = b.mem.RecallForward
		}
		_, err := recall(ctx, ws[0].offset, ws[0].words, &w)
		b.scratch.written = append(b.scratch.written[:0], w.N)
		if err != nil {
			b.count(func(d *boardDiag) { d.ReadErrs++ })
		}
		return err
	}
	writers, reads := b.scratch.writers[:0], b.scratch.reads[:0]
	for range ws {
		writers, reads = append(writers, FrameWriter{}), append(reads, sramcapture.RecordRead{})
	}
	b.scratch.writers, b.scratch.reads = writers, reads
	for i, w := range ws {
		writers[i] = FrameWriter{F: dst[i], M: m}
		count := w.words
		if w.bucket != 0 {
			count = w.words / w.bucket
		}
		reads[i] = sramcapture.RecordRead{Offset: w.offset, Bucket: w.bucket, Count: count, Dst: &writers[i]}
	}
	err := b.mem.RecallReadsOf(ctx, m, reads...)
	b.scratch.written = b.scratch.written[:0]
	for i := range writers {
		b.scratch.written = append(b.scratch.written, writers[i].N)
	}
	if err != nil {
		b.count(func(d *boardDiag) { d.ReadErrs++ })
	}
	return err
}

// Written is how many samples a channel the last read put in its i-th
// window's frame.
func (b *Board) Written(i int) int {
	if i < 0 || i >= len(b.scratch.written) {
		return 0
	}
	return b.scratch.written[i]
}

// Recall streams words [offset, offset+count) of the frozen record into
// dst, forwards on a revision 10 fabric; yield runs between its bursts.
// OWNER.
func (b *Board) Recall(ctx context.Context, offset, count uint32, dst io.Writer, yield func()) (int64, error) {
	b.reading, b.yield = true, yield
	defer func() { b.reading, b.yield = false, nil }()
	if b.rec.Revision == 10 {
		return b.mem.RecallForward(ctx, offset, count, dst)
	}
	return b.mem.Recall(ctx, offset, count, dst)
}

// EnvelopeDiagnostics is the fabric's envelope reject count and its last
// reason.
func (b *Board) EnvelopeDiagnostics() (int, string) { return b.mem.EnvelopeDiagnostics() }
