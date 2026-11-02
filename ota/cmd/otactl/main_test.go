// ENGMODEL-OWNER-UNIT: FU-OTA-OTACTL

package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"open-sds/ota/internal/config"
	"open-sds/ota/internal/slots"
	"open-sds/ota/internal/vxi11"
	"slices"
	"testing"
	"time"
)

// TestDefaultStageIsTheAgentsStaging checks that uploads stage by default
// where the agent on a stick keeps its upload staging: the scope's root file
// system, /tmp included, is read-only, and the stick mounts at U-disk0.
// TRLC-LINKS: REQ-SDS-175
func TestDefaultStageIsTheAgentsStaging(t *testing.T) {
	t.Setenv("OTA_DIR", "/usr/bin/siglent/usr/media/U-disk0/ota")
	t.Setenv("OTA_SLOT_ROOT", "")
	cfg := config.Load()
	if want := slots.New(cfg.SlotRoot).StagingDir(); defaultStageDir != want {
		t.Fatalf("default -stage %q, want the agent's staging %q", defaultStageDir, want)
	}
}

// otactl watch prints each event and heartbeat compacted.
// TRLC-LINKS: REQ-SDS-175
func BenchmarkCompact(b *testing.B) {
	msg := []byte(`{"app": {"running": true, "pid": 812, "slot": "B", "health": {"healthy_once": true, "since_change_ms": 400}},
  "device": "sds1102", "seq": 123, "time_unix": 1700000000, "ips": ["eth0 192.168.1.209/24"], "watchdog": {"armed": true}}`)
	for b.Loop() {
		compact(msg)
	}
}

// fakeLink is a VXI-11 instrument whose device_write fails; it reports each
// procedure it is called with on procs.
// TRLC-LINKS: REQ-SDS-175
func fakeLink(t *testing.T) (pmAddr string, procs chan uint32) {
	t.Helper()
	lc := net.ListenConfig{}
	pm, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	core, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pm.Close(); _ = core.Close() })
	corePort := uint32(netip.MustParseAddrPort(core.Addr().String()).Port())
	procs = make(chan uint32, 16)
	serve := func(ln net.Listener, answer func(proc uint32) []byte) {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				for {
					var rm [4]byte
					if _, err := io.ReadFull(c, rm[:]); err != nil {
						return
					}
					body := make([]byte, binary.BigEndian.Uint32(rm[:])&0x7fffffff)
					if _, err := io.ReadFull(c, body); err != nil {
						return
					}
					proc := binary.BigEndian.Uint32(body[20:24])
					reply := make([]byte, 24, 40)
					copy(reply, body[:4])                    // xid
					binary.BigEndian.PutUint32(reply[4:], 1) // REPLY, accepted, AUTH_NULL, SUCCESS
					reply = append(reply, answer(proc)...)
					binary.BigEndian.PutUint32(rm[:], 0x80000000|uint32(len(reply)))
					if _, err := c.Write(append(rm[:], reply...)); err != nil {
						return
					}
				}
			}()
		}
	}
	go serve(pm, func(uint32) []byte { return binary.BigEndian.AppendUint32(nil, corePort) })
	go serve(core, func(proc uint32) []byte {
		procs <- proc
		res := make([]byte, 16)
		if proc == 11 { // device_write
			binary.BigEndian.PutUint32(res, 4) // a non-zero device error
		}
		return res
	})
	return pm.Addr().String(), procs
}

// A failed scpi command still destroys its link: the instrument serves one,
// and a link left open locks out the next client until it times out.
// TRLC-LINKS: REQ-SDS-175
func TestScpiDestroysTheLinkOnFailure(t *testing.T) {
	pmAddr, procs := fakeLink(t)
	var out bytes.Buffer
	c := &cli{out: &out, errOut: &out, timeout: 2 * time.Second}
	err := scpiAt(c, func() (*vxi11.Client, error) { return vxi11.DialAt(pmAddr, "127.0.0.1", 2*time.Second) }, "STOP")
	if err == nil {
		t.Fatal("a failing device_write reported success")
	}
	var seen []uint32
	for len(procs) > 0 {
		seen = append(seen, <-procs)
	}
	if !slices.Contains(seen, 23) { // destroy_link
		t.Fatalf("procedures %v: no destroy_link after the failure", seen)
	}
}

// Usage mistakes exit 2 and failures 1, with the reason on stderr.
// TRLC-LINKS: REQ-SDS-175
func TestRunExitCodes(t *testing.T) {
	t.Setenv("OTA_TCP", "")
	t.Setenv("OTA_NATS", "")
	for _, tc := range []struct {
		args []string
		code int
	}{
		{nil, 2},
		{[]string{"-nosuchflag"}, 2},
		{[]string{"status"}, 2}, // no transport
		{[]string{"-tcp", "127.0.0.1:1", "exec"}, 2},
		{[]string{"-tcp", "127.0.0.1:1", "-timeout", "1", "status"}, 1}, // nothing listens on port 1
		{[]string{"version"}, 0},
	} {
		var out, errOut bytes.Buffer
		if got := run(tc.args, &out, &errOut); got != tc.code {
			t.Errorf("otactl %v: exit %d, want %d (stderr %q)", tc.args, got, tc.code, errOut.String())
		}
	}
}
