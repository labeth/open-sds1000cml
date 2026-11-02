// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT

package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"
)

// serveTCP is the local/LAN fallback transport: one JSON request per line,
// one JSON response line back. It exists so the device is fully controllable
// even when NATS is unreachable (e.g. no outbound path from the lab), reached
// directly at OTA_LISTEN. Like every other worker it only calls Dispatch —
// never the GPMC bus.
// TRLC-LINKS: REQ-SDS-107
func (a *Agent) serveTCP() {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", a.cfg.TCPListen)
	if err != nil {
		a.log.Printf("tcp listen %s: %v", a.cfg.TCPListen, err)
		return
	}
	a.tcpMu.Lock()
	a.tcpAddr = ln.Addr().String()
	a.tcpMu.Unlock()
	a.log.Printf("tcp control listening on %s", a.cfg.TCPListen)
	for {
		select {
		case <-a.stopped:
			_ = ln.Close()
			return
		default:
		}
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-a.stopped:
				return
			default:
				time.Sleep(100 * time.Millisecond)
				continue
			}
		}
		go a.handleConn(conn)
	}
}

// TCPAddr reports the TCP control listener's bound address ("" until it is
// listening). With OTA_LISTEN=127.0.0.1:0 this is how a harness learns the
// ephemeral port; it records state only and changes no listener behavior.
// TRLC-LINKS: REQ-SDS-107
func (a *Agent) TCPAddr() string {
	a.tcpMu.Lock()
	defer a.tcpMu.Unlock()
	return a.tcpAddr
}

// maxRequestLine bounds one request line. otactl's largest request is a
// 128 KiB file chunk (about 175 KiB as base64); a line is otherwise buffered
// whole, so a peer that never sends a newline would grow it without limit.
const maxRequestLine = 8 << 20

// TRLC-LINKS: REQ-SDS-107
func (a *Agent) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }() // the reply was written or the peer is gone
	// Large deadline: file-transfer chunks and long exec can take a while.
	// otactl dials once per call, so the reader is sized for a request line
	// in a few fills rather than a whole chunk: ReadBytes grows past it, and a
	// 1 MiB buffer zeroed per connection cost more than the call itself.
	r := bufio.NewReaderSize(conn, 64<<10)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		line, err := readLine(r, maxRequestLine)
		if errors.Is(err, errLineTooLong) {
			resp, _ := json.Marshal(Response{OK: false, Err: err.Error()}) // a fixed struct encodes
			_ = conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
			_, _ = conn.Write(append(resp, '\n'))
			return
		}
		if len(line) > 0 {
			resp := a.DispatchJSON(line)
			_ = conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if _, werr := conn.Write(append(resp, '\n')); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// errLineTooLong is a request line over maxRequestLine.
var errLineTooLong = errors.New("request line over 8 MiB")

// readLine is ReadBytes('\n') that stops at max bytes.
// TRLC-LINKS: REQ-SDS-107
func readLine(r *bufio.Reader, maxLen int) ([]byte, error) {
	var line []byte
	for {
		frag, err := r.ReadSlice('\n')
		if len(line)+len(frag) > maxLen {
			return nil, errLineTooLong
		}
		line = append(line, frag...)
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, err
		}
	}
}
