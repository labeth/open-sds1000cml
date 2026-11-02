// ENGMODEL-OWNER-UNIT: FU-OTA-OTACTL

package otactl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Shelly controls a Shelly Gen1 smart plug (HTTP /relay/0). This is the ONLY
// recovery from a GPMC bus wedge or a watchdog warm-reset (which drops USB
// hotplug so the stick never re-enumerates): a real power-off/on cycle.
// TRLC-LINKS: REQ-SDS-112
type Shelly struct {
	Host string // ip or host, no scheme
	HTTP *http.Client
}

// NewShelly returns a client for the plug at host, with a short timeout.
// TRLC-LINKS: REQ-SDS-112
func NewShelly(host string) *Shelly {
	return &Shelly{Host: host, HTTP: &http.Client{Timeout: 8 * time.Second}}
}

// get sends a GET for path on the plug.
// TRLC-LINKS: REQ-SDS-112
func (s *Shelly) get(path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+s.Host+path, nil)
	if err != nil {
		return nil, err
	}
	return s.HTTP.Do(req)
}

// do switches the relay ("on" or "off") and returns the plug's reply.
// TRLC-LINKS: REQ-SDS-112
func (s *Shelly) do(turn string) (string, error) {
	resp, err := s.get("/relay/0?turn=" + turn)
	if err != nil {
		return "", fmt.Errorf("shelly %s: %w", turn, err)
	}
	defer func() { _ = resp.Body.Close() }() // read-only body
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return string(body), fmt.Errorf("shelly %s: HTTP %d", turn, resp.StatusCode)
	}
	return string(body), nil
}

// On switches the relay on.
// TRLC-LINKS: REQ-SDS-112
func (s *Shelly) On() (string, error) { return s.do("on") }

// Off switches the relay off.
// TRLC-LINKS: REQ-SDS-112
func (s *Shelly) Off() (string, error) { return s.do("off") }

// Cycle powers off, waits, powers on — a hard reboot of the instrument.
// TRLC-LINKS: REQ-SDS-112
func (s *Shelly) Cycle(off time.Duration) error {
	if _, err := s.Off(); err != nil {
		return err
	}
	time.Sleep(off)
	_, err := s.On()
	return err
}

// State reports the relay's on/off state.
// TRLC-LINKS: REQ-SDS-112
func (s *Shelly) State() (bool, error) {
	resp, err := s.get("/relay/0")
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }() // read-only body
	var v struct {
		IsOn bool `json:"ison"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return false, err
	}
	return v.IsOn, nil
}
