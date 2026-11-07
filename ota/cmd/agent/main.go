// Command agent is the on-device OTA supervisor: the core supervisor
// (open-dso-core/supervisor/agent) on the SDS1102CML+ board. It is launched by the USB
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
//
// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"github.com/labeth/open-dso-core/supervisor/agent"
	"github.com/labeth/open-dso-core/supervisor/config"

	"github.com/labeth/open-sds1000cml/ota/internal/board"
	"github.com/labeth/open-sds1000cml/ota/internal/buildinfo"
)

// TRLC-LINKS: REQ-SDS-124
func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "-v", "--version":
			fmt.Println("open-sds ota agent", buildinfo.String())
			return
		case "probe":
			a := newAgent()
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

	a := newAgent()

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

// newAgent is the core supervisor on this board, configured from the
// environment startup.sh exports.
// TRLC-LINKS: REQ-SDS-124
func newAgent() *agent.Agent {
	cfg := config.Load()
	return agent.New(cfg, board.New(cfg, board.LoadConfig()), buildinfo.String())
}

// TRLC-LINKS: REQ-SDS-124
func hasFlag(f string) bool {
	return slices.Contains(os.Args[2:], f)
}

const usage = `open-sds OTA agent, usage:
  agent                 run the supervisor (boot path)
  agent probe [--gpmc]  read-only device fingerprint as JSON (--gpmc: also read version+fill)
  agent version         print version
  agent help            this help`
