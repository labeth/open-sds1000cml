package agent

// Takeover sequence tests (spec 01 §2.2 inherit-then-kill), off-device:
//
//   - the factory SCPI service is a REAL localhost VXI-11 instrument stub
//     (same wire protocol, mirroring internal/vxi11's own fake), reached
//     through the vxi11Dial seam because port 111 cannot be bound in tests;
//   - the idle-confirm reads go through a fake gpmcReader (the interface seam
//     over the inherited /dev/Gpmc descriptor);
//   - the factory app is a REAL orphan process holding the fake Gpmc node, so
//     factoryCandidates (/proc discovery) and killFactory (SIGKILL) run for
//     real;
//   - the watchdog device is a plain temp file (open+write succeed, so
//     Acquire arms immediately).
//
// Everything stays on 127.0.0.1 / the local machine.

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"open-sds/ota/internal/config"
	"open-sds/ota/internal/fdinherit"
	"open-sds/ota/internal/gpmc"
	"open-sds/ota/internal/vxi11"
)

// ---- fake gpmc reader (behind the gpmcReader seam) --------------------------

type fakeGpmc struct {
	versionOK bool
	idle      bool // frozen fill counter when true
	stopSeen  *atomic.Bool

	mu              sync.Mutex
	sawStopAtVerify []bool // per VerifyVersion call: had the instrument received STOP yet?
}

func (g *fakeGpmc) OK() bool { return true }

func (g *fakeGpmc) VerifyVersion() (uint16, bool) {
	g.mu.Lock()
	if g.stopSeen != nil {
		g.sawStopAtVerify = append(g.sawStopAtVerify, g.stopSeen.Load())
	}
	g.mu.Unlock()
	if g.versionOK {
		return gpmc.VersionMagic, true
	}
	return 0xdead, false
}

func (g *fakeGpmc) FillFrozen(pairs int, gap time.Duration) (bool, []uint16, error) {
	if g.idle {
		return true, []uint16{42, 42, 42, 42}, nil
	}
	return false, []uint16{1, 2}, nil
}

func (g *fakeGpmc) Read(plane uint8, sel uint16) (uint16, error) { return 0x008a, nil }

func (g *fakeGpmc) verifiedAfterStop() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.sawStopAtVerify) > 0 && g.sawStopAtVerify[0]
}

// ---- fake VXI-11 instrument (real wire protocol on 127.0.0.1) ---------------

const (
	vxCreateLink  = 10
	vxDeviceWrite = 11
	vxDeviceRead  = 12
	vxDestroyLink = 23
)

type fakeInstr struct {
	pmLn, coreLn net.Listener
	corePort     uint32
	readReply    string
	onCmd        func(cmd string) // called for every SCPI line, before replying

	mu  sync.Mutex
	got []string
}

func vxReadRecord(c net.Conn) ([]byte, error) {
	var rm [4]byte
	if _, err := io.ReadFull(c, rm[:]); err != nil {
		return nil, err
	}
	buf := make([]byte, binary.BigEndian.Uint32(rm[:])&0x7fffffff)
	_, err := io.ReadFull(c, buf)
	return buf, err
}

func vxWriteRecord(c net.Conn, body []byte) {
	var rm [4]byte
	binary.BigEndian.PutUint32(rm[:], 0x80000000|uint32(len(body)))
	c.Write(append(rm[:], body...))
}

func vxAcceptedReply(xid uint32, result []byte) []byte {
	b := make([]byte, 24)
	binary.BigEndian.PutUint32(b[0:], xid)
	binary.BigEndian.PutUint32(b[4:], 1) // REPLY
	// MSG_ACCEPTED(0), verf AUTH_NULL(0,0), accept_stat SUCCESS(0) all zero.
	return append(b, result...)
}

func newFakeInstr(t *testing.T, readReply string, onCmd func(string)) *fakeInstr {
	t.Helper()
	pm, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	core, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeInstr{
		pmLn: pm, coreLn: core,
		corePort:  uint32(core.Addr().(*net.TCPAddr).Port),
		readReply: readReply,
		onCmd:     onCmd,
	}
	go f.servePortmap()
	go f.serveCore()
	t.Cleanup(func() { pm.Close(); core.Close() })
	return f
}

func (f *fakeInstr) pmAddr() string { return f.pmLn.Addr().String() }

func (f *fakeInstr) cmds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.got...)
}

func (f *fakeInstr) servePortmap() {
	for {
		c, err := f.pmLn.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			for {
				body, err := vxReadRecord(c)
				if err != nil {
					return
				}
				res := make([]byte, 4) // GETPORT -> core port
				binary.BigEndian.PutUint32(res, f.corePort)
				vxWriteRecord(c, vxAcceptedReply(binary.BigEndian.Uint32(body[0:4]), res))
			}
		}(c)
	}
}

func (f *fakeInstr) serveCore() {
	for {
		c, err := f.coreLn.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			for {
				body, err := vxReadRecord(c)
				if err != nil {
					return
				}
				xid := binary.BigEndian.Uint32(body[0:4])
				switch binary.BigEndian.Uint32(body[20:24]) { // proc
				case vxCreateLink:
					res := make([]byte, 16) // error=0, lid=1, abortPort, maxRecvSize
					binary.BigEndian.PutUint32(res[4:], 1)
					binary.BigEndian.PutUint32(res[12:], 0x800000)
					vxWriteRecord(c, vxAcceptedReply(xid, res))
				case vxDeviceWrite:
					// After the 40-byte call header: lid,io,lock,flags then opaque.
					data := body[40:]
					l := binary.BigEndian.Uint32(data[16:20])
					cmd := string(data[20 : 20+int(l)])
					f.mu.Lock()
					f.got = append(f.got, cmd)
					f.mu.Unlock()
					if f.onCmd != nil {
						f.onCmd(cmd)
					}
					res := make([]byte, 8) // error=0, size
					binary.BigEndian.PutUint32(res[4:], l)
					vxWriteRecord(c, vxAcceptedReply(xid, res))
				case vxDeviceRead:
					reply := []byte(f.readReply)
					res := make([]byte, 12+len(reply))
					binary.BigEndian.PutUint32(res[4:], 0x4) // reason END
					binary.BigEndian.PutUint32(res[8:], uint32(len(reply)))
					copy(res[12:], reply)
					vxWriteRecord(c, vxAcceptedReply(xid, res))
				case vxDestroyLink:
					vxWriteRecord(c, vxAcceptedReply(xid, make([]byte, 4)))
				default:
					vxWriteRecord(c, vxAcceptedReply(xid, make([]byte, 4)))
				}
			}
		}(c)
	}
}

// ---- seam swaps --------------------------------------------------------------

func swapVxiDial(t *testing.T, fn func(string, time.Duration) (*vxi11.Client, error)) {
	t.Helper()
	old := vxi11Dial
	vxi11Dial = fn
	t.Cleanup(func() { vxi11Dial = old })
}

// dialFake routes factoryStop's fixed 127.0.0.1:111 portmapper dial to the
// fake instrument, asserting the production host is loopback (the factory
// SCPI service is only ever local).
func dialFake(t *testing.T, f *fakeInstr) {
	t.Helper()
	swapVxiDial(t, func(host string, timeout time.Duration) (*vxi11.Client, error) {
		if host != "127.0.0.1" {
			t.Errorf("factoryStop dialed host %q, want 127.0.0.1", host)
		}
		return vxi11.DialAt(f.pmAddr(), "127.0.0.1", timeout)
	})
}

func swapIdleTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := takeoverIdleTimeout
	takeoverIdleTimeout = d
	t.Cleanup(func() { takeoverIdleTimeout = old })
}

// captureReassert replaces the async network re-assert with a recorder.
func captureReassert(t *testing.T) chan []string {
	t.Helper()
	ch := make(chan []string, 1)
	old := reassertNetworkFn
	reassertNetworkFn = func(a *Agent, preIPs []string) { ch <- preIPs }
	t.Cleanup(func() { reassertNetworkFn = old })
	return ch
}

// ---- environment -------------------------------------------------------------

// takeoverAgent builds an agent whose Gpmc node is a temp file this process
// has open (so New discovers a genuinely inherited fd) and whose watchdog is
// a plain writable temp file (Acquire arms instantly).
func takeoverAgent(t *testing.T, g *fakeGpmc) (*Agent, string) {
	t.Helper()
	dir := t.TempDir()
	gpmcPath := filepath.Join(dir, "gpmc.dev")
	wdPath := filepath.Join(dir, "watchdog.dev")
	for _, p := range []string{gpmcPath, wdPath} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("OTA_DIR", filepath.Join(dir, "ota"))
	t.Setenv("OTA_SLOT_ROOT", filepath.Join(dir, "slots"))
	t.Setenv("OTA_HEALTH_DIR", dir)
	t.Setenv("OTA_LISTEN", "")
	t.Setenv("OTA_NATS", "")
	t.Setenv("OTA_GPMC", gpmcPath)
	t.Setenv("OTA_WD_DEV", wdPath)
	if err := os.MkdirAll(filepath.Join(dir, "ota"), 0o755); err != nil {
		t.Fatal(err) // state.json home
	}

	f, err := os.Open(gpmcPath) // the "inherited" descriptor
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })

	a := New(config.Load())
	if a.gpmcFD < 0 {
		// TempDir behind a symlink can defeat the /proc readlink match; the
		// takeover gate only needs a valid fd number.
		a.gpmcFD = int(f.Fd())
	}
	a.gpmc = g
	t.Cleanup(a.wd.Disarm)
	return a, gpmcPath
}

// spawnGpmcHolder starts a process that (a) holds an open fd on the fake Gpmc
// node and (b) is NOT our descendant — `sh -c '... &'` orphans the sleep when
// sh exits — exactly the shape factoryCandidates must pick up and killFactory
// must kill. Returns once factoryCandidates actually reports it.
func spawnGpmcHolder(t *testing.T, a *Agent, path string) int {
	t.Helper()
	out, err := exec.Command("/bin/sh", "-c",
		`sleep 30 < "$0" > /dev/null 2>&1 & echo $!`, path).Output()
	if err != nil {
		t.Fatalf("spawn holder: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 1 {
		t.Fatalf("bad holder pid %q: %v", out, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, h := range a.factoryCandidates() {
			if h.PID == pid {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("holder pid=%d never showed up as a factory candidate (holders=%v)",
		pid, fdinherit.HoldersOf(path))
	return 0
}

func stepIdx(steps []string, sub string) int {
	for i, s := range steps {
		if strings.Contains(s, sub) {
			return i
		}
	}
	return -1
}

// ---- the tests ----------------------------------------------------------------

func TestTakeoverHappyPathOrderOfOperations(t *testing.T) {
	var stopSeen atomic.Bool
	g := &fakeGpmc{versionOK: true, idle: true, stopSeen: &stopSeen}
	a, gpmcPath := takeoverAgent(t, g)
	instr := newFakeInstr(t, "SAST STOP\n", func(cmd string) {
		if strings.HasPrefix(cmd, "STOP") {
			stopSeen.Store(true)
		}
	})
	dialFake(t, instr)
	reassert := captureReassert(t)
	pid := spawnGpmcHolder(t, a, gpmcPath)

	res := a.Takeover(TakeoverOpts{})
	if !res.OK {
		t.Fatalf("takeover failed: %s\nsteps: %s", res.Err, res.Summary())
	}

	// The factory app was driven over its own SCPI service, in order:
	// momentary STOP, then TRMD STOP, then the SAST? diagnostic query.
	if got := instr.cmds(); len(got) != 3 ||
		got[0] != "STOP\n" || got[1] != "TRMD STOP\n" || got[2] != "SAST?\n" {
		t.Errorf("factory SCPI sequence = %q, want [STOP, TRMD STOP, SAST?]", got)
	}
	// Idle-confirm reads happened post-STOP, while the factory app was alive.
	if !g.verifiedAfterStop() {
		t.Error("idle-confirm read the bus before the factory STOP was sent")
	}

	// Order of operations, from the step log: STOP -> idle confirm ->
	// persist taken_over (point of no return, BEFORE any kill) -> SIGKILL ->
	// watchdog -> done.
	order := []string{
		"gate: inherited gpmc fd=",
		"factory STOP sent",
		"idle confirmed",
		"state persisted: taken_over=true",
		"SIGKILL pid=",
		"watchdog acquired",
		"takeover complete",
	}
	prev := -1
	for _, marker := range order {
		i := stepIdx(res.Steps, marker)
		if i < 0 {
			t.Fatalf("step %q missing; steps: %s", marker, res.Summary())
		}
		if i <= prev {
			t.Errorf("step %q out of order; steps: %s", marker, res.Summary())
		}
		prev = i
	}
	if stepIdx(res.Steps, fmt.Sprintf("candidate pid=%d", pid)) < 0 {
		t.Errorf("holder pid=%d not reported as candidate; steps: %s", pid, res.Summary())
	}

	// Outcomes: persisted state, dead factory holder, armed watchdog, and the
	// async network re-assert scheduled.
	if !a.st.get().TakenOver {
		t.Error("taken_over not persisted")
	}
	deadline := time.Now().Add(3 * time.Second)
	for fdinherit.Alive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if fdinherit.Alive(pid) {
		t.Errorf("factory holder pid=%d survived the kill", pid)
	}
	if !a.wd.Status().Armed {
		t.Error("watchdog not armed after takeover")
	}
	select {
	case <-reassert:
	case <-time.After(2 * time.Second):
		t.Error("reassertNetwork was not scheduled on the happy path")
	}

	// Idempotency: a second takeover is a no-op success.
	res2 := a.Takeover(TakeoverOpts{})
	if !res2.OK || stepIdx(res2.Steps, "already taken over") < 0 {
		t.Errorf("second takeover not idempotent: %+v", res2)
	}
}

func TestTakeoverRefusesWhenFactoryWontIdle(t *testing.T) {
	// STOP succeeds over the wire but the fill counter keeps advancing: the
	// engine never lands. Without force the takeover must refuse BEFORE the
	// point of no return: nothing persisted, nothing killed, no watchdog, no
	// network re-assert.
	g := &fakeGpmc{versionOK: true, idle: false}
	a, gpmcPath := takeoverAgent(t, g)
	instr := newFakeInstr(t, "SAST TRIG\n", nil)
	dialFake(t, instr)
	swapIdleTimeout(t, 400*time.Millisecond)
	reassert := captureReassert(t)
	pid := spawnGpmcHolder(t, a, gpmcPath)

	res := a.Takeover(TakeoverOpts{})
	if res.OK {
		t.Fatalf("takeover must refuse when the engine won't idle; steps: %s", res.Summary())
	}
	if !strings.Contains(res.Err, "idle landing not confirmed") {
		t.Errorf("err = %q, want idle-landing refusal", res.Err)
	}
	if stepIdx(res.Steps, "factory STOP sent") < 0 {
		t.Errorf("refusal should come at the idle gate, after STOP; steps: %s", res.Summary())
	}
	if a.st.get().TakenOver {
		t.Error("refusal must not persist taken_over")
	}
	if !fdinherit.Alive(pid) {
		t.Error("refusal must not kill the factory app")
	}
	if a.wd.Status().Armed {
		t.Error("refusal must not arm the watchdog")
	}
	select {
	case <-reassert:
		t.Error("reassertNetwork must not run on a refused takeover")
	default:
	}
}

func TestTakeoverRefusesWhenFactoryStopUnreachable(t *testing.T) {
	// The factory SCPI service can't be reached (dead portmapper). Without
	// force this is a hard refusal at the STOP gate.
	g := &fakeGpmc{versionOK: true, idle: true}
	a, gpmcPath := takeoverAgent(t, g)
	swapVxiDial(t, func(host string, timeout time.Duration) (*vxi11.Client, error) {
		return nil, fmt.Errorf("vxi11: portmap dial: connection refused (test)")
	})
	reassert := captureReassert(t)
	pid := spawnGpmcHolder(t, a, gpmcPath)

	res := a.Takeover(TakeoverOpts{})
	if res.OK {
		t.Fatalf("takeover must refuse when STOP cannot be commanded; steps: %s", res.Summary())
	}
	if !strings.Contains(res.Err, "could not command factory STOP") {
		t.Errorf("err = %q, want STOP-gate refusal", res.Err)
	}
	if a.st.get().TakenOver || !fdinherit.Alive(pid) {
		t.Error("STOP-gate refusal must leave the device untouched")
	}
	select {
	case <-reassert:
		t.Error("reassertNetwork must not run on a refused takeover")
	default:
	}
}

func TestTakeoverForcePartialFailureStillReassertsNetwork(t *testing.T) {
	// Force is the dead-factory escape hatch: STOP fails AND the idle landing
	// can't be confirmed, but the sequence continues past the point of no
	// return — and the contract is that the post-kill network re-assert is
	// scheduled regardless of those partial failures.
	g := &fakeGpmc{versionOK: false, idle: false}
	a, gpmcPath := takeoverAgent(t, g)
	swapVxiDial(t, func(host string, timeout time.Duration) (*vxi11.Client, error) {
		return nil, fmt.Errorf("vxi11: portmap dial: connection refused (test)")
	})
	swapIdleTimeout(t, 300*time.Millisecond)
	reassert := captureReassert(t)
	pid := spawnGpmcHolder(t, a, gpmcPath)

	res := a.Takeover(TakeoverOpts{Force: true})
	if !res.OK {
		t.Fatalf("forced takeover failed: %s\nsteps: %s", res.Err, res.Summary())
	}
	if stepIdx(res.Steps, "force: continuing without STOP") < 0 ||
		stepIdx(res.Steps, "force: continuing without idle confirm") < 0 {
		t.Errorf("force fallthroughs missing; steps: %s", res.Summary())
	}
	if !a.st.get().TakenOver {
		t.Error("forced takeover must persist taken_over")
	}
	deadline := time.Now().Add(3 * time.Second)
	for fdinherit.Alive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if fdinherit.Alive(pid) {
		t.Errorf("factory holder pid=%d survived the forced kill", pid)
	}
	select {
	case <-reassert:
	case <-time.After(2 * time.Second):
		t.Error("reassertNetwork must run even when STOP/idle-confirm failed under force")
	}
}
