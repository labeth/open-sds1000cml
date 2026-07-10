package agent

import (
	"bufio"
	"net"
	"time"
)

// serveTCP is the local/LAN fallback transport: one JSON request per line,
// one JSON response line back. It exists so the device is fully controllable
// even when NATS is unreachable (e.g. no outbound path from the lab), reached
// directly at OTA_LISTEN. Like every other worker it only calls Dispatch —
// never the GPMC bus.
func (a *Agent) serveTCP() {
	ln, err := net.Listen("tcp", a.cfg.TCPListen)
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
func (a *Agent) TCPAddr() string {
	a.tcpMu.Lock()
	defer a.tcpMu.Unlock()
	return a.tcpAddr
}

func (a *Agent) handleConn(conn net.Conn) {
	defer conn.Close()
	// Large deadline: file-transfer chunks and long exec can take a while.
	r := bufio.NewReaderSize(conn, 1<<20)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		line, err := r.ReadBytes('\n')
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
