package vxi11

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// fakeInstrument is a minimal VXI-11 server: a portmapper on the listener that
// GETPORTs to a second core listener, which answers create_link / device_write
// / device_read / destroy_link. It records the SCPI commands it receives.
type fakeInstrument struct {
	pmLn, coreLn net.Listener
	corePort     uint32
	received     chan string
	readReply    string
}

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

func writeRecord(c net.Conn, body []byte) {
	var rm [4]byte
	binary.BigEndian.PutUint32(rm[:], 0x80000000|uint32(len(body)))
	c.Write(append(rm[:], body...))
}

// acceptedReply builds an accepted RPC reply header for xid + result payload.
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

func callXID(body []byte) uint32  { return binary.BigEndian.Uint32(body[0:4]) }
func callProc(body []byte) uint32 { return binary.BigEndian.Uint32(body[20:24]) }

func newFake(t *testing.T) *fakeInstrument {
	t.Helper()
	pm, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	core, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeInstrument{
		pmLn: pm, coreLn: core,
		corePort:  uint32(core.Addr().(*net.TCPAddr).Port),
		received:  make(chan string, 8),
		readReply: "",
	}
	go f.servePortmap()
	go f.serveCore()
	return f
}

func (f *fakeInstrument) host() string { return "127.0.0.1" }
func (f *fakeInstrument) pmPort() int  { return f.pmLn.Addr().(*net.TCPAddr).Port }

func (f *fakeInstrument) servePortmap() {
	for {
		c, err := f.pmLn.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
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

func (f *fakeInstrument) serveCore() {
	for {
		c, err := f.coreLn.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
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

func (f *fakeInstrument) close() {
	f.pmLn.Close()
	f.coreLn.Close()
}

func TestDialSendQuery(t *testing.T) {
	f := newFake(t)
	defer f.close()
	f.readReply = "SAST Stopped\n"

	// Dial hard-codes port 111 for the portmapper, so point it at the fake by
	// dialing the portmap listener's actual address through a small shim: we
	// can't override 111 here, so exercise the RPC layer via a direct client.
	host := net.JoinHostPort(f.host(), itoa(f.pmPort()))
	cl, err := dialAt(host, f.host(), 5*time.Second)
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
