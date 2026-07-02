// Command stubapp is the REFERENCE "clean-room app" for the OTA contract, and
// the vehicle for validating takeover end-to-end without the real acquisition
// app existing yet.
//
// It demonstrates the ENTIRE contract the future app must satisfy — and nothing
// more, so app development never has to touch the OTA agent:
//
//  1. It is launched by the agent as a direct child, so it INHERITS the boot
//     file descriptors. It discovers /dev/Gpmc and /dev/fpga_key by scanning
//     /proc/self/fd (never fresh-opens them) and reports what it found.
//  2. It reads its runtime contract from the environment the agent exports:
//     OTA_HEALTH_PATH, SCOPE_GPMC, SCOPE_LCD, SCOPE_MMAP_DRAIN.
//  3. It reports liveness by re-writing the health token at OTA_HEALTH_PATH on
//     a frame-advance cadence (throttled ~400ms). The FIRST write is gated on
//     "genuine capture" — here simulated after a short warmup — so the agent's
//     health watcher only marks it healthy once it is really advancing.
//  4. It exits cleanly (0) on SIGTERM/SIGINT so the agent's stop path is clean.
//
// It deliberately does NOT touch the GPMC bus (it only holds the inherited
// fds). That keeps this test safe: it proves the OTA launch/supervise/health/
// watchdog machinery without driving the display. The real app replaces exactly
// this surface — same fds, same env, same health file — and adds the actual
// acquisition/render engine. It needs no knowledge of takeover, watchdog, A/B
// slots, or rollback: the agent owns all of that.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func findInheritedFD(path string) int {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	for _, e := range entries {
		fd, err := strconv.Atoi(e.Name())
		if err != nil || fd < 3 {
			continue
		}
		if t, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name())); err == nil && t == path {
			return fd
		}
	}
	return -1
}

func main() {
	log := func(f string, a ...any) { fmt.Printf("[stubapp] "+f+"\n", a...) }

	healthPath := os.Getenv("OTA_HEALTH_PATH")
	gpmcDev := envOr("SCOPE_GPMC", "/dev/Gpmc")
	keyDev := "/dev/fpga_key"

	gpmcFD := findInheritedFD(gpmcDev)
	keyFD := findInheritedFD(keyDev)
	log("start pid=%d", os.Getpid())
	log("inherited %s fd=%d  %s fd=%d", gpmcDev, gpmcFD, keyDev, keyFD)
	log("env OTA_HEALTH_PATH=%q SCOPE_LCD=%q SCOPE_MMAP_DRAIN=%q",
		healthPath, os.Getenv("SCOPE_LCD"), os.Getenv("SCOPE_MMAP_DRAIN"))

	if gpmcFD < 0 {
		// The real app would report unhealthy and refuse to drive here (spec 01
		// §5.2). The stub keeps running so the takeover test can still observe
		// the supervise loop, but it shouts about it.
		log("WARNING: no inherited /dev/Gpmc fd — on the real app this is a hard fault")
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)

	// Warmup: do NOT write the health token before "genuine capture" — the
	// agent's first-report gate depends on the token being absent until the
	// engine is really advancing. Simulate a sub-second warmup.
	warmup := time.NewTimer(600 * time.Millisecond)
	var healthy bool
	var frames uint64

	tick := time.NewTicker(400 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case s := <-sig:
			log("signal %v — clean exit", s)
			return
		case <-warmup.C:
			healthy = true
			log("warmup complete — reporting healthy")
		case <-tick.C:
			frames++
			if !healthy {
				continue
			}
			// Re-write (touch) the health token on frame advance. Content
			// changes each write so a same-second mtime still counts.
			tok := fmt.Sprintf("frames=%d ts=%d\n", frames, time.Now().UnixNano())
			tmp := healthPath + ".tmp"
			if err := os.WriteFile(tmp, []byte(tok), 0o644); err == nil {
				_ = os.Rename(tmp, healthPath)
			}
		}
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
