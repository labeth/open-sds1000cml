package bus

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

// The vendored module is the exact build proven on the unit (bus/dcinv/README.md).
const dcinvKOSHA256 = "6f7654881bad20536d6fd883339757d947fef9001df3a8cfec3c9d8bbe663f88"

func TestDcinvEmbedded(t *testing.T) {
	if len(dcinvKO) < 4096 || string(dcinvKO[1:4]) != "ELF" {
		t.Fatalf("embedded dcinv.ko is not an ELF module (%d bytes)", len(dcinvKO))
	}
	sum := sha256.Sum256(dcinvKO)
	if got := hex.EncodeToString(sum[:]); got != dcinvKOSHA256 {
		t.Fatalf("dcinv.ko sha256 %s, want %s", got, dcinvKOSHA256)
	}
}

func fakeEnv(present map[string]bool, devices string) (dcinvEnv, *[]string) {
	log := []string{}
	e := dcinvEnv{
		exists:  func(p string) bool { return present[p] },
		writeKO: func(dir string) (string, error) { log = append(log, "write:"+dir); return dir + "/dcinv.ko", nil },
		insmod: func(p string) error {
			log = append(log, "insmod:"+p)
			present[DcinvPath] = true // devtmpfs creates the node
			return nil
		},
		devices:  func() (string, error) { return devices, nil },
		mknod:    func(p string, major int) error { log = append(log, "mknod:"+p); present[p] = true; return nil },
		sleep:    func(time.Duration) {},
		deadline: 200 * time.Millisecond,
	}
	return e, &log
}

func TestEnsureDcinvAlreadyPresent(t *testing.T) {
	e, log := fakeEnv(map[string]bool{DcinvPath: true}, "")
	if err := ensureDcinv(e, "/slot", nil); err != nil || len(*log) != 0 {
		t.Fatalf("err=%v log=%v", err, *log)
	}
}

func TestEnsureDcinvLoadsAndDevtmpfsCreatesNode(t *testing.T) {
	e, log := fakeEnv(map[string]bool{}, "Character devices:\n  1 mem\n")
	if err := ensureDcinv(e, "/slot", nil); err != nil {
		t.Fatal(err)
	}
	if want := []string{"write:/slot", "insmod:/slot/dcinv.ko"}; len(*log) != 2 || (*log)[0] != want[0] || (*log)[1] != want[1] {
		t.Fatalf("log %v", *log)
	}
}

func TestEnsureDcinvMknodFromProcDevices(t *testing.T) {
	present := map[string]bool{}
	e, log := fakeEnv(present, "Character devices:\n  1 mem\n248 dcinv\n")
	e.insmod = func(p string) error { *log = append(*log, "insmod:"+p); return nil } // node never appears
	// module already loaded (dcinv in /proc/devices) -> no insmod, straight to mknod
	if err := ensureDcinv(e, "/slot", nil); err != nil {
		t.Fatal(err)
	}
	if len(*log) != 1 || (*log)[0] != "mknod:"+DcinvPath {
		t.Fatalf("log %v", *log)
	}
}

func TestEnsureDcinvInsmodFails(t *testing.T) {
	e, _ := fakeEnv(map[string]bool{}, "")
	e.insmod = func(string) error { return errors.New("vermagic") }
	if err := ensureDcinv(e, "/slot", nil); err == nil {
		t.Fatal("expected error")
	}
}
