// Package vxi11srv is the LAN instrument-control transport (spec 11 §1): an
// ONC-RPC DEVICE_CORE server on an ephemeral TCP port, registered with the
// system portmapper on :111. It is the ONLY LAN SCPI path — no raw :5025
// socket exists in this contract. The server never touches the GPMC bus;
// every SCPI line goes to the injected handler (staging setters/snapshots).
package vxi11srv

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const (
	progPortmap = 100000
	progCore    = 0x0607AF

	pmapProcSet   = 1
	pmapProcUnset = 2

	procCreateLink   = 10
	procDeviceWrite  = 11
	procDeviceRead   = 12
	procDeviceReadSt = 13
	procDeviceTrig   = 14
	procDeviceClear  = 15
	procDeviceRemote = 16
	procDeviceLocal  = 17
	procDeviceLock   = 18
	procDeviceUnlock = 19
	procEnableSRQ    = 20
	procDestroyLink  = 23

	errOK      = 0
	errInvLink = 4
	errLocked  = 11
	errTimeout = 15

	reasonEND = 0x4

	maxRecvSize = 0x800000
)

// Handler executes one complete SCPI line and returns the reply bytes.
type Handler func(line []byte) []byte

type Server struct {
	h    Handler
	logf func(string, ...any)
	ln   net.Listener

	mu        sync.Mutex
	linkOwner *conn
	nextLID   uint32
}

type conn struct {
	c       net.Conn
	srv     *Server
	lid     uint32
	lineBuf []byte
	pending []byte
}

// Start listens on an ephemeral TCP port, optionally registers with the
// portmapper (register=false for tests), and serves in the background.
func Start(h Handler, register bool, logf func(string, ...any)) (*Server, int, error) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	s := &Server{h: h, logf: logf, ln: ln, nextLID: 1}
	if register {
		if err := registerPortmap(port, logf); err != nil {
			logf("vxi11: portmap registration failed: %v (clients cannot GETPORT)", err)
		}
	}
	go s.acceptLoop()
	return s, port, nil
}

func (s *Server) acceptLoop() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go (&conn{c: c, srv: s}).serve()
	}
}

// ---- record marking (spec 11 §1.3) ----

// maxRecord bounds a reassembled RPC message so a hostile or corrupt
// record-mark cannot make the server allocate gigabytes (client writes are
// capped at maxRecvSize; the header + payload fit comfortably below this).
const maxRecord = maxRecvSize + 0x10000

func readRecord(r io.Reader) ([]byte, error) {
	var msg []byte
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return nil, err
		}
		mark := binary.BigEndian.Uint32(hdr[:])
		n := int(mark & 0x7fffffff)
		if len(msg)+n > maxRecord {
			return nil, fmt.Errorf("vxi11: record too large (%d bytes)", len(msg)+n)
		}
		frag := make([]byte, n)
		if _, err := io.ReadFull(r, frag); err != nil {
			return nil, err
		}
		msg = append(msg, frag...)
		if mark&0x80000000 != 0 {
			return msg, nil
		}
	}
}

func writeRecord(w io.Writer, msg []byte) error {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], 0x80000000|uint32(len(msg)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(msg)
	return err
}

// ---- XDR ----

type xdr struct {
	b   []byte
	off int
	err bool
}

func (x *xdr) u32() uint32 {
	if x.off+4 > len(x.b) {
		x.err = true
		return 0
	}
	v := binary.BigEndian.Uint32(x.b[x.off:])
	x.off += 4
	return v
}

func (x *xdr) opaque() []byte {
	n := int(x.u32())
	// A declared length ≥ 0x80000000 is negative once cast to int on 32-bit
	// ARM; guard n < 0 as well as overflow so it can't bypass the bound.
	if x.err || n < 0 || x.off+n > len(x.b) {
		x.err = true
		return nil
	}
	v := x.b[x.off : x.off+n]
	x.off += (n + 3) &^ 3
	return v
}

func (x *xdr) skipAuth() {
	x.u32() // flavor
	n := int(x.u32())
	x.off += (n + 3) &^ 3
}

func putU32(b []byte, v uint32) []byte {
	var w [4]byte
	binary.BigEndian.PutUint32(w[:], v)
	return append(b, w[:]...)
}

func putOpaque(b, data []byte) []byte {
	b = putU32(b, uint32(len(data)))
	b = append(b, data...)
	for pad := (4 - len(data)%4) % 4; pad > 0; pad-- {
		b = append(b, 0)
	}
	return b
}

// ---- connection ----

func (cn *conn) serve() {
	defer func() {
		cn.srv.release(cn)
		cn.c.Close()
	}()
	for {
		msg, err := readRecord(cn.c)
		if err != nil {
			return
		}
		x := &xdr{b: msg}
		xid := x.u32()
		if x.u32() != 0 { // msg_type CALL
			continue
		}
		x.u32() // rpc_vers
		prog := x.u32()
		x.u32() // vers
		proc := x.u32()
		x.skipAuth()
		x.skipAuth()

		var results []byte
		if prog == progCore {
			results = cn.dispatch(proc, x)
		}
		reply := putU32(nil, xid)
		reply = putU32(reply, 1) // REPLY
		reply = putU32(reply, 0) // MSG_ACCEPTED
		reply = putU32(reply, 0) // verf AUTH_NULL
		reply = putU32(reply, 0)
		if results == nil {
			reply = putU32(reply, 3) // PROC_UNAVAIL
		} else {
			reply = putU32(reply, 0) // SUCCESS
			reply = append(reply, results...)
		}
		if err := writeRecord(cn.c, reply); err != nil {
			return
		}
	}
}

// release frees the single link when its TCP connection dies — a dropped
// client must never wedge the interface (spec 11 §6 trap 1).
func (s *Server) release(cn *conn) {
	s.mu.Lock()
	if s.linkOwner == cn {
		s.linkOwner = nil
	}
	s.mu.Unlock()
}

func (cn *conn) dispatch(proc uint32, x *xdr) []byte {
	s := cn.srv
	switch proc {
	case procCreateLink:
		x.u32() // clientId
		x.u32() // lockDevice
		x.u32() // lock_timeout
		x.opaque()
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.linkOwner != nil && s.linkOwner != cn {
			r := putU32(nil, errLocked)
			r = putU32(r, 0)
			r = putU32(r, 0)
			return putU32(r, maxRecvSize)
		}
		s.nextLID++
		cn.lid = s.nextLID
		cn.pending = nil
		cn.lineBuf = nil
		s.linkOwner = cn
		r := putU32(nil, errOK)
		r = putU32(r, cn.lid)
		r = putU32(r, 0) // abortPort: no DEVICE_ASYNC in v1
		return putU32(r, maxRecvSize)

	case procDeviceWrite:
		lid := x.u32()
		x.u32() // io_timeout
		x.u32() // lock_timeout
		x.u32() // flags (END)
		data := x.opaque()
		if !cn.owns(lid) || x.err {
			r := putU32(nil, errInvLink)
			return putU32(r, 0)
		}
		cn.lineBuf = append(cn.lineBuf, data...)
		for {
			i := indexByte(cn.lineBuf, '\n')
			if i < 0 {
				break
			}
			line := cn.lineBuf[:i+1]
			cn.pending = append(cn.pending, s.h(line)...)
			cn.lineBuf = cn.lineBuf[i+1:]
		}
		r := putU32(nil, errOK)
		return putU32(r, uint32(len(data)))

	case procDeviceRead:
		lid := x.u32()
		reqSize := int(x.u32())
		ioMs := x.u32()
		if ioMs > 10000 {
			ioMs = 10000 // clamp a client-supplied timeout so a huge value
		} //              can't pin the conn goroutine indefinitely
		ioTimeout := time.Duration(ioMs) * time.Millisecond
		if !cn.owns(lid) {
			r := putU32(nil, errInvLink)
			r = putU32(r, 0)
			return putOpaque(r, nil)
		}
		if len(cn.pending) == 0 {
			// Nothing pending: block up to io_timeout, then error 15.
			deadline := time.Now().Add(ioTimeout)
			for len(cn.pending) == 0 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if len(cn.pending) == 0 {
				r := putU32(nil, errTimeout)
				r = putU32(r, 0)
				return putOpaque(r, nil)
			}
		}
		n := len(cn.pending)
		if n > reqSize {
			n = reqSize
		}
		chunk := cn.pending[:n]
		cn.pending = cn.pending[n:]
		reason := uint32(0) // 0 = more remains, client loops
		if len(cn.pending) == 0 {
			reason = reasonEND
		}
		r := putU32(nil, errOK)
		r = putU32(r, reason)
		return putOpaque(r, chunk)

	case procDeviceClear:
		// Resynchronize: flush pending response and line assembly.
		cn.pending = nil
		cn.lineBuf = nil
		return putU32(nil, errOK)

	case procDeviceReadSt:
		r := putU32(nil, errOK)
		return putU32(r, 0) // STB = 0

	case procDeviceTrig, procDeviceRemote, procDeviceLocal,
		procDeviceLock, procDeviceUnlock, procEnableSRQ:
		return putU32(nil, errOK)

	case procDestroyLink:
		lid := x.u32()
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.linkOwner != cn || cn.lid != lid || lid == 0 {
			return putU32(nil, errInvLink) // don't drop a live link on a bad id
		}
		s.linkOwner = nil
		cn.lid = 0
		return putU32(nil, errOK)
	}
	return nil
}

func (cn *conn) owns(lid uint32) bool {
	cn.srv.mu.Lock()
	defer cn.srv.mu.Unlock()
	return cn.srv.linkOwner == cn && cn.lid == lid && lid != 0
}

func indexByte(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}

// ---- portmap registration (spec 11 §1.2) ----

// registerPortmap UNSETs any stale DEVICE_CORE mapping and SETs ours. The
// classic portmapper rejects SET/UNSET from unprivileged source ports, so
// dial from a local port <1024 (the app runs as root on the device).
func registerPortmap(port int, logf func(string, ...any)) error {
	call := func(proc uint32, prog, vers, prot, prt uint32) error {
		var c net.Conn
		var err error
		for lp := 1023; lp >= 600; lp-- {
			d := net.Dialer{
				LocalAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: lp},
				Timeout:   2 * time.Second,
			}
			c, err = d.Dial("tcp", "127.0.0.1:111")
			if err == nil {
				break
			}
		}
		if err != nil {
			return fmt.Errorf("privileged dial: %w", err)
		}
		defer c.Close()
		msg := putU32(nil, uint32(time.Now().UnixNano()))
		msg = putU32(msg, 0) // CALL
		msg = putU32(msg, 2) // rpc v2
		msg = putU32(msg, progPortmap)
		msg = putU32(msg, 2)
		msg = putU32(msg, proc)
		msg = putU32(msg, 0)
		msg = putU32(msg, 0)
		msg = putU32(msg, 0)
		msg = putU32(msg, 0)
		msg = putU32(msg, prog)
		msg = putU32(msg, vers)
		msg = putU32(msg, prot)
		msg = putU32(msg, prt)
		if err := writeRecord(c, msg); err != nil {
			return err
		}
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, err = readRecord(c)
		return err
	}
	if err := call(pmapProcUnset, progCore, 1, 6, 0); err != nil {
		logf("vxi11: portmap UNSET: %v", err)
	}
	if err := call(pmapProcSet, progCore, 1, 6, uint32(port)); err != nil {
		return err
	}
	logf("vxi11: DEVICE_CORE registered on tcp/%d", port)
	return nil
}
