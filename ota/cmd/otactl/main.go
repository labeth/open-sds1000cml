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
//
// ENGMODEL-OWNER-UNIT: FU-OTA-OTACTL
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"open-sds/ota/internal/buildinfo"
	"open-sds/ota/internal/otactl"
	"open-sds/ota/internal/vxi11"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
)

// defaultStageDir is where uploads stage on the scope: the agent's staging
// directory on the stick (mounted at U-disk0), the one writable file system,
// as the scope's root file system, /tmp included, is read-only.
const defaultStageDir = "/usr/bin/siglent/usr/media/U-disk0/agent-slots/staging"

// TRLC-LINKS: REQ-SDS-109, REQ-SDS-110, REQ-SDS-111, REQ-SDS-112, REQ-SDS-175
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// cli is one invocation: the global flags and where output goes. Results go
// to stdout; progress and errors go to stderr, so a result pipes cleanly.
// TRLC-LINKS: REQ-SDS-175
type cli struct {
	natsURL, tcpAddr, device, shellyIP, stageDir string
	timeoutS                                     int
	timeout                                      time.Duration
	out, errOut                                  io.Writer
}

// usageError is a command used wrongly: it exits 2, a failure exits 1.
// TRLC-LINKS: REQ-SDS-175
type usageError struct{ msg string }

// TRLC-LINKS: REQ-SDS-175
func (e usageError) Error() string { return e.msg }

// localCommands run without a device transport; every other command needs one.
var localCommands = map[string]func(c *cli, rest []string) error{
	"version":  cmdVersion,
	"serve":    cmdServe,
	"power":    cmdPower,
	"shelly":   cmdPower, // alias
	"discover": cmdDiscover,
	"watch":    cmdWatch,
	"scpi":     cmdScpi,
}

// deviceCommands are the commands that talk to one device's agent. A command
// in neither table is sent to the agent as a raw RPC with no arguments.
var deviceCommands = map[string]func(c *cli, dc *otactl.Client, rest []string) error{
	"ping":            rpcNoArgs("ping"),
	"status":          rpcNoArgs("status"),
	"help":            rpcNoArgs("help"),
	"commands":        rpcNoArgs("help"),
	"untakeover":      rpcNoArgs("untakeover"),
	"probe":           cmdProbe,
	"logs":            cmdLogs,
	"exec":            cmdExec,
	"sh":              cmdSh,
	"takeover":        cmdTakeover,
	"restore-factory": cmdRestoreFactory,
	"app":             cmdApp,
	"activate":        cmdActivate,
	"reboot":          cmdReboot,
	"put":             cmdPut,
	"get":             cmdGet,
	"update-app":      cmdUpdateApp,
	"update-agent":    cmdUpdateAgent,
}

// run parses the global flags, runs one command and returns the exit code.
// TRLC-LINKS: REQ-SDS-109, REQ-SDS-175
func run(args []string, stdout, stderr io.Writer) int {
	c := &cli{out: stdout, errOut: stderr}
	fs := flag.NewFlagSet("otactl", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&c.natsURL, "nats", envOr("OTA_NATS", ""), "NATS URL (else direct TCP)")
	fs.StringVar(&c.tcpAddr, "tcp", envOr("OTA_TCP", ""), "device TCP address host:port (agent OTA_LISTEN)")
	fs.StringVar(&c.device, "device", envOr("OTA_DEVICE_ID", ""), "device id (NATS transport)")
	fs.IntVar(&c.timeoutS, "timeout", 30, "RPC timeout seconds")
	fs.StringVar(&c.shellyIP, "shelly", envOr("OTA_SHELLY", ""), "Shelly plug host for power control")
	fs.StringVar(&c.stageDir, "stage", defaultStageDir, "device staging dir for uploads")
	fs.Usage = func() { usage(stderr) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() == 0 {
		usage(stderr)
		return 2
	}
	c.timeout = time.Duration(c.timeoutS) * time.Second
	if err := c.dispatch(fs.Arg(0), fs.Args()[1:]); err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", err)
		if _, ok := errors.AsType[usageError](err); ok {
			return 2
		}
		return 1
	}
	return 0
}

// dispatch runs one command: a local one directly, anything else over a
// transport to the device, closed again when the command returns.
// TRLC-LINKS: REQ-SDS-109, REQ-SDS-175
func (c *cli) dispatch(cmd string, rest []string) error {
	if fn, ok := localCommands[cmd]; ok {
		return fn(c, rest)
	}
	tr, err := dial(c.natsURL, c.tcpAddr, c.device)
	if err != nil {
		return err
	}
	defer tr.Close()
	dc := &otactl.Client{T: tr}
	if fn, ok := deviceCommands[cmd]; ok {
		return fn(c, dc, rest)
	}
	return c.callPrint(dc, cmd, nil)
}

// rpcNoArgs is a command that sends one RPC without arguments and prints
// the reply.
// TRLC-LINKS: REQ-SDS-109, REQ-SDS-175
func rpcNoArgs(rpc string) func(*cli, *otactl.Client, []string) error {
	return func(c *cli, dc *otactl.Client, _ []string) error { return c.callPrint(dc, rpc, nil) }
}

// TRLC-LINKS: REQ-SDS-175
func cmdVersion(c *cli, _ []string) error {
	_, err := fmt.Fprintln(c.out, "otactl", buildinfo.String())
	return err
}

// TRLC-LINKS: REQ-SDS-114, REQ-SDS-175
func cmdProbe(c *cli, dc *otactl.Client, rest []string) error {
	return c.callPrint(dc, "probe", map[string]any{"read_gpmc": has(rest, "--gpmc")})
}

// TRLC-LINKS: REQ-SDS-115, REQ-SDS-175
func cmdLogs(c *cli, dc *otactl.Client, rest []string) error {
	which := "agent"
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		which = rest[0]
	}
	raw, err := c.call(dc, "logs", map[string]any{"file": which, "tail": 16384})
	if err != nil {
		return err
	}
	var v struct {
		Path string `json:"path"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("logs: %w", err)
	}
	_, err = fmt.Fprintf(c.out, "== %s ==\n%s\n", v.Path, v.Text)
	return err
}

// TRLC-LINKS: REQ-SDS-115, REQ-SDS-175
func cmdExec(c *cli, dc *otactl.Client, rest []string) error {
	if len(rest) == 0 {
		return usageError{"exec needs a command"}
	}
	raw, err := c.call(dc, "exec", map[string]any{"argv": rest, "timeout_s": c.timeoutS})
	if err != nil {
		return err
	}
	return c.printExec(raw)
}

// TRLC-LINKS: REQ-SDS-115, REQ-SDS-175
func cmdSh(c *cli, dc *otactl.Client, rest []string) error {
	raw, err := c.call(dc, "sh", map[string]any{"script": strings.Join(rest, " "), "timeout_s": c.timeoutS})
	if err != nil {
		return err
	}
	return c.printExec(raw)
}

// TRLC-LINKS: REQ-SDS-027, REQ-SDS-175
func cmdTakeover(c *cli, dc *otactl.Client, rest []string) error {
	return c.callPrint(dc, "takeover", map[string]any{
		"dry_run": has(rest, "--dry-run"), "force": has(rest, "--force"),
	})
}

// cmdRestoreFactory takes optional JSON choosing the console the vendor UI
// is launched on, e.g. restore-factory '{"console":"/dev/console","setsid":true}'.
// TRLC-LINKS: REQ-SDS-120, REQ-SDS-175
func cmdRestoreFactory(c *cli, dc *otactl.Client, rest []string) error {
	var rfArgs any
	if len(rest) > 0 && rest[0] != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(rest[0]), &m); err != nil {
			return usageError{fmt.Sprintf("restore-factory: args must be JSON: %v", err)}
		}
		rfArgs = m
	}
	return c.callPrint(dc, "restore-factory", rfArgs)
}

// TRLC-LINKS: REQ-SDS-117, REQ-SDS-175
func cmdApp(c *cli, dc *otactl.Client, rest []string) error {
	if len(rest) == 0 {
		return usageError{"app needs start|stop|restart"}
	}
	return c.callPrint(dc, "app."+rest[0], nil)
}

// TRLC-LINKS: REQ-SDS-118, REQ-SDS-175
func cmdActivate(c *cli, dc *otactl.Client, rest []string) error {
	if len(rest) == 0 {
		return usageError{"activate needs a slot (A|B)"}
	}
	return c.callPrint(dc, "app.activate", map[string]any{"slot": rest[0]})
}

// TRLC-LINKS: REQ-SDS-121, REQ-SDS-175
func cmdReboot(c *cli, dc *otactl.Client, rest []string) error {
	return c.callPrint(dc, "reboot", map[string]any{"confirm": has(rest, "--confirm")})
}

// TRLC-LINKS: REQ-SDS-110, REQ-SDS-175
func cmdPut(c *cli, dc *otactl.Client, rest []string) error {
	if len(rest) < 2 {
		return usageError{"put <local> <device-path>"}
	}
	sum, err := dc.PutFile(rest[0], rest[1], 0, 0, c.progressBar("upload"))
	_, _ = fmt.Fprintln(c.errOut)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(c.out, "ok: %s -> %s sha256=%s\n", rest[0], rest[1], sum)
	return err
}

// TRLC-LINKS: REQ-SDS-110, REQ-SDS-175
func cmdGet(c *cli, dc *otactl.Client, rest []string) error {
	if len(rest) < 2 {
		return usageError{"get <device-path> <local>"}
	}
	err := dc.GetFile(rest[0], rest[1], c.progressBar("download"))
	_, _ = fmt.Fprintln(c.errOut)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(c.out, "ok: %s -> %s\n", rest[0], rest[1])
	return err
}

// TRLC-LINKS: REQ-SDS-110, REQ-SDS-118, REQ-SDS-175
func cmdUpdateApp(c *cli, dc *otactl.Client, rest []string) error {
	if len(rest) == 0 {
		return usageError{"update-app <local-app-binary>"}
	}
	_, _ = fmt.Fprintf(c.errOut, "uploading %s ...\n", rest[0])
	_, err := dc.PutFile(rest[0], c.stageDir+"/app.upload", 0o755, 0, c.progressBar("upload"))
	_, _ = fmt.Fprintln(c.errOut)
	if err != nil {
		return err
	}
	return c.printRaw(dc.UpdateApp(rest[0], c.stageDir))
}

// TRLC-LINKS: REQ-SDS-110, REQ-SDS-119, REQ-SDS-175
func cmdUpdateAgent(c *cli, dc *otactl.Client, rest []string) error {
	if len(rest) == 0 {
		return usageError{"update-agent <local-agent-binary>"}
	}
	_, _ = fmt.Fprintf(c.errOut, "uploading %s ...\n", rest[0])
	return c.printRaw(dc.UpdateAgent(rest[0], c.stageDir))
}

// TRLC-LINKS: REQ-SDS-109, REQ-SDS-175
func dial(natsURL, tcpAddr, device string) (otactl.Transport, error) {
	if tcpAddr != "" {
		return otactl.NewTCP(tcpAddr), nil
	}
	if natsURL != "" {
		if device == "" {
			return nil, usageError{"nats transport needs -device"}
		}
		return otactl.NewNATS(natsURL, device)
	}
	return nil, usageError{"no transport: pass -tcp host:port or -nats url -device id"}
}

// TRLC-LINKS: REQ-SDS-111, REQ-SDS-175
func cmdServe(c *cli, rest []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(c.errOut)
	host := fs.String("host", "0.0.0.0", "bind host")
	port := fs.Int("port", 4222, "NATS port")
	if err := fs.Parse(rest); err != nil {
		return usageError{err.Error()}
	}
	return otactl.RunServer(*host, *port, func(url string) {
		_, _ = fmt.Fprintf(c.out, "otactl NATS broker up at %s\n", url)
		_, _ = fmt.Fprintf(c.out, "set OTA_NATS=nats://<this-host>:%d in the stick's ota/agent.env\n", *port)
	})
}

// cmdPower accepts -shelly either before the subcommand (global) or after it
// (local flag set), so `otactl power -shelly <host> cycle` works as documented.
// TRLC-LINKS: REQ-SDS-112, REQ-SDS-175
func cmdPower(c *cli, rest []string) error {
	fs := flag.NewFlagSet("power", flag.ContinueOnError)
	fs.SetOutput(c.errOut)
	local := fs.String("shelly", "", "Shelly plug host")
	if err := fs.Parse(rest); err != nil {
		return usageError{err.Error()}
	}
	rest = fs.Args()
	shellyIP := c.shellyIP
	if *local != "" {
		shellyIP = *local
	}
	if shellyIP == "" {
		return usageError{"power needs -shelly <host> (or -shelly before the subcommand, or OTA_SHELLY)"}
	}
	if len(rest) == 0 {
		return usageError{"power on|off|cycle|state"}
	}
	s := otactl.NewShelly(shellyIP)
	switch rest[0] {
	case "on":
		return c.report(s.On())
	case "off":
		return c.report(s.Off())
	case "cycle":
		_, _ = fmt.Fprintln(c.errOut, "power off, waiting 5s, on ...")
		if err := s.Cycle(5 * time.Second); err != nil {
			return err
		}
		_, err := fmt.Fprintln(c.out, "power cycled")
		return err
	case "state":
		on, err := s.State()
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(c.out, "relay: %v\n", map[bool]string{true: "ON", false: "OFF"}[on])
		return err
	default:
		return usageError{"power on|off|cycle|state"}
	}
}

// cmdScpi speaks VXI-11 SCPI directly to the instrument (host:111 ->
// DEVICE_CORE): the vendor factory app or a clean-room app. It validates the
// VXI-11 client and stops or resumes the factory app non-destructively. A
// command carrying '?' is a query and its reply is printed. The link is
// always destroyed, a failed command too: the instrument serves one link.
// TRLC-LINKS: REQ-SDS-175
func cmdScpi(c *cli, args []string) error {
	host := hostOf(c.tcpAddr)
	if host == "" {
		return usageError{"scpi needs a device host (pass -tcp <ip>:<port>)"}
	}
	if len(args) == 0 {
		return usageError{"scpi <command>   e.g. scpi \"*IDN?\"  or  scpi STOP"}
	}
	return scpiAt(c, func() (*vxi11.Client, error) { return vxi11.Dial(host, c.timeout) }, strings.Join(args, " "))
}

// scpiAt runs one SCPI command on a link from dial.
// TRLC-LINKS: REQ-SDS-175
func scpiAt(c *cli, dial func() (*vxi11.Client, error), cmd string) error {
	cl, err := dial()
	if err != nil {
		return err
	}
	defer cl.Close()
	// A query is any command carrying "?" -- "C1:WF? DAT2" and "WFSU?" alike
	// (the waveform queries put their arguments after the question mark).
	if strings.Contains(cmd, "?") {
		resp, err := cl.Query(cmd)
		if err != nil {
			return err
		}
		if !strings.HasSuffix(resp, "\n") {
			resp += "\n"
		}
		_, err = fmt.Fprint(c.out, resp)
		return err
	}
	if err := cl.Send(cmd); err != nil {
		return err
	}
	_, err = fmt.Fprintf(c.out, "sent: %s\n", cmd)
	return err
}

// TRLC-LINKS: REQ-SDS-175
func hostOf(addr string) string {
	if addr == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// TRLC-LINKS: REQ-SDS-175
func cmdDiscover(c *cli, _ []string) error {
	if c.natsURL == "" {
		return usageError{"discover needs -nats"}
	}
	nc, err := nats.Connect(c.natsURL, nats.Name("otactl-discover"))
	if err != nil {
		return err
	}
	defer func() { _ = nc.Drain() }() // the replies are read; nothing is pending
	sub, err := nc.SubscribeSync(nats.NewInbox())
	if err != nil {
		return err
	}
	if err := nc.PublishRequest("ota.discover", sub.Subject, nil); err != nil {
		return err
	}
	deadline := time.Now().Add(c.timeout)
	_, _ = fmt.Fprintln(c.errOut, "discovering (waiting up to", c.timeout, ")...")
	n := 0
	for time.Now().Before(deadline) {
		msg, err := sub.NextMsg(time.Until(deadline))
		if err != nil {
			break
		}
		n++
		c.printJSON(msg.Data)
	}
	_, err = fmt.Fprintf(c.out, "%d device(s) responded\n", n)
	return err
}

// cmdWatch prints the events and heartbeats until interrupted.
// TRLC-LINKS: REQ-SDS-175
func cmdWatch(c *cli, _ []string) error {
	if c.natsURL == "" {
		return usageError{"watch needs -nats"}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	nc, err := nats.Connect(c.natsURL, nats.Name("otactl-watch"),
		nats.MaxReconnects(-1), nats.ReconnectWait(2*time.Second))
	if err != nil {
		return err
	}
	defer func() { _ = nc.Drain() }() // printing only: nothing to flush
	sel := "*"
	if c.device != "" {
		sel = c.device
	}
	_, _ = fmt.Fprintf(c.errOut, "watching ota.%s.event and ota.%s.heartbeat (Ctrl-C to stop)\n", sel, sel)
	for subj, tag := range map[string]string{"event": "[event]", "heartbeat": "[hb]   "} {
		if _, err := nc.Subscribe("ota."+sel+"."+subj, func(m *nats.Msg) {
			_, _ = fmt.Fprintf(c.out, "%s %s\n", tag, compact(m.Data))
		}); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return nil
}

// ---- helpers ---------------------------------------------------------------

// call runs one RPC; a device-side failure's data is printed before the
// error is returned.
// TRLC-LINKS: REQ-SDS-109, REQ-SDS-175
func (c *cli) call(dc *otactl.Client, cmd string, args any) (json.RawMessage, error) {
	raw, err := dc.Call(cmd, args, c.timeout)
	if err != nil && len(raw) > 0 {
		c.printJSON(raw)
	}
	return raw, err
}

// callPrint runs one RPC and prints its reply.
// TRLC-LINKS: REQ-SDS-109, REQ-SDS-175
func (c *cli) callPrint(dc *otactl.Client, cmd string, args any) error {
	raw, err := c.call(dc, cmd, args)
	if err != nil {
		return err
	}
	c.printJSON(raw)
	return nil
}

// printRaw prints a multi-step operation's final reply, or its partial
// reply and the error.
// TRLC-LINKS: REQ-SDS-175
func (c *cli) printRaw(raw json.RawMessage, err error) error {
	if len(raw) > 0 {
		c.printJSON(raw)
	}
	return err
}

// TRLC-LINKS: REQ-SDS-175
func (c *cli) printExec(raw json.RawMessage) error {
	var v struct {
		Exit   int    `json:"exit"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("bad exec reply: %w", err)
	}
	out := v.Output
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	_, err := fmt.Fprintf(c.out, "%s[exit %d]\n", out, v.Exit)
	return err
}

// printJSON pretty-prints a reply with its keys sorted, or prints it as it
// came when it is not JSON.
// TRLC-LINKS: REQ-SDS-175
func (c *cli) printJSON(raw json.RawMessage) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		_, _ = fmt.Fprintln(c.out, string(raw))
		return
	}
	b, _ := json.MarshalIndent(v, "", "  ") // re-encoding a decoded value cannot fail
	_, _ = fmt.Fprintln(c.out, string(b))
}

// TRLC-LINKS: REQ-SDS-175
func compact(b []byte) string {
	// Whitespace out in one pass, the agent's key order kept: no decode
	// into maps and encode again.
	var out bytes.Buffer
	if json.Compact(&out, b) != nil {
		return string(b)
	}
	return out.String()
}

// progressBar reports a transfer's progress on stderr, rewriting one line.
// TRLC-LINKS: REQ-SDS-175
func (c *cli) progressBar(label string) func(done, total int64) {
	last := -1
	return func(done, total int64) {
		if total <= 0 {
			_, _ = fmt.Fprintf(c.errOut, "\r%s: %d bytes", label, done)
			return
		}
		pct := int(done * 100 / total)
		if pct != last {
			_, _ = fmt.Fprintf(c.errOut, "\r%s: %d%% (%d/%d)", label, pct, done, total)
			last = pct
		}
	}
}

// TRLC-LINKS: REQ-SDS-175
func (c *cli) report(out string, err error) error {
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(c.out, strings.TrimSpace(out))
	return err
}

// TRLC-LINKS: REQ-SDS-175
func has(args []string, flag string) bool {
	return slices.Contains(args, flag)
}

// TRLC-LINKS: REQ-SDS-175
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// TRLC-LINKS: REQ-SDS-175
func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, `otactl — open-sds OTA host controller

USAGE
  otactl [global flags] <command> [args]

GLOBAL FLAGS
  -tcp host:port    device TCP control address (agent OTA_LISTEN, e.g. 192.168.1.209:5900)
  -nats url         NATS broker URL (alternative transport)
  -device id        device id (with -nats)
  -shelly host      Shelly power plug host (for the power command)
  -stage dir        device staging dir for uploads (default the stick's
                    /usr/bin/siglent/usr/media/U-disk0/agent-slots/staging)
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
  untakeover                release control (clear taken_over + disarm wd)
  restore-factory [json]    re-launch the vendor app in place (post-test); optional
                            {"console":"/dev/console","setsid":true,"inherit":true,"no_pgid":true}
  scpi <cmd>                raw VXI-11 SCPI to the instrument (e.g. scpi "*IDN?")
  app start|stop|restart    app lifecycle (after takeover)
  activate <A|B>            set active app slot + restart
  update-app <bin>          OTA the app (upload -> inactive slot -> restart)
  update-agent <bin>        OTA the agent itself (A/B agent slot)
  put <local> <devpath>     upload a file
  get <devpath> <local>     download a file
  reboot [--confirm]        soft reboot (often a no-op on this unit)
  power on|off|cycle|state  Shelly mains control (hard power-cycle)
  version
`)
}
