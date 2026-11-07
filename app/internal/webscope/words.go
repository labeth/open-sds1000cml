// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"errors"
	"sync"
	"time"

	"github.com/labeth/open-dso-core/webserver"
	"github.com/labeth/open-dso-core/webserver/wire"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// The client decode (ADR-STREAM-CLIENT-DECODE): a page's web worker reads
// the digital stream's words as the engine's ring holds them, decodes them
// and posts the frames back, which the engine queues as its own.

// clientDecoder is the engine's client decode (engine/soft_client.go).
type clientDecoder interface {
	ReadClientWords(seq uint64, limit int, wait time.Duration, dst []engine.ClientChunk) (engine.ClientWords, func())
	PostClientFrames(body []byte) error
}

// ClientDecode is the engine's client decode.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (s *Scope) ClientDecode() (webserver.ClientDecode, bool) {
	cd, ok := s.sc.(clientDecoder)
	return clientDecode{cd}, ok
}

// clientDecode hands the engine's chunks to the server as wire chunks.
type clientDecode struct{ cd clientDecoder }

// engineChunks are reads' engine chunk lists, reused: a page at the densest
// stream asks a few times a second.
var engineChunks = sync.Pool{New: func() any { return new([]engine.ClientChunk) }}

// ReadWords reads the ring's chunks from seq; their words stay the
// engine's ring's (pinned until release), written straight to the socket.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (c clientDecode) ReadWords(seq uint64, limit int, wait time.Duration, dst []wire.WordChunk) (wire.Words, func()) {
	ec, ok := engineChunks.Get().(*[]engine.ClientChunk)
	if !ok {
		ec = new([]engine.ClientChunk)
	}
	cw, release := c.cd.ReadClientWords(seq, limit, wait, (*ec)[:0])
	*ec = cw.Chunks
	for _, ch := range cw.Chunks {
		dst = append(dst, wire.WordChunk{Index: ch.Index, Words: ch.Words, Break: ch.Break, Check: ch.Check, Enc: ch.Enc})
	}
	return wire.Words{Info: cw.Info, Chunks: dst, Dropped: cw.Dropped, Next: cw.Next, Lag: cw.Lag}, func() {
		release()
		clear(*ec)
		engineChunks.Put(ec)
	}
}

// ReplyMax bounds a read (the engine's tap).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (c clientDecode) ReplyMax() int { return engine.ClientReplyMax }

// PostFrames queues a page's frames; a body that is not frames is the
// request's fault.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (c clientDecode) PostFrames(body []byte) error {
	err := c.cd.PostClientFrames(body)
	if errors.Is(err, engine.ErrClientFramesBody) {
		return webserver.BadRequest{Err: err}
	}
	return err
}
