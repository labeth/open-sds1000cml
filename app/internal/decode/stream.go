// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"cmp"
	"slices"
	"sort"
)

// StreamSpan is a decoded token of a continuous stream, its sample indices
// counted from the stream's first sample.
type StreamSpan struct {
	I0, I1 uint64
	Text   string
	Kind   string
	Val    int
}

// StreamFrame is one whole frame of a continuous stream: the spans from a
// start of frame (or a silence) up to the next.
type StreamFrame struct {
	I0, I1 uint64
	Spans  []StreamSpan
}

// StreamDecoder runs a whole-buffer decoder over a continuous sample stream
// that arrives in chunks (ADR-STREAM-SOFT-DECODE). It decodes a window
// starting in a silence between frames, hands out each frame once it is
// whole - the next frame has begun, or the line has carried no decoded span
// for Close samples after it - and moves the window's start into the
// silence after the last frame handed out. Every frame is decoded whole
// from a window that holds all of it and starts where the line is idle, so
// a chunk boundary neither cuts a frame nor hands it out twice.
//
// Close must exceed the longest stretch of a frame without a decoded span
// (a frame whose decoder reads it whole - Wiegand's value at its last bit -
// is one such stretch), and Max the longest frame plus Close: a window that
// grows past Max (a bus that never pauses) hands out its spans up to Close
// before its end as they stand.
type StreamDecoder struct {
	decode     func(a, b []uint8) Result
	closeN     int
	maxN       int
	a, b       []uint8
	base, done uint64
	// Sorted span and frame group scratch, reused.
	spans  []Span
	groups []streamGroup
	// Subframes: S/PDIF's frames run without a pause and only every 192nd
	// opens with a "sof" (B); its M and W subframes open frames too.
	subframes bool
	leadN     int
	// An edge stream (NewEdgeStreamDecoder): its window is the lines'
	// edges, decoded by edec, with the same windows and frames.
	edges  bool
	edec   func(a, b *EdgeLine) Result
	ea, eb EdgeLine
}

// streamGroup is a frame's spans in the sorted scratch (from, to) and its
// sample extent in the window (i0, i1).
type streamGroup struct{ from, to, i0, i1 int }

// SetLead keeps n samples of line before a frame's start in the window
// (StreamLead).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
func (d *StreamDecoder) SetLead(n int) { d.leadN = max(n, 0) }

// StreamRecord decodes one record of a stream of records (a, b its lines'
// samples, b nil for one line): the frames it holds whole. A frame its end
// cuts (none Close samples after it) is dropped, and so is one starting in
// its first head samples, which its start can cut and the decoder read from
// the middle.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func StreamRecord(a, b []uint8, decode func(a, b []uint8) Result, closeN, head, cutN int, subframes bool) []StreamFrame {
	from := 0
	if cutN > 0 {
		// A decoder that finds its frames by their first edge and follows a
		// transaction (Miller, SWIM) reads a cut frame's rest as frames
		// of its own, and the frames after it in the wrong place of the
		// transaction: it decodes from the record's first quiet line, if one
		// comes within cutN.
		at := quietAt(a, b, closeN)
		if at < 0 || at > cutN {
			return nil
		}
		from = max(0, at-closeN/2)
	}
	d := NewStreamDecoder(decode, closeN, len(a)+1)
	d.SetSubframes(subframes)
	var bl []uint8
	if b != nil {
		bl = b[from:]
	}
	frames := d.Push(a[from:], bl)
	for i := range frames {
		f := &frames[i]
		f.I0, f.I1 = f.I0+uint64(from), f.I1+uint64(from)
		for k := range f.Spans {
			f.Spans[k].I0 += uint64(from)
			f.Spans[k].I1 += uint64(from)
		}
	}
	for len(frames) > 0 && frames[0].I0 < uint64(head) {
		frames = frames[1:]
	}
	// A frame cut by the record's start decodes without its start of frame
	// (a 1-Wire transaction's bytes without their reset).
	if len(frames) > 0 && len(frames[0].Spans) > 0 && frames[0].Spans[0].Kind != "sof" && frames[0].Spans[0].Kind != "break" {
		frames = frames[1:]
	}
	return frames
}

// StreamRecordFromIdle is StreamRecord for a protocol a decoder opened in
// the middle of its frames reads out of step (StreamRecordAfterIdle): it
// decodes from the record's first idle line - the first gap between two
// decoded characters longer than half of one, past the stop bits that
// part characters sent back to back - and returns nothing from a record
// with none. A device's reply after its
// guard time follows an idle line too; a record whose first idle is that
// one reads the reply as an instruction.
// TRLC-LINKS: REQ-SDS-260
func StreamRecordFromIdle(a, b []uint8, decode func(a, b []uint8) Result, closeN, head, cutN int, subframes bool) []StreamFrame {
	r := decode(a, b)
	from, last := -1, -1
	for k := 0; k < len(r.Spans) && from < 0; k++ {
		q := r.Spans[k]
		if q.I1 <= q.I0 { // a mark, as a start of frame
			continue
		}
		if last >= 0 {
			if p := r.Spans[last]; 2*(q.I0-p.I1) > p.I1-p.I0 {
				from = p.I1
			}
		}
		last = k
	}
	if from < 0 {
		return nil
	}
	var bl []uint8
	if b != nil {
		bl = b[from:]
	}
	frames := StreamRecord(a[from:], bl, decode, closeN, head, cutN, subframes)
	for i := range frames {
		f := &frames[i]
		f.I0, f.I1 = f.I0+uint64(from), f.I1+uint64(from)
		for k := range f.Spans {
			f.Spans[k].I0 += uint64(from)
			f.Spans[k].I1 += uint64(from)
		}
	}
	return frames
}

// quietAt is the first sample after n samples in which neither line swings
// a quarter of its range, -1 none.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
func quietAt(a, b []uint8, n int) int {
	last := -1 // the last sample where a line moved
	moved := func(codes []uint8) func(i int) bool {
		if codes == nil {
			return func(int) bool { return false }
		}
		lo, hi := codes[0], codes[0]
		for _, v := range codes {
			lo, hi = min(lo, v), max(hi, v)
		}
		mid := (int(lo) + int(hi)) / 2
		return func(i int) bool { return i > 0 && (int(codes[i]) > mid) != (int(codes[i-1]) > mid) }
	}
	ma, mb := moved(a), moved(b)
	for i := range a {
		if ma(i) || mb(i) {
			last = i
		}
		if i-last >= n {
			return i
		}
	}
	return -1
}

// SetSubframes makes the "rw" spans open frames as well (S/PDIF's M and W
// subframes): a bus that never pauses then still has frames to hand out.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *StreamDecoder) SetSubframes(on bool) { d.subframes = on }

// isFrameStart reports a span that opens a frame.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *StreamDecoder) isFrameStart(s Span) bool {
	return s.Kind == "sof" || s.Kind == "break" || (d.subframes && s.Kind == "rw")
}

// NewStreamDecoder makes a stream decoder over decode (a, b: the lines'
// samples, b nil for a one-line bus); closeN and maxN are Close and Max in
// samples.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func NewStreamDecoder(decode func(a, b []uint8) Result, closeN, maxN int) *StreamDecoder {
	closeN = max(closeN, 2)
	return &StreamDecoder{decode: decode, closeN: closeN, maxN: max(maxN, 4*closeN)}
}

// NewEdgeStreamDecoder is NewStreamDecoder over digital lines' edges
// (EdgeLine): decode takes the window's lines (b nil for a one-line bus)
// and the stream is pushed with PushEdges. Its frames are the ones the
// samples' decoder hands out from the same lines.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func NewEdgeStreamDecoder(decode func(a, b *EdgeLine) Result, closeN, maxN int) *StreamDecoder {
	d := NewStreamDecoder(nil, closeN, maxN)
	d.edges, d.edec = true, decode
	return d
}

// Held is the samples the window holds, and Next the stream index of the
// sample the next Push starts at.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *StreamDecoder) Held() int {
	if d.edges {
		return d.ea.N
	}
	return len(d.a)
}

// Next is the stream index of the next sample pushed.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *StreamDecoder) Next() uint64 { return d.base + uint64(d.Held()) }

// PushEdges appends samples from..to-1 of an edge stream's lines (b nil,
// or as long as a) and returns the frames they complete, oldest first.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *StreamDecoder) PushEdges(a, b *EdgeLine, from, to int) []StreamFrame {
	d.ea.AppendRange(a, from, to)
	if b != nil {
		d.eb.AppendRange(b, from, to)
	}
	return d.run()
}

// Push appends the next samples (b nil, or as long as a) and returns the
// frames they complete, oldest first.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *StreamDecoder) Push(a, b []uint8) []StreamFrame {
	d.a = append(d.a, a...)
	if b != nil {
		d.b = append(d.b, b...)
	}
	return d.run()
}

// Flush ends the stream (a capture's last sample): the frames its end
// completes are handed out, a frame still open at the end is dropped, and
// the decoder starts empty at the next index.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *StreamDecoder) Flush() []StreamFrame {
	out := d.run()
	d.Skip(0)
	return out
}

// Skip drops the window and moves the stream on by n samples that will not
// arrive (lost words, a gap between captures): decoding starts afresh after
// them.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *StreamDecoder) Skip(n int) {
	d.base += uint64(d.Held() + n)
	d.done = max(d.done, d.base)
	d.a, d.b = d.a[:0], d.b[:0]
	d.ea.Reset()
	d.eb.Reset()
}

// run decodes the window and hands out its whole frames.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *StreamDecoder) run() []StreamFrame {
	n := d.Held()
	if n == 0 {
		return nil
	}
	var r Result
	if d.edges {
		var bl *EdgeLine
		if d.eb.N == n {
			bl = &d.eb
		}
		r = d.edec(&d.ea, bl)
	} else {
		var bl []uint8
		if len(d.b) == n {
			bl = d.b
		}
		r = d.decode(d.a, bl)
	}
	d.spans = d.spans[:0]
	// The spans of a decode that is not OK count too: a window holding only
	// a frame that fails its check (a DHT checksum) or an NEC repeat is not
	// OK, and those frames belong in the stream with their errors.
	for _, s := range r.Spans {
		if s.I1 < s.I0 {
			s.I1 = s.I0
		}
		if s.I0 >= 0 && s.I0 < n && d.base+uint64(s.I0) >= d.done {
			d.spans = append(d.spans, s)
		}
	}
	sp := d.spans
	// Decoders report their spans in order nearly always: sort only when not.
	if !slices.IsSortedFunc(sp, func(x, y Span) int { return cmp.Compare(x.I0, y.I0) }) {
		sort.SliceStable(sp, func(i, j int) bool { return sp[i].I0 < sp[j].I0 })
	}
	// Group into frames: a start of frame, or a silence longer than Close,
	// opens the next.
	groups := d.groups[:0]
	for k, s := range sp {
		if len(groups) == 0 || d.isFrameStart(s) || s.I0-groups[len(groups)-1].i1 > d.closeN {
			groups = append(groups, streamGroup{from: k, to: k + 1, i0: s.I0, i1: s.I1})
			continue
		}
		g := &groups[len(groups)-1]
		g.to, g.i1 = k+1, max(g.i1, s.I1)
	}
	d.groups = groups
	var out []StreamFrame
	// The frames' spans share one array, each frame's capped at its own end.
	var all []StreamSpan
	cut, last := 0, -1 // last: the end of the last frame handed out
	take := func(g streamGroup) {
		if all == nil {
			all = make([]StreamSpan, 0, len(sp)-g.from)
		}
		at := len(all)
		for _, s := range sp[g.from:g.to] {
			all = append(all, StreamSpan{I0: d.base + uint64(s.I0), I1: d.base + uint64(s.I1), Text: s.Text, Kind: s.Kind, Val: s.Val})
		}
		out = append(out, StreamFrame{I0: d.base + uint64(g.i0), I1: d.base + uint64(g.i1), Spans: all[at:len(all):len(all)]})
		last = g.i1
	}
	k := 0
	for ; k < len(groups); k++ {
		g := groups[k]
		// Whole only with Close samples after it: the window's end can cut
		// the frame, and a decoder can read a cut frame's head as a frame of
		// its own (a start of frame alone) followed by a garbled rest.
		if n-1-g.i1 <= d.closeN {
			break
		}
		take(g)
	}
	if k < len(groups) && n-groups[k].i0 > d.maxN {
		// A bus that never pauses: hand out the spans that end Close before
		// the window's end as a frame, as they stand.
		g := groups[k]
		j := g.from
		for j < g.to && sp[j].I1 < n-d.closeN {
			j++
		}
		if j > g.from {
			take(streamGroup{from: g.from, to: j, i0: g.i0, i1: sp[j-1].I1})
			groups[k].from = j
			if j < g.to {
				groups[k].i0 = sp[j].I0
			}
		}
	}
	if last >= 0 {
		// Start the next window in the silence after the last frame handed
		// out: half way to the next span, so the decoder meets the line idle.
		next := n
		if k < len(groups) && groups[k].from < groups[k].to {
			next = groups[k].i0
		}
		cut = max(last+1, last+(next-last)/2)
		cut = min(cut, max(last+1, n-d.closeN), next)
		// A frame the decoder finds by what precedes it (DCF77's second 0 by
		// the missing pulse before it) keeps Lead samples before it; the
		// last frame's spans that come again lie before done and are dropped.
		cut = max(0, min(cut, next-d.leadN))
		d.done = d.base + uint64(last) + 1
	} else if k == len(groups) || groups[k].from >= groups[k].to {
		// Nothing decoded: keep only the Close samples a frame can open in.
		cut = max(0, n-d.closeN-d.leadN)
	} else {
		// A frame under way: what lies Close before it decoded to nothing.
		cut = max(0, groups[k].i0-d.closeN-d.leadN)
	}
	// Past twice Max (and the lead) no frame is under way, whatever a
	// decoder makes of the line: a span it reports up to the window's end
	// on a line that is no bus of it (Morse's endless letter on a steady
	// square) kept the window, and each push's decode of it, growing for the
	// whole session.
	if limit := 2*d.maxN + d.leadN; n-cut > limit {
		cut = n - limit
		d.done = max(d.done, d.base+uint64(cut))
	}
	if cut > 0 {
		cut = min(cut, n)
		if d.edges {
			d.ea.Drop(cut)
			if d.eb.N == n {
				d.eb.Drop(cut)
			}
		} else {
			d.a = d.a[:copy(d.a, d.a[cut:])]
			if len(d.b) == n {
				d.b = d.b[:copy(d.b, d.b[cut:])]
			}
		}
		d.base += uint64(cut)
	}
	return out
}
