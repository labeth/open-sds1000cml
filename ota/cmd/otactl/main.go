// Command otactl is the host-side controller for the open-sds OTA agents.
//
// It talks to a device either directly over TCP (the agent's OTA_LISTEN
// fallback, default :5900) or through NATS (embedded broker via `otactl
// serve`). Point it at a device with -tcp <ip:port> or -nats <url> -device <id>.
//
// Examples:
//
//	otactl serve                                   # embedded NATS broker for the lab
//	otactl -tcp 192.168.1.209:5900 status
//	otactl -tcp 192.168.1.209:5900 probe --gpmc
//	otactl -tcp 192.168.1.209:5900 takeover --dry-run
//	otactl -tcp 192.168.1.209:5900 update-app ./app-arm
//	otactl -tcp 192.168.1.209:5900 update-agent ./agent-arm
//	otactl power -shelly 192.168.1.223 cycle
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"open-sds/ota/internal/buildinfo"
	"open-sds/ota/internal/otactl"

	"github.com/nats-io/nats.go"
)

func main() {
	var (
		natsURL  = flag.String("nats", envOr("OTA_NATS", ""), "NATS URL (else direct TCP)")
		tcpAddr  = flag.String("tcp", envOr("OTA_TCP", ""), "device TCP address host:port (agent OTA_LISTEN)")
		device   = flag.String("device", envOr("OTA_DEVICE_ID", ""), "device id (NATS transport)")
		timeoutS = flag.Int("timeout", 30, "RPC timeout seconds")
		shellyIP = flag.String("shelly", envOr("OTA_SHELLY", ""), "Shelly plug host for power control")
		stageDir = flag.String("stage", "/tmp", "device staging dir for uploads")
	)
	flag.Usage = usage
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	cmd := args[0]
	rest := args[1:]
	timeout := time.Duration(*timeoutS) * time.Second

	switch cmd {
	case "version":
		fmt.Println("otactl", buildinfo.String())
		return
	case "serve":
		runServe(rest)
		return
	case "power":
		runPower(*shellyIP, rest)
		return
	case "shelly": // alias
		runPower(*shellyIP, rest)
		return
	case "discover":
		runDiscover(*natsURL, timeout)
		return
	case "watch":
		runWatch(*natsURL, *device)
		return
	}

	// Everything else needs a device transport.
	tr, err := dial(*natsURL, *tcpAddr, *device)
	if err != nil {
		fatal(err)
	}
	defer tr.Close()
	c := &otactl.Client{T: tr}

	switch cmd {
	case "ping":
		printJSON(mustCall(c, "ping", nil, timeout))
	case "status":
		printJSON(mustCall(c, "status", nil, timeout))
	case "probe":
		printJSON(mustCall(c, "probe", map[string]any{"read_gpmc": has(rest, "--gpmc")}, timeout))
	case "help", "commands":
		printJSON(mustCall(c, "help", nil, timeout))
	case "logs":
		which := "agent"
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			which = rest[0]
		}
		raw := mustCall(c, "logs", map[string]any{"file": which, "tail": 16384}, timeout)
		var v struct {
			Path string `json:"path"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &v)
		fmt.Printf("== %s ==\n%s\n", v.Path, v.Text)
	case "exec":
		if len(rest) == 0 {
			fatal(fmt.Errorf("exec needs a command"))
		}
		printExec(mustCall(c, "exec", map[string]any{"argv": rest, "timeout_s": *timeoutS}, timeout))
	case "sh":
		printExec(mustCall(c, "sh", map[string]any{"script": strings.Join(rest, " "), "timeout_s": *timeoutS}, timeout))
	case "takeover":
		printJSON(mustCall(c, "takeover", map[string]any{
			"dry_run": has(rest, "--dry-run"), "force": has(rest, "--force"),
		}, timeout))
	case "app":
		if len(rest) == 0 {
			fatal(fmt.Errorf("app needs start|stop|restart"))
		}
		printJSON(mustCall(c, "app."+rest[0], nil, timeout))
	case "activate":
		if len(rest) == 0 {
			fatal(fmt.Errorf("activate needs a slot (A|B)"))
		}
		printJSON(mustCall(c, "app.activate", map[string]any{"slot": rest[0]}, timeout))
	case "reboot":
		printJSON(mustCall(c, "reboot", map[string]any{"confirm": has(rest, "--confirm")}, timeout))
	case "put":
		if len(rest) < 2 {
			fatal(fmt.Errorf("put <local> <device-path>"))
		}
		sum, err := c.PutFile(rest[0], rest[1], 0, 0, progressBar("upload"))
		fmt.Println()
		if err != nil {
			fatal(err)
		}
		fmt.Printf("ok: %s -> %s sha256=%s\n", rest[0], rest[1], sum)
	case "get":
		if len(rest) < 2 {
			fatal(fmt.Errorf("get <device-path> <local>"))
		}
		if err := c.GetFile(rest[0], rest[1], progressBar("download")); err != nil {
			fmt.Println()
			fatal(err)
		}
		fmt.Printf("\nok: %s -> %s\n", rest[0], rest[1])
	case "update-app":
		if len(rest) == 0 {
			fatal(fmt.Errorf("update-app <local-app-binary>"))
		}
		fmt.Printf("uploading %s ...\n", rest[0])
		if _, err := c.PutFile(rest[0], *stageDir+"/app.upload", 0o755, 0, progressBar("upload")); err != nil {
			fmt.Println()
			fatal(err)
		}
		fmt.Println()
		printJSON(mustRaw(c.UpdateApp(rest[0], *stageDir)))
	case "update-agent":
		if len(rest) == 0 {
			fatal(fmt.Errorf("update-agent <local-agent-binary>"))
		}
		fmt.Printf("uploading %s ...\n", rest[0])
		printJSON(mustRaw(c.UpdateAgent(rest[0], *stageDir)))
	case "install-emergency":
		if len(rest) == 0 {
			fatal(fmt.Errorf("install-emergency <local-app-binary>"))
		}
		if _, err := c.PutFile(rest[0], *stageDir+"/emergency.upload", 0o755, 0, progressBar("upload")); err != nil {
			fmt.Println()
			fatal(err)
		}
		fmt.Println()
		printJSON(mustRaw(c.Call("app.install-emergency", map[string]any{"src": *stageDir + "/emergency.upload"}, timeout)))
	default:
		// Fall through: treat as a raw RPC cmd with no args.
		printJSON(mustCall(c, cmd, nil, timeout))
	}
}

func dial(natsURL, tcpAddr, device string) (otactl.Transport, error) {
	if tcpAddr != "" {
		return otactl.NewTCP(tcpAddr), nil
	}
	if natsURL != "" {
		if device == "" {
			return nil, fmt.Errorf("nats transport needs -device")
		}
		return otactl.NewNATS(natsURL, device)
	}
	return nil, fmt.Errorf("no transport: pass -tcp host:port or -nats url -device id")
}

func runServe(rest []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	host := fs.String("host", "0.0.0.0", "bind host")
	port := fs.Int("port", 4222, "NATS port")
	_ = fs.Parse(rest)
	err := otactl.RunServer(*host, *port, func(url string) {
		fmt.Printf("otactl NATS broker up at %s\n", url)
		fmt.Printf("set OTA_NATS=nats://<this-host>:%d in the stick's ota/agent.env\n", *port)
	})
	if err != nil {
		fatal(err)
	}
}

func runPower(shellyIP string, rest []string) {
	// Accept -shelly either before the subcommand (global) or after it (local
	// flagset), so `otactl power -shelly <host> cycle` works as documented.
	fs := flag.NewFlagSet("power", flag.ExitOnError)
	local := fs.String("shelly", "", "Shelly plug host")
	_ = fs.Parse(rest)
	rest = fs.Args()
	if *local != "" {
		shellyIP = *local
	}
	if shellyIP == "" {
		fatal(fmt.Errorf("power needs -shelly <host> (or -shelly before the subcommand, or OTA_SHELLY)"))
	}
	if len(rest) == 0 {
		fatal(fmt.Errorf("power on|off|cycle|state"))
	}
	s := otactl.NewShelly(shellyIP)
	switch rest[0] {
	case "on":
		out, err := s.On()
		report(out, err)
	case "off":
		out, err := s.Off()
		report(out, err)
	case "cycle":
		fmt.Println("power off, waiting 5s, on ...")
		if err := s.Cycle(5 * time.Second); err != nil {
			fatal(err)
		}
		fmt.Println("power cycled")
	case "state":
		on, err := s.State()
		if err != nil {
			fatal(err)
		}
		fmt.Printf("relay: %v\n", map[bool]string{true: "ON", false: "OFF"}[on])
	default:
		fatal(fmt.Errorf("power on|off|cycle|state"))
	}
}

func runDiscover(natsURL string, timeout time.Duration) {
	if natsURL == "" {
		fatal(fmt.Errorf("discover needs -nats"))
	}
	nc, err := nats.Connect(natsURL, nats.Name("otactl-discover"))
	if err != nil {
		fatal(err)
	}
	defer nc.Drain()
	sub, _ := nc.SubscribeSync(nats.NewInbox())
	_ = nc.PublishRequest("ota.discover", sub.Subject, nil)
	deadline := time.Now().Add(timeout)
	fmt.Println("discovering (waiting up to", timeout, ")...")
	n := 0
	for time.Now().Before(deadline) {
		msg, err := sub.NextMsg(time.Until(deadline))
		if err != nil {
			break
		}
		n++
		printJSON(msg.Data)
	}
	fmt.Printf("%d device(s) responded\n", n)
}

func runWatch(natsURL, device string) {
	if natsURL == "" {
		fatal(fmt.Errorf("watch needs -nats"))
	}
	nc, err := nats.Connect(natsURL, nats.Name("otactl-watch"),
		nats.MaxReconnects(-1), nats.ReconnectWait(2*time.Second))
	if err != nil {
		fatal(err)
	}
	defer nc.Drain()
	sel := "*"
	if device != "" {
		sel = device
	}
	fmt.Printf("watching ota.%s.event and ota.%s.heartbeat (Ctrl-C to stop)\n", sel, sel)
	_, _ = nc.Subscribe("ota."+sel+".event", func(m *nats.Msg) {
		fmt.Printf("[event] %s\n", compact(m.Data))
	})
	_, _ = nc.Subscribe("ota."+sel+".heartbeat", func(m *nats.Msg) {
		fmt.Printf("[hb]    %s\n", compact(m.Data))
	})
	select {} // block forever
}

// ---- helpers ---------------------------------------------------------------

func mustCall(c *otactl.Client, cmd string, args any, timeout time.Duration) json.RawMessage {
	raw, err := c.Call(cmd, args, timeout)
	if err != nil {
		if len(raw) > 0 {
			printJSON(raw)
		}
		fatal(err)
	}
	return raw
}

func mustRaw(raw json.RawMessage, err error) json.RawMessage {
	if err != nil {
		if len(raw) > 0 {
			printJSON(raw)
		}
		fatal(err)
	}
	return raw
}

func printExec(raw json.RawMessage) {
	var v struct {
		Exit   int    `json:"exit"`
		Output string `json:"output"`
	}
	_ = json.Unmarshal(raw, &v)
	fmt.Print(v.Output)
	if !strings.HasSuffix(v.Output, "\n") {
		fmt.Println()
	}
	fmt.Printf("[exit %d]\n", v.Exit)
}

func printJSON(raw json.RawMessage) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		fmt.Println(string(raw))
		return
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

func compact(b []byte) string {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return string(b)
	}
	out, _ := json.Marshal(v)
	return string(out)
}

func progressBar(label string) func(done, total int64) {
	last := -1
	return func(done, total int64) {
		if total <= 0 {
			fmt.Printf("\r%s: %d bytes", label, done)
			return
		}
		pct := int(done * 100 / total)
		if pct != last {
			fmt.Printf("\r%s: %d%% (%d/%d)", label, pct, done, total)
			last = pct
		}
	}
}

func report(out string, err error) {
	if err != nil {
		fatal(err)
	}
	fmt.Println(strings.TrimSpace(out))
}

func has(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func usage() {
	fmt.Fprint(os.Stderr, `otactl — open-sds OTA host controller

USAGE
  otactl [global flags] <command> [args]

GLOBAL FLAGS
  -tcp host:port    device TCP control address (agent OTA_LISTEN, e.g. 192.168.1.209:5900)
  -nats url         NATS broker URL (alternative transport)
  -device id        device id (with -nats)
  -shelly host      Shelly power plug host (for the power command)
  -stage dir        device staging dir for uploads (default /tmp)
  -timeout n        RPC timeout seconds (default 30)

COMMANDS
  serve                     run an embedded NATS broker for the lab
  discover                  (nats) list devices that answer
  watch                     (nats) stream events + heartbeats
  status                    device + app + slot + watchdog status
  probe [--gpmc]            read-only device fingerprint
  ping
  logs [agent|boot]         tail a device log
  exec <argv...>            run a command on the device
  sh <script...>            run a /bin/sh script on the device
  takeover [--dry-run|--force]   inherit-then-kill the factory app
  app start|stop|restart    app lifecycle (after takeover)
  activate <A|B>            set active app slot + restart
  update-app <bin>          OTA the app (upload -> inactive slot -> restart)
  update-agent <bin>        OTA the agent itself (A/B agent slot)
  install-emergency <bin>   install the known-good backstop app
  put <local> <devpath>     upload a file
  get <devpath> <local>     download a file
  reboot [--confirm]        soft reboot (often a no-op on this unit)
  power on|off|cycle|state  Shelly mains control (hard power-cycle)
  version
`)
}
