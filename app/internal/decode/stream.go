// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import "sort"

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
	// Sorted span scratch, reused.
	spans []Span
	// Subframes: S/PDIF's frames run without a pause and only every 192nd
	// opens with a "sof" (B); its M and W subframes open frames too.
	subframes bool
}

// StreamRecord decodes one record of a stream of records (a, b its lines'
// samples, b nil for one line): the frames it holds whole. A frame its end
// cuts (none Close samples after it) is dropped, and so is one starting in
// its first head samples, which its start can cut and the decoder read from
// the middle.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func StreamRecord(a, b []uint8, decode func(a, b []uint8) Result, closeN, head int, subframes bool) []StreamFrame {
	d := NewStreamDecoder(decode, closeN, len(a)+1)
	d.SetSubframes(subframes)
	frames := d.Push(a, b)
	for len(frames) > 0 && frames[0].I0 < uint64(head) {
		frames = frames[1:]
	}
	return frames
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

// Held is the samples the window holds, and Next the stream index of the
// sample the next Push starts at.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *StreamDecoder) Held() int { return len(d.a) }

// Next is the stream index of the next sample pushed.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *StreamDecoder) Next() uint64 { return d.base + uint64(len(d.a)) }

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
	d.base += uint64(len(d.a) + n)
	d.done = max(d.done, d.base)
	d.a, d.b = d.a[:0], d.b[:0]
}

// run decodes the window and hands out its whole frames.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *StreamDecoder) run() []StreamFrame {
	n := len(d.a)
	if n == 0 {
		return nil
	}
	var bl []uint8
	if len(d.b) == n {
		bl = d.b
	}
	r := d.decode(d.a, bl)
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
	sort.SliceStable(sp, func(i, j int) bool { return sp[i].I0 < sp[j].I0 })
	// Group into frames: a start of frame, or a silence longer than Close,
	// opens the next.
	type group struct{ from, to, i0, i1 int }
	var groups []group
	for k, s := range sp {
		if len(groups) == 0 || d.isFrameStart(s) || s.I0-groups[len(groups)-1].i1 > d.closeN {
			groups = append(groups, group{from: k, to: k + 1, i0: s.I0, i1: s.I1})
			continue
		}
		g := &groups[len(groups)-1]
		g.to, g.i1 = k+1, max(g.i1, s.I1)
	}
	var out []StreamFrame
	cut, last := 0, -1 // last: the end of the last frame handed out
	take := func(g group) {
		f := StreamFrame{I0: d.base + uint64(g.i0), I1: d.base + uint64(g.i1)}
		for _, s := range sp[g.from:g.to] {
			f.Spans = append(f.Spans, StreamSpan{I0: d.base + uint64(s.I0), I1: d.base + uint64(s.I1), Text: s.Text, Kind: s.Kind, Val: s.Val})
		}
		out = append(out, f)
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
			take(group{from: g.from, to: j, i0: g.i0, i1: sp[j-1].I1})
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
		d.done = d.base + uint64(last) + 1
	} else if k == len(groups) || groups[k].from >= groups[k].to {
		// Nothing decoded: keep only the Close samples a frame can open in.
		cut = max(0, n-d.closeN)
	} else {
		// A frame under way: what lies Close before it decoded to nothing.
		cut = max(0, groups[k].i0-d.closeN)
	}
	if cut > 0 {
		cut = min(cut, n)
		d.a = d.a[:copy(d.a, d.a[cut:])]
		if len(d.b) == n {
			d.b = d.b[:copy(d.b, d.b[cut:])]
		}
		d.base += uint64(cut)
	}
	return out
}
