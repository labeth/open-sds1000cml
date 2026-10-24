// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT
package agent

import (
	"encoding/json"
	"time"

	"open-sds/ota/internal/buildinfo"
	"open-sds/ota/internal/sysinfo"
	"open-sds/ota/internal/watchdog"

	"github.com/nats-io/nats.go"
)

// NATS subject scheme (deviceID = OTA_DEVICE_ID):
//
//	ota.<id>.rpc        request/reply — one Request in, one Response out
//	ota.all.rpc         broadcast request/reply (fleet-wide; reply per device)
//	ota.<id>.event      agent-pushed lifecycle events (takeover, rollback…)
//	ota.<id>.heartbeat  periodic liveness + status summary
//	ota.discover        request/reply — every device answers with its id/ip
//
// The link auto-reconnects forever; on each (re)connect it re-subscribes and
// publishes an "online" event so the host learns the device is reachable.
// TRLC-LINKS: REQ-SDS-108
func (a *Agent) runNATS() {
	opts := []nats.Option{
		nats.Name("ota-agent/" + a.cfg.DeviceID),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		nats.ReconnectBufSize(-1),
		nats.PingInterval(20 * time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			a.log.Printf("nats disconnected: %v", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			a.log.Printf("nats reconnected to %s", nc.ConnectedUrl())
		}),
	}
	if a.cfg.CAFile != "" {
		opts = append(opts, nats.RootCAs(a.cfg.CAFile))
	}
	if a.cfg.CredsFile != "" {
		opts = append(opts, nats.UserCredentials(a.cfg.CredsFile))
	}
	if a.cfg.NKeyFile != "" {
		if o, err := nats.NkeyOptionFromSeed(a.cfg.NKeyFile); err == nil {
			opts = append(opts, o)
		} else {
			a.log.Printf("nats nkey: %v", err)
		}
	}

	for {
		select {
		case <-a.stopped:
			return
		default:
		}
		nc, err := nats.Connect(a.cfg.NATSURL, opts...)
		if err != nil {
			a.log.Printf("nats connect %s: %v (retry in 5s)", a.cfg.NATSURL, err)
			select {
			case <-a.stopped:
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}
		a.log.Printf("nats connected to %s", nc.ConnectedUrl())
		a.serveNATS(nc)
		nc.Drain()
		return
	}
}

// TRLC-LINKS: REQ-SDS-108
func (a *Agent) serveNATS(nc *nats.Conn) {
	reply := func(m *nats.Msg) {
		resp := a.DispatchJSON(m.Data)
		if m.Reply != "" {
			_ = nc.Publish(m.Reply, resp)
		}
	}
	if _, err := nc.Subscribe("ota."+a.cfg.DeviceID+".rpc", reply); err != nil {
		a.log.Printf("nats subscribe rpc: %v", err)
	}
	if _, err := nc.QueueSubscribe("ota.all.rpc", "", reply); err != nil {
		a.log.Printf("nats subscribe all.rpc: %v", err)
	}
	nc.Subscribe("ota.discover", func(m *nats.Msg) {
		if m.Reply != "" {
			b, _ := json.Marshal(map[string]any{
				"device": a.cfg.DeviceID, "ips": sysinfo.IPv4s(),
				"version": buildinfo.String(), "agent_slot": a.AgentSlot(),
			})
			_ = nc.Publish(m.Reply, b)
		}
	})

	// Push events onto the wire.
	a.setEventFn(func(kind string, detail map[string]any) {
		b, _ := json.Marshal(map[string]any{
			"device": a.cfg.DeviceID, "kind": kind, "detail": detail,
			"time_unix": time.Now().Unix(),
		})
		_ = nc.Publish("ota."+a.cfg.DeviceID+".event", b)
	})
	defer a.setEventFn(nil)

	// Online announcement + heartbeat.
	a.event("online", map[string]any{"version": buildinfo.String(), "ips": sysinfo.IPv4s()})
	hb := time.NewTicker(a.cfg.HBInterval)
	defer hb.Stop()
	for {
		select {
		case <-a.stopped:
			return
		case <-hb.C:
			b, _ := json.Marshal(a.heartbeat())
			_ = nc.Publish("ota."+a.cfg.DeviceID+".heartbeat", b)
		}
	}
}

// heartbeatMsg is a heartbeat's fields, encoded from a struct rather than a
// map: no sort of the keys and no boxing of each value every interval.
// TRLC-LINKS: REQ-SDS-108
type heartbeatMsg struct {
	Device    string          `json:"device"`
	Seq       int64           `json:"seq"`
	TimeUnix  int64           `json:"time_unix"`
	UptimeS   int64           `json:"uptime_s"`
	TakenOver bool            `json:"taken_over"`
	AgentSlot string          `json:"agent_slot"`
	App       appState        `json:"app"`
	Watchdog  watchdog.Status `json:"watchdog"`
	IPs       []string        `json:"ips"`
}

// TRLC-LINKS: REQ-SDS-108
func (a *Agent) heartbeat() heartbeatMsg {
	st := a.st.get()
	a.appMu.Lock()
	app := a.app
	a.appMu.Unlock()
	return heartbeatMsg{
		Device:    a.cfg.DeviceID,
		Seq:       a.hbSeq.Add(1),
		TimeUnix:  time.Now().Unix(),
		UptimeS:   int64(time.Since(a.started).Seconds()),
		TakenOver: st.TakenOver,
		AgentSlot: a.AgentSlot(),
		App:       app,
		Watchdog:  a.wd.Status(),
		IPs:       sysinfo.IPv4s(),
	}
}
