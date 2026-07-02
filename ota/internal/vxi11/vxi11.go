// Package vxi11 is a minimal VXI-11 (ONC RPC over TCP) client, sufficient to
// command the FACTORY firmware's SCPI interface (spec 11 §2): portmap GETPORT
// → DEVICE_CORE create_link/device_write/device_read/destroy_link. Used by
// the takeover to drive the factory app to STOP before the idle-confirm.
//
// The instrument serves a SINGLE link: always destroy_link, and tolerate an
// initial create_link failure from a stale link by retrying (spec 11 §2.3).
package vxi11

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
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
)

type xdrBuf struct{ b []byte }

func (x *xdrBuf) u32(v uint32) *xdrBuf {
	var t [4]byte
	binary.BigEndian.PutUint32(t[:], v)
	x.b = append(x.b, t[:]...)
	return x
}

func (x *xdrBuf) opaque(p []byte) *xdrBuf {
	x.u32(uint32(len(p)))
	x.b = append(x.b, p...)
	for len(x.b)%4 != 0 {
		x.b = append(x.b, 0)
	}
	return x
}

// rpcCall sends one record-marked AUTH_NULL call and returns the result bytes
// (after the accepted-reply header) or an error.
func rpcCall(conn net.Conn, timeout time.Duration, prog, vers, proc uint32, args []byte) ([]byte, error) {
	xid := uint32(time.Now().UnixNano())
	var msg xdrBuf
	msg.u32(xid).u32(0 /*CALL*/).u32(2 /*rpcvers*/).u32(prog).u32(vers).u32(proc)
	msg.u32(0).u32(0) // cred AUTH_NULL
	msg.u32(0).u32(0) // verf AUTH_NULL
	msg.b = append(msg.b, args...)

	_ = conn.SetDeadline(time.Now().Add(timeout))
	var rm [4]byte
	binary.BigEndian.PutUint32(rm[:], 0x80000000|uint32(len(msg.b)))
	if _, err := conn.Write(append(rm[:], msg.b...)); err != nil {
		return nil, fmt.Errorf("vxi11: write: %w", err)
	}

	// Read record-marked reply, concatenating fragments.
	var reply []byte
	for {
		if _, err := io.ReadFull(conn, rm[:]); err != nil {
			return nil, fmt.Errorf("vxi11: read record mark: %w", err)
		}
		h := binary.BigEndian.Uint32(rm[:])
		frag := make([]byte, h&0x7fffffff)
		if _, err := io.ReadFull(conn, frag); err != nil {
			return nil, fmt.Errorf("vxi11: read fragment: %w", err)
		}
		reply = append(reply, frag...)
		if h&0x80000000 != 0 {
			break
		}
	}
	if len(reply) < 24 {
		return nil, fmt.Errorf("vxi11: short reply (%d bytes)", len(reply))
	}
	if got := binary.BigEndian.Uint32(reply[0:4]); got != xid {
		return nil, fmt.Errorf("vxi11: xid mismatch")
	}
	if binary.BigEndian.Uint32(reply[4:8]) != 1 { // REPLY
		return nil, fmt.Errorf("vxi11: not a reply")
	}
	if binary.BigEndian.Uint32(reply[8:12]) != 0 { // MSG_ACCEPTED
		return nil, fmt.Errorf("vxi11: rpc denied")
	}
	// verf flavor+len at 12..20 (AUTH_NULL, len 0), accept_stat at 20..24.
	verfLen := binary.BigEndian.Uint32(reply[16:20])
	off := 20 + int(verfLen+3)/4*4
	if len(reply) < off+4 {
		return nil, fmt.Errorf("vxi11: truncated reply")
	}
	if st := binary.BigEndian.Uint32(reply[off : off+4]); st != 0 {
		return nil, fmt.Errorf("vxi11: accept_stat=%d", st)
	}
	return reply[off+4:], nil
}

// Client is one open DEVICE_CORE link.
type Client struct {
	conn    net.Conn
	lid     uint32
	timeout time.Duration
}

// Dial resolves DEVICE_CORE through the portmapper on host:111 and creates the
// "inst0" link. create_link is retried a few times because a dropped previous
// connection can leave the single link stuck until it times out.
func Dial(host string, timeout time.Duration) (*Client, error) {
	return dialAt(net.JoinHostPort(host, "111"), host, timeout)
}

// dialAt is Dial with an explicit portmapper address (test seam).
func dialAt(pmAddr, host string, timeout time.Duration) (*Client, error) {
	pm, err := net.DialTimeout("tcp", pmAddr, timeout)
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
		return nil, fmt.Errorf("vxi11: GETPORT short result")
	}
	port := binary.BigEndian.Uint32(res[0:4])
	if port == 0 || port > 65535 {
		return nil, fmt.Errorf("vxi11: DEVICE_CORE not registered (port=%d)", port)
	}

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), timeout)
	if err != nil {
		return nil, fmt.Errorf("vxi11: core dial :%d: %w", port, err)
	}
	c := &Client{conn: conn, timeout: timeout}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		var cl xdrBuf
		cl.u32(1 /*clientId*/).u32(0 /*lockDevice=false*/).u32(0 /*lock_timeout*/)
		cl.opaque([]byte("inst0"))
		res, err := rpcCall(conn, timeout, coreProg, coreVers, procCreateLink, cl.b)
		if err != nil {
			lastErr = err
		} else if len(res) < 16 {
			lastErr = fmt.Errorf("vxi11: create_link short result")
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
func (c *Client) Query(cmd string) (string, error) {
	if err := c.Send(cmd); err != nil {
		return "", err
	}
	var out []byte
	for i := 0; i < 64; i++ { // bounded reassembly loop
		var args xdrBuf
		args.u32(c.lid).u32(0x400000 /*requestSize*/).u32(5000).u32(0).u32(0).u32(0)
		res, err := rpcCall(c.conn, c.timeout, coreProg, coreVers, procDeviceRead, args.b)
		if err != nil {
			return string(out), err
		}
		if len(res) < 12 {
			return string(out), fmt.Errorf("vxi11: device_read short result")
		}
		e := binary.BigEndian.Uint32(res[0:4])
		reason := binary.BigEndian.Uint32(res[4:8])
		n := binary.BigEndian.Uint32(res[8:12])
		if int(n) > len(res)-12 {
			n = uint32(len(res) - 12)
		}
		out = append(out, res[12:12+n]...)
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
func (c *Client) Close() {
	var args xdrBuf
	args.u32(c.lid)
	_, _ = rpcCall(c.conn, c.timeout, coreProg, coreVers, procDestroyLink, args.b)
	_ = c.conn.Close()
}
