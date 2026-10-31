// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
)

// The client decode's transport (ADR-STREAM-CLIENT-DECODE): a page's web
// worker (webdec_worker.js) long-polls /api/stream/words.bin for the digital
// stream's words as the scope's ring holds them, decodes them, and posts the
// frames to /api/stream/frames.bin, which the scope queues as its own.

// clientDecoder is the engine's client decode (engine/soft_client.go).
type clientDecoder interface {
	ReadClientWords(seq uint64, max int, wait time.Duration, dst []byte) (engine.ClientWords, []byte)
	PostClientFrames(session uint32, through uint64, frames []decode.StreamFrame) error
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

// wordsBufs are reply buffers, reused: a page at the densest stream reads
// about 4 MB a second.
var wordsBufs = sync.Pool{New: func() any { b := make([]byte, 0, 256<<10); return &b }}

// hStreamWords serves the digital stream's words to a page that decodes
// them: ?from=seq (the reply's next), ?max bytes, ?wait ms to park for words.
// Asking holds the client decode (the engine's lease); a reply with active
// false says the scope decodes, and why. The body after the header is the
// chunks' heads (engine.AppendClientChunkHeads), then their words as the
// engine codes them (appendWordsRLE).
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
		maxB = min(v, 4<<20)
	}
	wait := 0
	if v, err := strconv.Atoi(q.Get("wait")); err == nil && v > 0 {
		wait = min(v, 1500)
	}
	bp := wordsBufs.Get().(*[]byte)
	defer wordsBufs.Put(bp)
	cw, enc := src.ReadClientWords(from, maxB, time.Duration(wait)*time.Millisecond, (*bp)[:0])
	*bp = enc[:0]
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
	head := make([]byte, 8, 8+len(hj)+engine.ClientChunkHead*len(cw.Chunks))
	copy(head, wordsBinMagic[:])
	binary.LittleEndian.PutUint32(head[4:], uint32(len(hj)))
	head = append(head, hj...)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Second))
	head = engine.AppendClientChunkHeads(head, cw.Chunks)
	// The chunks' headers first, then their words in one piece (as read).
	w.Header().Set("Content-Length", strconv.Itoa(len(head)+len(enc)))
	if _, err := w.Write(head); err == nil {
		_, _ = w.Write(enc)
	}
}

// framesBinMax bounds a /api/stream/frames.bin request.
const framesBinMax = 4 << 20

// hStreamFrames takes a page's decoded frames (engine.ParseClientFrames).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (s *Server) hStreamFrames(w http.ResponseWriter, r *http.Request) {
	src, ok := s.sc.(clientDecoder)
	if !ok {
		http.Error(w, "no client decode", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, framesBinMax))
	if err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	session, through, frames, err := engine.ParseClientFrames(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := src.PostClientFrames(session, through, frames); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
