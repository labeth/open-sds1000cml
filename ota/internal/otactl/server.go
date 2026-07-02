package otactl

import (
	"fmt"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
)

// RunServer starts an embedded NATS server the devices connect back to. This
// is the simplest zero-dependency broker for the lab: run `otactl serve` on
// the host, point OTA_NATS at it (nats://<host>:4222), and every device that
// boots the stick shows up. It blocks until interrupted.
func RunServer(host string, port int, printBanner func(string)) error {
	opts := &natsserver.Options{
		Host:      host,
		Port:      port,
		JetStream: false,
		NoSigs:    true,
		Logtime:   true,
	}
	ns, err := natsserver.NewServer(opts)
	if err != nil {
		return fmt.Errorf("nats-server: %w", err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(10 * time.Second) {
		return fmt.Errorf("nats-server not ready")
	}
	if printBanner != nil {
		printBanner(ns.ClientURL())
	}
	ns.WaitForShutdown()
	return nil
}
