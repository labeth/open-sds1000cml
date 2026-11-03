// Package vxi11 is a minimal VXI-11 (ONC RPC over TCP) client, sufficient to
// command the FACTORY firmware's SCPI interface (spec 11 §2): portmap GETPORT
// → DEVICE_CORE create_link/device_write/device_read/destroy_link. Used by
// the takeover to drive the factory app to STOP before the idle-confirm.
//
// The instrument serves a SINGLE link: always destroy_link, and tolerate an
// initial create_link failure from a stale link by retrying (spec 11 §2.3).
// ENGMODEL-OWNER-UNIT: FU-OTA-VXI11
package vxi11

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

const (
	pmapProg    = 100000
	pmapVers    = 2
	pmapGetport = 3

	coreProg = 0x0607AF // DEVICE_CORE
	coreVers = 1

	procCreateLink  = 10
	procDeviceWrite = 11
	procDeviceRead  = 12
	procDestroyLink = 23

	ipprotoTCP = 6

	flagEnd = 0x8 // device_write: assert EOI on last byte

	readReasonMask = 0x7 // END|CHR|REQCNT — non-zero means response complete

	// maxReply bounds a reply's fragments: a device_read asks for at most
	// 4 MiB, and a length off the wire is not an allocation size.
	maxReply = 8 << 20
	// maxAuthBody is the largest opaque_auth body ONC RPC allows (RFC 5531).
	maxAuthBody = 400
)

// TRLC-LINKS: REQ-SDS-100
type xdrBuf struct{ b []byte }

// TRLC-LINKS: REQ-SDS-100
func (x *xdrBuf) u32(v uint32) *xdrBuf {
	var t [4]byte
	binary.BigEndian.PutUint32(t[:], v)
	x.b = append(x.b, t[:]...)
	return x
}

// TRLC-LINKS: REQ-SDS-100
func (x *xdrBuf) opaque(p []byte) *xdrBuf {
	x.u32(uint32(len(p))) //nolint:gosec // an opaque here is a SCPI line or "inst0", far below 4 GiB
	x.b = append(x.b, p...)
	for len(x.b)%4 != 0 {
		x.b = append(x.b, 0)
	}
	return x
}

// maxStale is how many late replies to earlier calls a call reads past.
const maxStale = 8

// readRPCRecord reads one record-marked RPC message, concatenating its fragments.
// TRLC-LINKS: REQ-SDS-100
func readRPCRecord(conn net.Conn) ([]byte, error) {
	var rm [4]byte
	var reply []byte
	for {
		if _, err := io.ReadFull(conn, rm[:]); err != nil {
			return nil, fmt.Errorf("vxi11: read record mark: %w", err)
		}
		h := binary.BigEndian.Uint32(rm[:])
		size := int64(h & 0x7fffffff)
		if int64(len(reply))+size > maxReply {
			return nil, fmt.Errorf("vxi11: reply over %d bytes", maxReply)
		}
		at := len(reply) // each fragment read straight onto the reply
		reply = append(reply, make([]byte, size)...)
		if _, err := io.ReadFull(conn, reply[at:]); err != nil {
			return nil, fmt.Errorf("vxi11: read fragment: %w", err)
		}
		if h&0x80000000 != 0 {
			return reply, nil
		}
	}
}

// rpcCall sends one record-marked AUTH_NULL call and returns the result bytes
// (after the accepted-reply header) or an error.
// TRLC-LINKS: REQ-SDS-100
func rpcCall(conn net.Conn, timeout time.Duration, prog, vers, proc uint32, args []byte) ([]byte, error) {
	xid := uint32(time.Now().UnixNano()) //nolint:gosec // any 32 bits that differ per call make an xid; the clock's low bits do
	// The record mark is reserved at the front and filled in last: the call
	// is built and written as one buffer.
	msg := xdrBuf{b: make([]byte, 4, 4+40+len(args))}
	msg.u32(xid).u32(0 /*CALL*/).u32(2 /*rpcvers*/).u32(prog).u32(vers).u32(proc)
	msg.u32(0).u32(0) // cred AUTH_NULL
	msg.u32(0).u32(0) // verf AUTH_NULL
	msg.b = append(msg.b, args...)
	binary.BigEndian.PutUint32(msg.b[:4], 0x80000000|uint32(len(msg.b)-4)) //nolint:gosec // one call's record, a SCPI line long

	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(msg.b); err != nil {
		return nil, fmt.Errorf("vxi11: write: %w", err)
	}

	// A reply whose xid is not this call's answers an earlier call that timed
	// out: it is read past, so one slow answer does not misread every call
	// after it.
	var reply []byte
	for stale := 0; ; stale++ {
		r, err := readRPCRecord(conn)
		if err != nil {
			return nil, err
		}
		if len(r) < 24 {
			return nil, fmt.Errorf("vxi11: short reply (%d bytes)", len(r))
		}
		if binary.BigEndian.Uint32(r[0:4]) == xid {
			reply = r
			break
		}
		if stale >= maxStale {
			return nil, errors.New("vxi11: xid mismatch")
		}
	}
	if binary.BigEndian.Uint32(reply[4:8]) != 1 { // REPLY
		return nil, errors.New("vxi11: not a reply")
	}
	if binary.BigEndian.Uint32(reply[8:12]) != 0 { // MSG_ACCEPTED
		return nil, errors.New("vxi11: rpc denied")
	}
	// verf flavor+len at 12..20 (AUTH_NULL, len 0), accept_stat at 20..24.
	verfLen := binary.BigEndian.Uint32(reply[16:20])
	if verfLen > maxAuthBody {
		return nil, fmt.Errorf("vxi11: verifier of %d bytes", verfLen)
	}
	off := 20 + int(verfLen+3)/4*4
	if len(reply) < off+4 {
		return nil, errors.New("vxi11: truncated reply")
	}
	if st := binary.BigEndian.Uint32(reply[off : off+4]); st != 0 {
		return nil, fmt.Errorf("vxi11: accept_stat=%d", st)
	}
	return reply[off+4:], nil
}

// Client is one open DEVICE_CORE link.
// TRLC-LINKS: REQ-SDS-100
type Client struct {
	conn    net.Conn
	lid     uint32
	timeout time.Duration
}

// Dial resolves DEVICE_CORE through the portmapper on host:111 and creates the
// "inst0" link. create_link is retried a few times because a dropped previous
// connection can leave the single link stuck until it times out.
// TRLC-LINKS: REQ-SDS-100
func Dial(host string, timeout time.Duration) (*Client, error) {
	return DialAt(net.JoinHostPort(host, "111"), host, timeout)
}

// DialAt is Dial with an explicit portmapper address instead of the fixed
// host:111 — a seam for test harnesses, which cannot bind the privileged
// portmapper port. Dial(host, t) is exactly DialAt(host+":111", host, t).
// TRLC-LINKS: REQ-SDS-100
func DialAt(pmAddr, host string, timeout time.Duration) (*Client, error) {
	d := net.Dialer{Timeout: timeout}
	pm, err := d.DialContext(context.Background(), "tcp", pmAddr)
	if err != nil {
		return nil, fmt.Errorf("vxi11: portmap dial: %w", err)
	}
	var args xdrBuf
	args.u32(coreProg).u32(coreVers).u32(ipprotoTCP).u32(0)
	res, err := rpcCall(pm, timeout, pmapProg, pmapVers, pmapGetport, args.b)
	_ = pm.Close()
	if err != nil {
		return nil, fmt.Errorf("vxi11: GETPORT: %w", err)
	}
	if len(res) < 4 {
		return nil, errors.New("vxi11: GETPORT short result")
	}
	port := binary.BigEndian.Uint32(res[0:4])
	if port == 0 || port > 65535 {
		return nil, fmt.Errorf("vxi11: DEVICE_CORE not registered (port=%d)", port)
	}

	conn, err := d.DialContext(context.Background(), "tcp", net.JoinHostPort(host, strconv.FormatUint(uint64(port), 10)))
	if err != nil {
		return nil, fmt.Errorf("vxi11: core dial :%d: %w", port, err)
	}
	c := &Client{conn: conn, timeout: timeout}

	var lastErr error
	for range 3 {
		var cl xdrBuf
		cl.u32(1 /*clientId*/).u32(0 /*lockDevice=false*/).u32(0 /*lock_timeout*/)
		cl.opaque([]byte("inst0"))
		res, err := rpcCall(conn, timeout, coreProg, coreVers, procCreateLink, cl.b)
		if err != nil {
			lastErr = err
		} else if len(res) < 16 {
			lastErr = errors.New("vxi11: create_link short result")
		} else if e := binary.BigEndian.Uint32(res[0:4]); e != 0 {
			lastErr = fmt.Errorf("vxi11: create_link error %d (stale link?)", e)
		} else {
			c.lid = binary.BigEndian.Uint32(res[4:8])
			return c, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	_ = conn.Close()
	return nil, lastErr
}

// Send writes one SCPI line (newline appended if missing).
// TRLC-LINKS: REQ-SDS-100
func (c *Client) Send(cmd string) error {
	if len(cmd) == 0 || cmd[len(cmd)-1] != '\n' {
		cmd += "\n"
	}
	var args xdrBuf
	args.u32(c.lid).u32(5000 /*io_timeout ms*/).u32(0).u32(flagEnd)
	args.opaque([]byte(cmd))
	res, err := rpcCall(c.conn, c.timeout, coreProg, coreVers, procDeviceWrite, args.b)
	if err != nil {
		return err
	}
	if len(res) >= 4 {
		if e := binary.BigEndian.Uint32(res[0:4]); e != 0 {
			return fmt.Errorf("vxi11: device_write error %d", e)
		}
	}
	return nil
}

// Query sends a SCPI query and reads the response until END/termination.
// TRLC-LINKS: REQ-SDS-100
func (c *Client) Query(cmd string) (string, error) {
	if err := c.Send(cmd); err != nil {
		return "", err
	}
	var out []byte
	for range 64 { // bounded reassembly loop
		var args xdrBuf
		args.u32(c.lid).u32(0x400000 /*requestSize*/).u32(5000).u32(0).u32(0).u32(0)
		res, err := rpcCall(c.conn, c.timeout, coreProg, coreVers, procDeviceRead, args.b)
		if err != nil {
			return string(out), err
		}
		if len(res) < 12 {
			return string(out), errors.New("vxi11: device_read short result")
		}
		e := binary.BigEndian.Uint32(res[0:4])
		reason := binary.BigEndian.Uint32(res[4:8])
		data := res[12:]
		// The count is clamped to the bytes that came, compared unsigned: as
		// an int, a count over 2^31 is negative on the 32-bit device.
		if n := binary.BigEndian.Uint32(res[8:12]); uint64(n) < uint64(len(data)) {
			data = data[:n]
		}
		out = append(out, data...)
		if e != 0 {
			return string(out), fmt.Errorf("vxi11: device_read error %d", e)
		}
		if reason&readReasonMask != 0 {
			break
		}
	}
	return string(out), nil
}

// Close destroys the link and closes the connection. Always call it: the
// instrument serves a single link.
// TRLC-LINKS: REQ-SDS-100
func (c *Client) Close() {
	var args xdrBuf
	args.u32(c.lid)
	_, _ = rpcCall(c.conn, c.timeout, coreProg, coreVers, procDestroyLink, args.b)
	_ = c.conn.Close()
}
