// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"open-sds/app/internal/engine"
)

// The client decode's transport (ADR-STREAM-CLIENT-DECODE): a page's web
// worker (webdec_worker.js) long-polls /api/stream/words.bin for the digital
// stream's words as the scope's ring holds them, decodes them, and posts the
// frames to /api/stream/frames.bin, which the scope queues as its own.

// clientDecoder is the engine's client decode (engine/soft_client.go).
type clientDecoder interface {
	ReadClientWords(seq uint64, max int, wait time.Duration, dst []engine.ClientChunk) (engine.ClientWords, func())
	PostClientFrames(body []byte) error
}

// wordsBinMagic opens a /api/stream/words.bin reply.
var wordsBinMagic = [4]byte{'S', 'D', 'W', '1'}

// wordsBinHeader is the JSON header of a /api/stream/words.bin reply.
type wordsBinHeader struct {
	engine.ClientStreamInfo
	Next     uint64 `json:"next"`    // the seq to ask for next
	Dropped  uint64 `json:"dropped"` // chunks asked for that the ring no longer held
	Chunks   int    `json:"chunks"`
	Lag      uint64 `json:"lag"` // stream samples the scope holds without the page's frames
	ServerMS int64  `json:"serverMS"`
}

// wordsBufs are a reply's chunk list and head, and a post's body, reused:
// a page at the densest stream asks a few times a second and posts as often.
var wordsBufs = sync.Pool{New: func() any { return &wordsBuf{} }}

// wordsBuf is one request's reused memory.
type wordsBuf struct {
	chunks []engine.ClientChunk
	head   []byte
}

// framesBufs are posts' bodies, reused.
var framesBufs = sync.Pool{New: func() any { b := make([]byte, 0, 64<<10); return &b }}

// hStreamWords serves the digital stream's words to a page that decodes
// them: ?from=seq (the reply's next), ?max bytes, ?wait ms to park for words.
// Asking holds the client decode (the engine's lease); a reply with active
// false says the scope decodes, and why. The body after the header is the
// chunks' heads (engine.AppendClientChunkHeads), then their words as the
// engine codes them (appendWordsRLE), written straight from the engine's
// ring (pinned until written): no copy of the words on the scope but the
// socket's.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (s *Server) hStreamWords(w http.ResponseWriter, r *http.Request) {
	src, ok := s.sc.(clientDecoder)
	if !ok {
		http.Error(w, "no client decode", http.StatusNotFound)
		return
	}
	q := r.URL.Query()
	from, _ := strconv.ParseUint(q.Get("from"), 10, 64)
	maxB := 1 << 20
	if v, err := strconv.Atoi(q.Get("max")); err == nil && v > 0 {
		maxB = min(v, engine.ClientReplyMax)
	}
	wait := 0
	if v, err := strconv.Atoi(q.Get("wait")); err == nil && v > 0 {
		wait = min(v, 1500)
	}
	wb := wordsBufs.Get().(*wordsBuf)
	defer wordsBufs.Put(wb)
	cw, release := src.ReadClientWords(from, maxB, time.Duration(wait)*time.Millisecond, wb.chunks[:0])
	defer release()
	wb.chunks = cw.Chunks
	h := wordsBinHeader{ClientStreamInfo: cw.Info, Next: cw.Next, Dropped: cw.Dropped, Chunks: len(cw.Chunks), Lag: cw.Lag,
		ServerMS: time.Now().UnixMilli()}
	hj, err := json.Marshal(h)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// The header padded to whole words: the page reads the words in place.
	for len(hj)%4 != 0 {
		hj = append(hj, ' ')
	}
	head := append(wb.head[:0], wordsBinMagic[:]...)
	head = binary.LittleEndian.AppendUint32(head, uint32(len(hj)))
	head = append(head, hj...)
	head = engine.AppendClientChunkHeads(head, cw.Chunks)
	wb.head = head
	size := len(head)
	for _, c := range cw.Chunks {
		size += len(c.Enc)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(size))
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := w.Write(head); err != nil {
		return
	}
	for _, c := range cw.Chunks {
		if _, err := w.Write(c.Enc); err != nil {
			return
		}
	}
}

// framesBinMax bounds a /api/stream/frames.bin request.
const framesBinMax = 4 << 20

// hStreamFrames takes a page's decoded frames (engine PostClientFrames),
// read into a reused buffer: the engine queues them without keeping it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (s *Server) hStreamFrames(w http.ResponseWriter, r *http.Request) {
	src, ok := s.sc.(clientDecoder)
	if !ok {
		http.Error(w, "no client decode", http.StatusNotFound)
		return
	}
	bp := framesBufs.Get().(*[]byte)
	defer framesBufs.Put(bp)
	body, err := readBody(http.MaxBytesReader(w, r.Body, framesBinMax), (*bp)[:0], r.ContentLength)
	*bp = body[:0]
	if err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	if err := src.PostClientFrames(body); err != nil {
		code := http.StatusConflict
		if errors.Is(err, engine.ErrClientFramesBody) {
			code = http.StatusBadRequest
		}
		http.Error(w, err.Error(), code)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// readBody reads r to its end into dst (grown once to n when the length is
// known).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func readBody(r io.Reader, dst []byte, n int64) ([]byte, error) {
	if n > 0 && n <= framesBinMax {
		dst = slices.Grow(dst, int(n)+1)
	}
	for {
		if len(dst) == cap(dst) {
			dst = slices.Grow(dst, 64<<10)
		}
		k, err := r.Read(dst[len(dst):cap(dst)])
		dst = dst[:len(dst)+k]
		if err == io.EOF {
			return dst, nil
		}
		if err != nil {
			return dst, err
		}
	}
}
