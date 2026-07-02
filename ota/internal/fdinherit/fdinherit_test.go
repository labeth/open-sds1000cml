package fdinherit

import (
	"os"
	"testing"
)

func TestFindInheritedFD(t *testing.T) {
	// Open a real file; its /proc/self/fd entry must be discoverable by path.
	f, err := os.CreateTemp(t.TempDir(), "fdtest")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	path := f.Name()

	fd := Find(path)
	if fd < 0 {
		t.Fatalf("Find(%s) returned -1; expected the open fd", path)
	}
	if fd != int(f.Fd()) {
		t.Errorf("Find returned fd %d, file has fd %d", fd, f.Fd())
	}

	if Find("/nonexistent/device/node") != -1 {
		t.Error("Find of an unopened path should be -1")
	}
}

func TestHoldersOfSelf(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "holdertest")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	holders := HoldersOf(f.Name())
	found := false
	me := os.Getpid()
	for _, h := range holders {
		if h.PID == me {
			found = true
		}
	}
	if !found {
		t.Errorf("HoldersOf did not find this process (pid %d) among %v", me, holders)
	}
}

func TestAncestorsAndAlive(t *testing.T) {
	anc := AncestorsOfSelf()
	if len(anc) == 0 {
		t.Error("expected at least one ancestor (the test runner / shell)")
	}
	if anc[os.Getpid()] {
		t.Error("self should not be in ancestors")
	}
	if !Alive(os.Getpid()) {
		t.Error("self should be alive")
	}
	if Alive(1 << 30) {
		t.Error("an absurd pid should not be alive")
	}
}

func TestPPidOfSelf(t *testing.T) {
	if PPid(os.Getpid()) != os.Getppid() {
		t.Errorf("PPid(self)=%d, want %d", PPid(os.Getpid()), os.Getppid())
	}
}
