package agent

import (
	"encoding/json"
	"time"

	"open-sds/ota/internal/buildinfo"
	"open-sds/ota/internal/sysinfo"

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

func (a *Agent) heartbeat() map[string]any {
	st := a.st.get()
	a.appMu.Lock()
	app := a.app
	a.appMu.Unlock()
	return map[string]any{
		"device":     a.cfg.DeviceID,
		"seq":        a.hbSeq.Add(1),
		"time_unix":  time.Now().Unix(),
		"uptime_s":   int64(time.Since(a.started).Seconds()),
		"taken_over": st.TakenOver,
		"agent_slot": a.AgentSlot(),
		"app":        app,
		"watchdog":   a.wd.Status(),
		"ips":        sysinfo.IPv4s(),
	}
}
