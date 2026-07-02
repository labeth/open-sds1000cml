package vxi11srv

import (
	"encoding/binary"
	"fmt"
	"net"
	"testing"
	"time"
)

// testClient speaks the same framing as ota/internal/vxi11 (the first
// compatibility target).
type testClient struct {
	c   net.Conn
	xid uint32
}

func dial(t *testing.T, port int) *testClient {
	t.Helper()
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return &testClient{c: c, xid: 100}
}

func (tc *testClient) call(t *testing.T, proc uint32, args []byte) []byte {
	t.Helper()
	tc.xid++
	msg := putU32(nil, tc.xid)
	msg = putU32(msg, 0)
	msg = putU32(msg, 2)
	msg = putU32(msg, progCore)
	msg = putU32(msg, 1)
	msg = putU32(msg, proc)
	msg = putU32(msg, 0)
	msg = putU32(msg, 0)
	msg = putU32(msg, 0)
	msg = putU32(msg, 0)
	msg = append(msg, args...)
	if err := writeRecord(tc.c, msg); err != nil {
		t.Fatal(err)
	}
	tc.c.SetReadDeadline(time.Now().Add(3 * time.Second))
	rep, err := readRecord(tc.c)
	if err != nil {
		t.Fatal(err)
	}
	// xid, REPLY, MSG_ACCEPTED, verf(2), accept_stat
	if binary.BigEndian.Uint32(rep[0:]) != tc.xid {
		t.Fatal("xid mismatch")
	}
	if binary.BigEndian.Uint32(rep[20:]) != 0 {
		t.Fatalf("accept_stat = %d", binary.BigEndian.Uint32(rep[20:]))
	}
	return rep[24:]
}

func (tc *testClient) createLink(t *testing.T) (uint32, uint32) {
	args := putU32(nil, 1)
	args = putU32(args, 0)
	args = putU32(args, 0)
	args = putOpaque(args, []byte("inst0"))
	r := tc.call(t, procCreateLink, args)
	return binary.BigEndian.Uint32(r[0:]), binary.BigEndian.Uint32(r[4:])
}

func (tc *testClient) write(t *testing.T, lid uint32, s string) {
	args := putU32(nil, lid)
	args = putU32(args, 5000)
	args = putU32(args, 0)
	args = putU32(args, 8)
	args = putOpaque(args, []byte(s))
	r := tc.call(t, procDeviceWrite, args)
	if binary.BigEndian.Uint32(r[0:]) != 0 {
		t.Fatalf("write error %d", binary.BigEndian.Uint32(r[0:]))
	}
}

func (tc *testClient) read(t *testing.T, lid uint32, reqSize uint32) (uint32, uint32, []byte) {
	args := putU32(nil, lid)
	args = putU32(args, reqSize)
	args = putU32(args, 2000)
	args = putU32(args, 0)
	args = putU32(args, 0)
	args = putU32(args, 0)
	r := tc.call(t, procDeviceRead, args)
	errc := binary.BigEndian.Uint32(r[0:])
	reason := binary.BigEndian.Uint32(r[4:])
	n := binary.BigEndian.Uint32(r[8:])
	return errc, reason, r[12 : 12+n]
}

func startTest(t *testing.T) (*Server, int) {
	t.Helper()
	h := func(line []byte) []byte {
		if string(line) == "*IDN?\n" {
			return []byte("Siglent,TEST,0,0\n")
		}
		if string(line) == "BIG?\n" {
			out := make([]byte, 1000)
			for i := range out {
				out[i] = byte(i)
			}
			return out
		}
		return nil
	}
	s, port, err := Start(h, false, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.ln.Close() })
	return s, port
}

func TestLinkWriteRead(t *testing.T) {
	_, port := startTest(t)
	tc := dial(t, port)
	defer tc.c.Close()

	errc, lid := tc.createLink(t)
	if errc != 0 || lid == 0 {
		t.Fatalf("create_link: err=%d lid=%d", errc, lid)
	}
	tc.write(t, lid, "*IDN?\n")
	errc, reason, data := tc.read(t, lid, 0x400000)
	if errc != 0 || reason != reasonEND || string(data) != "Siglent,TEST,0,0\n" {
		t.Fatalf("read: err=%d reason=%d data=%q", errc, reason, data)
	}
}

func TestChunkedRead(t *testing.T) {
	_, port := startTest(t)
	tc := dial(t, port)
	defer tc.c.Close()
	_, lid := tc.createLink(t)
	tc.write(t, lid, "BIG?\n")

	var got []byte
	for i := 0; i < 64; i++ {
		errc, reason, data := tc.read(t, lid, 256)
		if errc != 0 {
			t.Fatalf("chunk err %d", errc)
		}
		got = append(got, data...)
		if reason&0x7 != 0 {
			if reason != reasonEND {
				t.Fatalf("final reason = %d", reason)
			}
			break
		}
	}
	if len(got) != 1000 {
		t.Fatalf("reassembled %d bytes, want 1000", len(got))
	}
}

func TestSingleLinkAndRelease(t *testing.T) {
	_, port := startTest(t)
	tc1 := dial(t, port)
	_, lid1 := tc1.createLink(t)
	if lid1 == 0 {
		t.Fatal("first link failed")
	}

	// Second connection: create_link must fail with error 11.
	tc2 := dial(t, port)
	defer tc2.c.Close()
	errc, _ := tc2.createLink(t)
	if errc != errLocked {
		t.Fatalf("second link err = %d, want %d", errc, errLocked)
	}

	// Dropping the first TCP connection must free the link (a dead client
	// must never wedge the interface).
	tc1.c.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		errc, lid2 := tc2.createLink(t)
		if errc == 0 && lid2 != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("link not released after TCP close")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestReadTimeout(t *testing.T) {
	_, port := startTest(t)
	tc := dial(t, port)
	defer tc.c.Close()
	_, lid := tc.createLink(t)
	// Nothing pending (setter line produced no reply) → error 15 after
	// io_timeout.
	tc.write(t, lid, "SET SOMETHING\n")
	errc, _, _ := tc.read(t, lid, 1024)
	if errc != errTimeout {
		t.Fatalf("empty read err = %d, want %d", errc, errTimeout)
	}
}
