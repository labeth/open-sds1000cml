// ENGMODEL-OWNER-UNIT: FU-OTA-VXI11

package vxi11

import (
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// fakeInstrument is a minimal VXI-11 server: a portmapper on the listener that
// GETPORTs to a second core listener, which answers create_link / device_write
// / device_read / destroy_link. It records the SCPI commands it receives.
// TRLC-LINKS: REQ-SDS-100
type fakeInstrument struct {
	pmLn, coreLn net.Listener
	corePort     uint32
	received     chan string
	readReply    string
	// readCount, when set, is the byte count device_read claims instead of
	// the reply's length.
	readCount uint32
}

// TRLC-LINKS: REQ-SDS-100
func readRecord(c net.Conn) ([]byte, error) {
	var rm [4]byte
	if _, err := io.ReadFull(c, rm[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(rm[:]) & 0x7fffffff
	buf := make([]byte, n)
	_, err := io.ReadFull(c, buf)
	return buf, err
}

// TRLC-LINKS: REQ-SDS-100
func writeRecord(c net.Conn, body []byte) {
	var rm [4]byte
	binary.BigEndian.PutUint32(rm[:], 0x80000000|uint32(len(body)))
	_, _ = c.Write(append(rm[:], body...))
}

// acceptedReply builds an accepted RPC reply header for xid + result payload.
// TRLC-LINKS: REQ-SDS-100
func acceptedReply(xid uint32, result []byte) []byte {
	b := make([]byte, 24)
	binary.BigEndian.PutUint32(b[0:], xid)
	binary.BigEndian.PutUint32(b[4:], 1)  // REPLY
	binary.BigEndian.PutUint32(b[8:], 0)  // MSG_ACCEPTED
	binary.BigEndian.PutUint32(b[12:], 0) // verf flavor
	binary.BigEndian.PutUint32(b[16:], 0) // verf len
	binary.BigEndian.PutUint32(b[20:], 0) // accept_stat SUCCESS
	return append(b, result...)
}

// TRLC-LINKS: REQ-SDS-100
func callXID(body []byte) uint32 { return binary.BigEndian.Uint32(body[0:4]) }

// TRLC-LINKS: REQ-SDS-100
func callProc(body []byte) uint32 { return binary.BigEndian.Uint32(body[20:24]) }

// TRLC-LINKS: REQ-SDS-100
func newFake(t *testing.T) *fakeInstrument {
	t.Helper()
	pm, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	core, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeInstrument{
		pmLn: pm, coreLn: core,
		corePort:  uint32(netip.MustParseAddrPort(core.Addr().String()).Port()),
		received:  make(chan string, 8),
		readReply: "",
	}
	go f.servePortmap()
	go f.serveCore()
	return f
}

// TRLC-LINKS: REQ-SDS-100
func (f *fakeInstrument) host() string { return "127.0.0.1" }

// TRLC-LINKS: REQ-SDS-100
func (f *fakeInstrument) pmPort() int {
	return int(netip.MustParseAddrPort(f.pmLn.Addr().String()).Port())
}

// TRLC-LINKS: REQ-SDS-100
func (f *fakeInstrument) servePortmap() {
	for {
		c, err := f.pmLn.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer func() { _ = c.Close() }()
			for {
				body, err := readRecord(c)
				if err != nil {
					return
				}
				// GETPORT returns the core port.
				res := make([]byte, 4)
				binary.BigEndian.PutUint32(res, f.corePort)
				writeRecord(c, acceptedReply(callXID(body), res))
			}
		}(c)
	}
}

// TRLC-LINKS: REQ-SDS-100
func (f *fakeInstrument) serveCore() {
	for {
		c, err := f.coreLn.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer func() { _ = c.Close() }()
			for {
				body, err := readRecord(c)
				if err != nil {
					return
				}
				xid := callXID(body)
				switch callProc(body) {
				case procCreateLink:
					res := make([]byte, 16) // error=0, lid=1, abortPort, maxRecvSize
					binary.BigEndian.PutUint32(res[4:], 1)
					binary.BigEndian.PutUint32(res[12:], 0x800000)
					writeRecord(c, acceptedReply(xid, res))
				case procDeviceWrite:
					// args after 40-byte call header: lid,io,lock,flags,opaque
					data := body[40:]
					// skip lid(4) io(4) lock(4) flags(4) = 16, then opaque len(4)
					off := 16
					l := binary.BigEndian.Uint32(data[off : off+4])
					cmd := string(data[off+4 : off+4+int(l)])
					f.received <- cmd
					res := make([]byte, 8) // error=0, size
					binary.BigEndian.PutUint32(res[4:], l)
					writeRecord(c, acceptedReply(xid, res))
				case procDeviceRead:
					reply := []byte(f.readReply)
					res := make([]byte, 12+len(reply))
					binary.BigEndian.PutUint32(res[0:], 0)   // error
					binary.BigEndian.PutUint32(res[4:], 0x4) // reason END
					binary.BigEndian.PutUint32(res[8:], uint32(len(reply)))
					if f.readCount != 0 {
						binary.BigEndian.PutUint32(res[8:], f.readCount)
					}
					copy(res[12:], reply)
					writeRecord(c, acceptedReply(xid, res))
				case procDestroyLink:
					res := make([]byte, 4)
					writeRecord(c, acceptedReply(xid, res))
				default:
					writeRecord(c, acceptedReply(xid, make([]byte, 4)))
				}
			}
		}(c)
	}
}

// TRLC-LINKS: REQ-SDS-100
func (f *fakeInstrument) close() {
	_ = f.pmLn.Close()
	_ = f.coreLn.Close()
}

// TRLC-LINKS: REQ-SDS-100
func TestDialSendQuery(t *testing.T) {
	f := newFake(t)
	defer f.close()
	f.readReply = "SAST Stopped\n"

	// Dial hard-codes port 111 for the portmapper, so point it at the fake by
	// dialing the portmap listener's actual address through a small shim: we
	// can't override 111 here, so exercise the RPC layer via a direct client.
	host := net.JoinHostPort(f.host(), itoa(f.pmPort()))
	cl, err := DialAt(host, f.host(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cl.Close()

	if err := cl.Send("STOP"); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case got := <-f.received:
		if got != "STOP\n" {
			t.Errorf("instrument received %q, want %q", got, "STOP\n")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("instrument never received the command")
	}

	resp, err := cl.Query("SAST?")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if resp != "SAST Stopped\n" {
		t.Errorf("query reply = %q", resp)
	}
}

// TRLC-LINKS: REQ-SDS-100
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// A device_read whose byte count exceeds the bytes sent yields the bytes
// sent. A count of 2^31 or more must not slice past them: as an int it is
// negative on a 32-bit target (run with GOARCH=386 to cover the device's word
// size).
// TRLC-LINKS: REQ-SDS-100
func TestQueryClampsAnOversizedReadCount(t *testing.T) {
	for _, count := range []uint32{64, 0x80000000, 0xffffffff} {
		f := newFake(t)
		f.readReply = "SAST Stopped\n"
		f.readCount = count
		cl, err := DialAt(net.JoinHostPort(f.host(), itoa(f.pmPort())), f.host(), 5*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		resp, err := cl.Query("SAST?")
		cl.Close()
		f.close()
		if err != nil || resp != "SAST Stopped\n" {
			t.Errorf("count %#x: reply %q, %v; want the bytes sent", count, resp, err)
		}
	}
}

// rpcReply answers one call on a pipe with the raw reply body given.
// TRLC-LINKS: REQ-SDS-100
func rpcReply(t *testing.T, body func(xid uint32) []byte) ([]byte, error) {
	t.Helper()
	cli, srv := net.Pipe()
	defer func() { _ = cli.Close() }()
	go func() {
		defer func() { _ = srv.Close() }()
		call, err := readRecord(srv)
		if err != nil {
			return
		}
		writeRecord(srv, body(callXID(call)))
	}()
	return rpcCall(cli, 2*time.Second, coreProg, coreVers, procDeviceRead, nil)
}

// A reply's lengths come off the wire: a verifier longer than ONC RPC allows
// is an error, not an offset (on 32 bits one near 2^31 wrapped negative), and
// a fragment claiming gigabytes is refused before it is allocated.
// TRLC-LINKS: REQ-SDS-100
func TestRPCReplyLengthsAreBounded(t *testing.T) {
	_, err := rpcReply(t, func(xid uint32) []byte {
		b := acceptedReply(xid, make([]byte, 8))
		binary.BigEndian.PutUint32(b[16:], 0x7ffffffe) // verf len
		return b
	})
	if err == nil {
		t.Error("a 2 GiB verifier was accepted")
	}

	cli, srv := net.Pipe()
	defer func() { _ = cli.Close() }()
	go func() {
		defer func() { _ = srv.Close() }()
		if _, err := readRecord(srv); err != nil {
			return
		}
		var rm [4]byte
		binary.BigEndian.PutUint32(rm[:], 0xffffffff) // last fragment, 2 GiB
		_, _ = srv.Write(rm[:])
	}()
	if _, err := rpcCall(cli, 2*time.Second, coreProg, coreVers, procDeviceRead, nil); err == nil || !strings.Contains(err.Error(), "reply over") {
		t.Errorf("a 2 GiB fragment: %v, want it refused by size", err)
	}
}

// staleReplies answers one call with n late replies to earlier calls (other
// xids) and then its own; it returns what the call got.
// TRLC-LINKS: REQ-SDS-100
func staleReplies(t *testing.T, n int) ([]byte, error) {
	t.Helper()
	cli, srv := net.Pipe()
	defer func() { _ = cli.Close() }()
	go func() {
		defer func() { _ = srv.Close() }()
		call, err := readRecord(srv)
		if err != nil {
			return
		}
		xid := callXID(call)
		for i := range n {
			writeRecord(srv, acceptedReply(xid-uint32(i)-1, []byte("old!")))
		}
		writeRecord(srv, acceptedReply(xid, []byte("mine")))
	}()
	return rpcCall(cli, 2*time.Second, coreProg, coreVers, procDeviceRead, nil)
}

// A reply to an earlier call that timed out is read past: the late answer of
// one slow call does not misread every call after it. Past maxStale of them
// the call fails.
// TRLC-LINKS: REQ-SDS-100
func TestLateRepliesAreReadPast(t *testing.T) {
	if res, err := staleReplies(t, 2); err != nil || string(res) != "mine" {
		t.Fatalf("after two late replies: %q %v", res, err)
	}
	if _, err := staleReplies(t, maxStale+1); err == nil || !strings.Contains(err.Error(), "xid mismatch") {
		t.Fatalf("after %d late replies: %v", maxStale+1, err)
	}
}
