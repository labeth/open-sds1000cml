// ENGMODEL-OWNER-UNIT: FU-OTA-OTACTL
package otactl

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"open-sds/ota/internal/rpcproto"
)

// TRLC-LINKS: REQ-SDS-109
func BenchmarkTCPTransportCall(b *testing.B) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				if _, err := r.ReadBytes('\n'); err == nil {
					c.Write([]byte(`{"ok":true,"data":{"device":"x"}}` + "\n"))
				}
			}(c)
		}
	}()
	tr := NewTCP(ln.Addr().String())
	for b.Loop() {
		if r, err := tr.Call("ping", nil, time.Second); err != nil || !r.OK {
			b.Fatal(err)
		}
	}
}

// memTransport answers the file RPCs from memory, marshalling each request
// as the wire transports do.
// TRLC-LINKS: REQ-SDS-110
type memTransport struct {
	sha  string
	file []byte
}

// TRLC-LINKS: REQ-SDS-110
func (m *memTransport) Call(cmd string, args any, _ time.Duration) (*rpcproto.Response, error) {
	req, _ := json.Marshal(rpcproto.Request{Cmd: cmd, Args: args})
	var r struct {
		Args map[string]any `json:"args"`
	}
	switch cmd {
	case "put.begin":
		json.Unmarshal(req, &r)
		m.sha, _ = r.Args["sha256"].(string)
		return &rpcproto.Response{OK: true, Data: json.RawMessage(`{"id":"up-1"}`)}, nil
	case "put.commit":
		d, _ := json.Marshal(map[string]string{"sha256": m.sha})
		return &rpcproto.Response{OK: true, Data: d}, nil
	case "get":
		d, _ := json.Marshal(map[string]any{"data": base64.StdEncoding.EncodeToString(m.file), "eof": true, "size": len(m.file)})
		return &rpcproto.Response{OK: true, Data: d}, nil
	}
	return &rpcproto.Response{OK: true, Data: json.RawMessage(`{"written":1}`)}, nil
}

// TRLC-LINKS: REQ-SDS-110
func (m *memTransport) Close() {}

// TRLC-LINKS: REQ-SDS-110
func BenchmarkPutFile(b *testing.B) {
	p := filepath.Join(b.TempDir(), "f.bin")
	data := make([]byte, 1<<20)
	rand.Read(data)
	os.WriteFile(p, data, 0o644)
	c := &Client{T: &memTransport{}}
	for b.Loop() {
		if _, err := c.PutFile(p, "/x/f.bin", 0o755, 0, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// TRLC-LINKS: REQ-SDS-110
func BenchmarkGetFile(b *testing.B) {
	data := make([]byte, 256<<10)
	rand.Read(data)
	c := &Client{T: &memTransport{file: data}}
	p := filepath.Join(b.TempDir(), "g.bin")
	for b.Loop() {
		if err := c.GetFile("/x/g.bin", p, nil); err != nil {
			b.Fatal(err)
		}
	}
}
