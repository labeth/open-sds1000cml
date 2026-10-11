// ENGMODEL-OWNER-UNIT: FU-APP-SCPI
package scpi

import (
	"errors"
	"testing"
)

type fakeSetups struct {
	saved, recalled []int
	fail            bool
}

func (f *fakeSetups) SaveSetup(n int) error {
	f.saved = append(f.saved, n)
	return nil
}

func (f *fakeSetups) RecallSetup(n int) error {
	if f.fail {
		return errors.New("empty")
	}
	f.recalled = append(f.recalled, n)
	return nil
}

// *SAV/*RCL reach the setup memory, and anything they cannot do is an error,
// never a silent success (bug hunt 2026-10-10: *RCL 1 restored nothing).
// TRLC-LINKS: REQ-SDS-024
func TestSaveRecallSetupMemory(t *testing.T) {
	h, _ := newH(t)
	if out := string(h.HandleLine([]byte("*SAV 1\n"))); out != errOutOfRange+"\n" {
		t.Fatalf("*SAV with no setup memory: %q", out)
	}
	m := &fakeSetups{}
	h.SetSetupMemory(m)
	for _, c := range []string{"*SAV 3", "*RCL 3"} {
		if out := h.HandleLine([]byte(c + "\n")); len(out) != 0 {
			t.Fatalf("%s: %q", c, out)
		}
	}
	if len(m.saved) != 1 || m.saved[0] != 3 || len(m.recalled) != 1 || m.recalled[0] != 3 {
		t.Fatalf("saved %v recalled %v", m.saved, m.recalled)
	}
	for c, want := range map[string]string{"*SAV 0": errOutOfRange, "*RCL 21": errOutOfRange, "*SAV x": errHeader} {
		if out := string(h.HandleLine([]byte(c + "\n"))); out != want+"\n" {
			t.Fatalf("%s: %q want %q", c, out, want)
		}
	}
	m.fail = true
	if out := string(h.HandleLine([]byte("*RCL 4\n"))); out != errOutOfRange+"\n" {
		t.Fatalf("*RCL of an empty slot: %q", out)
	}
}

type fakeCurMeas struct {
	fakeDisplay
	cursors, meas bool
}

func (d *fakeCurMeas) CursorsOn() bool      { return d.cursors }
func (d *fakeCurMeas) SetCursorsOn(on bool) { d.cursors = on }
func (d *fakeCurMeas) SetMeasOn(on bool)    { d.meas = on }

type fakeSetupFiles struct {
	fakeSetups
	files map[string]bool
	got   string
}

func (f *fakeSetupFiles) SaveSetupFile(n string) error { f.files[n] = true; return nil }
func (f *fakeSetupFiles) RecallSetupFile(n string) error {
	if !f.files[n] {
		return errors.New("missing")
	}
	f.got = n
	return nil
}

// The panel-memory and cursor/measure verbs act or refuse; none is silently
// accepted (bug hunt 2026-10-11: PNSU/STPN/RCPN/PACU/CRMS/CRST did nothing).
// TRLC-LINKS: REQ-SDS-024
func TestPanelMemoryAndCursorVerbsActOrRefuse(t *testing.T) {
	h, _ := newH(t)
	d := &fakeCurMeas{}
	h.disp = d
	fs := &fakeSetupFiles{files: map[string]bool{}}
	h.SetSetupMemory(fs)
	ok := func(c string) {
		t.Helper()
		if out := h.HandleLine([]byte(c + "\n")); len(out) != 0 {
			t.Fatalf("%s: %q", c, out)
		}
	}
	tok := func(c, want string) {
		t.Helper()
		if out := string(h.HandleLine([]byte(c + "\n"))); out != want+"\n" {
			t.Fatalf("%s: %q want %q", c, out, want)
		}
	}
	ok(`STPN DISK,UDSK,FILE,'/usb/BENCH_1.SET'`)
	ok(`RCPN DISK,UDSK,FILE,"BENCH_1.SET"`)
	if !fs.files["BENCH_1.SET"] || fs.got != "BENCH_1.SET" {
		t.Fatalf("files %v recalled %q", fs.files, fs.got)
	}
	tok(`RCPN DISK,UDSK,FILE,'NOPE.SET'`, errOutOfRange)
	tok(`STPN DISK,UDSK,FILE,'../x.json'`, errOutOfRange)
	tok(`STPN DISK,UDSK,FILE,'a b.SET'`, errOutOfRange)
	tok(`STPN UDSK,FILE,'A.SET'`, errHeader)
	ok("CRMS MANUAL")
	if !d.cursors || string(h.HandleLine([]byte("CRMS?\n"))) != "CRMS MANUAL\n" {
		t.Fatalf("CRMS MANUAL: cursors %v", d.cursors)
	}
	ok("CRMS OFF")
	tok("CRMS TRACK", errOutOfRange)
	ok("PACU PKPK,C1")
	if !d.meas {
		t.Fatal("PACU did not show the measurements")
	}
	tok("PACU BOGUS,C1", errOutOfRange)
	tok("PACU PKPK,C3", errSuffix)
	tok("CRST HREF,2.0", errOutOfRange)
	tok("PNSU 1", errOutOfRange)
}
