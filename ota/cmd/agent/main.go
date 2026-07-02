// Command agent is the on-device OTA supervisor. It is launched by the USB
// boot anchor (startup.sh) as a direct child so it inherits the boot-opened
// /dev/Gpmc and /dev/fpga_key descriptors, and it in turn launches the
// clean-room app so the app inherits them too.
//
// Run with no arguments for the supervisor (the boot path). Subcommands are
// local diagnostics runnable on the device without disturbing a running agent:
//
//	agent probe            read-only device fingerprint (JSON)
//	agent status           local status via the running agent's TCP port
//	agent version
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"open-sds/ota/internal/agent"
	"open-sds/ota/internal/buildinfo"
	"open-sds/ota/internal/config"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "-v", "--version":
			fmt.Println("open-sds ota agent", buildinfo.String())
			return
		case "probe":
			cfg := config.Load()
			a := agent.New(cfg)
			rep := a.Probe(hasFlag("--gpmc"))
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(rep)
			return
		case "help", "-h", "--help":
			fmt.Println(usage)
			return
		}
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n%s\n", os.Args[1], usage)
		os.Exit(2)
	}

	cfg := config.Load()
	a := agent.New(cfg)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		s := <-sig
		fmt.Fprintf(os.Stderr, "[agent] signal %v: clean stop (watchdog disarm, app left for adoption)\n", s)
		a.Stop()
	}()

	if err := a.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "[agent] fatal: %v\n", err)
		os.Exit(1)
	}
}

func hasFlag(f string) bool {
	for _, a := range os.Args[2:] {
		if a == f {
			return true
		}
	}
	return false
}

const usage = `open-sds OTA agent
  agent                 run the supervisor (boot path)
  agent probe [--gpmc]  read-only device fingerprint as JSON (--gpmc: also read version+fill)
  agent version         print version
  agent help            this help`
