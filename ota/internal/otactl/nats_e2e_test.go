package otactl_test

// End-to-end NATS path: an embedded nats-server on 127.0.0.1, the REAL agent
// (its runNATS worker, via Run with OTA_NATS set) connected to it, driven by
// the REAL otactl NATS transport — the exact production chain minus the wire
// between lab host and device. Asserts the ota.<device>.rpc subject scheme,
// the online event + heartbeat push path, and response parity with the TCP
// transport against the same agent.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"open-sds/ota/internal/agent"
	"open-sds/ota/internal/config"
	"open-sds/ota/internal/otactl"
	"open-sds/ota/internal/rpcproto"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

const e2eDevice = "e2e-dev-1"

// startEmbeddedNATS runs a broker on an ephemeral 127.0.0.1 port (the same
// nats-server embedding `otactl serve` uses) and returns its client URL.
func startEmbeddedNATS(t *testing.T) string {
	t.Helper()
	ns, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, JetStream: false, NoSigs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(10 * time.Second) {
		t.Fatal("embedded nats-server not ready")
	}
	t.Cleanup(ns.Shutdown)
	return ns.ClientURL()
}

// startAgent boots the real agent against the broker with its TCP fallback on
// an ephemeral localhost port, and returns it with the TCP control address.
func startAgent(t *testing.T, natsURL string) (*agent.Agent, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("OTA_DIR", filepath.Join(dir, "ota"))
	t.Setenv("OTA_SLOT_ROOT", filepath.Join(dir, "slots"))
	t.Setenv("OTA_HEALTH_DIR", dir)
	t.Setenv("OTA_LISTEN", "127.0.0.1:0")
	t.Setenv("OTA_NATS", natsURL)
	t.Setenv("OTA_DEVICE_ID", e2eDevice)
	t.Setenv("OTA_HB_INTERVAL", "1")
	a := agent.New(config.Load())
	go a.Run()
	t.Cleanup(a.Stop)

	deadline := time.Now().Add(5 * time.Second)
	for a.TCPAddr() == "" {
		if time.Now().After(deadline) {
			t.Fatal("agent TCP listener never came up")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return a, a.TCPAddr()
}

func waitNATSReady(t *testing.T, tr otactl.Transport) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := tr.Call("ping", nil, 700*time.Millisecond); err == nil && resp.OK {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("agent never became reachable over NATS")
}

func callBoth(t *testing.T, natsTr, tcpTr otactl.Transport, cmd string, args any) (viaNATS, viaTCP *rpcproto.Response) {
	t.Helper()
	viaNATS, err := natsTr.Call(cmd, args, 10*time.Second)
	if err != nil {
		t.Fatalf("%s via NATS: %v", cmd, err)
	}
	viaTCP, err = tcpTr.Call(cmd, args, 10*time.Second)
	if err != nil {
		t.Fatalf("%s via TCP: %v", cmd, err)
	}
	if !viaNATS.OK || !viaTCP.OK {
		t.Fatalf("%s not OK: nats=%+v tcp=%+v", cmd, viaNATS, viaTCP)
	}
	return viaNATS, viaTCP
}

func TestNATSEndToEndSubjectsEventsAndTCPParity(t *testing.T) {
	url := startEmbeddedNATS(t)

	// Observer connection: lifecycle subscriptions must be registered BEFORE
	// the agent connects so the one-shot "online" event is captured.
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	evSub, err := nc.SubscribeSync("ota." + e2eDevice + ".event")
	if err != nil {
		t.Fatal(err)
	}
	hbSub, err := nc.SubscribeSync("ota." + e2eDevice + ".heartbeat")
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}

	_, tcpAddr := startAgent(t, url)

	natsTr, err := otactl.NewNATS(url, e2eDevice)
	if err != nil {
		t.Fatal(err)
	}
	defer natsTr.Close()
	waitNATSReady(t, natsTr)

	tcpTr := otactl.NewTCP(tcpAddr)
	defer tcpTr.Close()

	// ---- subject naming: the request really travels on ota.<device>.rpc ----
	rpcSub, err := nc.SubscribeSync("ota.*.rpc")
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	if resp, err := natsTr.Call("ping", nil, 5*time.Second); err != nil || !resp.OK {
		t.Fatalf("ping for subject check: %v %+v", err, resp)
	}
	m, err := rpcSub.NextMsg(3 * time.Second)
	if err != nil {
		t.Fatalf("rpc request never seen on ota.*.rpc: %v", err)
	}
	if m.Subject != "ota."+e2eDevice+".rpc" {
		t.Errorf("rpc subject = %q, want %q", m.Subject, "ota."+e2eDevice+".rpc")
	}
	if !strings.Contains(string(m.Data), `"cmd":"ping"`) {
		t.Errorf("rpc payload = %s, want a ping request envelope", m.Data)
	}
	_ = rpcSub.Unsubscribe()

	// ---- push path: online event on connect, heartbeat on the interval ----
	em, err := evSub.NextMsg(10 * time.Second)
	if err != nil {
		t.Fatalf("no event on ota.%s.event: %v", e2eDevice, err)
	}
	var ev struct {
		Device string `json:"device"`
		Kind   string `json:"kind"`
	}
	if err := json.Unmarshal(em.Data, &ev); err != nil {
		t.Fatalf("bad event json: %v (%s)", err, em.Data)
	}
	if ev.Kind != "online" || ev.Device != e2eDevice {
		t.Errorf("first event = %+v, want online from %s", ev, e2eDevice)
	}
	hm, err := hbSub.NextMsg(10 * time.Second)
	if err != nil {
		t.Fatalf("no heartbeat on ota.%s.heartbeat: %v", e2eDevice, err)
	}
	var hb struct {
		Device string `json:"device"`
		Seq    int64  `json:"seq"`
	}
	if err := json.Unmarshal(hm.Data, &hb); err != nil {
		t.Fatalf("bad heartbeat json: %v (%s)", err, hm.Data)
	}
	if hb.Device != e2eDevice || hb.Seq < 1 {
		t.Errorf("heartbeat = %+v, want device %s with seq >= 1", hb, e2eDevice)
	}

	// ---- ping parity: identity fields agree across transports ----
	nPing, tPing := callBoth(t, natsTr, tcpTr, "ping", nil)
	type pingData struct {
		Device    string `json:"device"`
		AgentSlot string `json:"agent_slot"`
	}
	var np, tp pingData
	if err := json.Unmarshal(nPing.Data, &np); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(tPing.Data, &tp); err != nil {
		t.Fatal(err)
	}
	if np != tp || np.Device != e2eDevice {
		t.Errorf("ping parity: nats=%+v tcp=%+v (device want %s)", np, tp, e2eDevice)
	}

	// ---- status parity: the stable fields agree ----
	nSt, tSt := callBoth(t, natsTr, tcpTr, "status", nil)
	type statusData struct {
		Device    string `json:"device"`
		TakenOver bool   `json:"taken_over"`
		AgentSlot string `json:"agent_slot"`
		Slots     struct {
			Active string `json:"active"`
		} `json:"slots"`
	}
	var ns, ts statusData
	if err := json.Unmarshal(nSt.Data, &ns); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(tSt.Data, &ts); err != nil {
		t.Fatal(err)
	}
	if ns != ts {
		t.Errorf("status parity: nats=%+v tcp=%+v", ns, ts)
	}
	if ns.Device != e2eDevice || ns.TakenOver || ns.Slots.Active != "A" {
		t.Errorf("status = %+v, want device %s, coexist, active slot A", ns, e2eDevice)
	}

	// ---- exec parity: deterministic output, byte-identical responses ----
	args := map[string]any{"argv": []string{"/bin/sh", "-c", "printf e2e-ok"}, "timeout_s": 15}
	nEx, tEx := callBoth(t, natsTr, tcpTr, "exec", args)
	if string(nEx.Data) != string(tEx.Data) {
		t.Errorf("exec responses differ:\n  nats: %s\n  tcp:  %s", nEx.Data, tEx.Data)
	}
	if !strings.Contains(string(nEx.Data), `"output":"e2e-ok"`) ||
		!strings.Contains(string(nEx.Data), `"exit":0`) {
		t.Errorf("exec data = %s, want exit 0 with output e2e-ok", nEx.Data)
	}

	// ---- error parity: an unknown cmd is a structured refusal on both ----
	nErr, err := natsTr.Call("no-such-cmd", nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	tErr, err := tcpTr.Call("no-such-cmd", nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if nErr.OK || tErr.OK || nErr.Err != tErr.Err || !strings.Contains(nErr.Err, "unknown cmd") {
		t.Errorf("unknown-cmd parity: nats=%+v tcp=%+v", nErr, tErr)
	}
}
