// Package otactl is the host-side controller for the OTA agents.
package otactl

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"open-sds/ota/internal/rpcproto"

	"github.com/nats-io/nats.go"
)

// Transport delivers one RPC request to a device and returns its response.
type Transport interface {
	Call(cmd string, args any, timeout time.Duration) (*rpcproto.Response, error)
	Close()
}

// ---- TCP transport (direct to OTA_LISTEN) ---------------------------------

type tcpTransport struct{ addr string }

// NewTCP dials on each call (the agent handles one request per line but keeps
// the connection open; a fresh connection per call keeps the client simple
// and robust to a mid-transfer drop).
func NewTCP(addr string) Transport { return &tcpTransport{addr: addr} }

func (t *tcpTransport) Call(cmd string, args any, timeout time.Duration) (*rpcproto.Response, error) {
	conn, err := net.DialTimeout("tcp", t.addr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", t.addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	req, _ := json.Marshal(rpcproto.Request{Cmd: cmd, Args: args})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return nil, err
	}
	r := bufio.NewReaderSize(conn, 1<<20)
	line, err := r.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, err
	}
	var resp rpcproto.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("bad response: %w (%s)", err, truncate(line, 200))
	}
	return &resp, nil
}

func (t *tcpTransport) Close() {}

// ---- NATS transport --------------------------------------------------------

type natsTransport struct {
	nc     *nats.Conn
	device string
}

func NewNATS(url, device string, opts ...nats.Option) (Transport, error) {
	base := []nats.Option{nats.Name("otactl"), nats.Timeout(5 * time.Second)}
	nc, err := nats.Connect(url, append(base, opts...)...)
	if err != nil {
		return nil, fmt.Errorf("nats connect %s: %w", url, err)
	}
	return &natsTransport{nc: nc, device: device}, nil
}

func (t *natsTransport) Call(cmd string, args any, timeout time.Duration) (*rpcproto.Response, error) {
	req, _ := json.Marshal(rpcproto.Request{Cmd: cmd, Args: args})
	msg, err := t.nc.Request("ota."+t.device+".rpc", req, timeout)
	if err != nil {
		return nil, fmt.Errorf("nats request (device %s reachable?): %w", t.device, err)
	}
	var resp rpcproto.Response
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return nil, fmt.Errorf("bad response: %w", err)
	}
	return &resp, nil
}

func (t *natsTransport) Conn() *nats.Conn { return t.nc }

func (t *natsTransport) Close() { t.nc.Drain() }

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}
