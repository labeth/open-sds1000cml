package otactl

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Shelly controls a Shelly Gen1 smart plug (HTTP /relay/0). This is the ONLY
// recovery from a GPMC bus wedge or a watchdog warm-reset (which drops USB
// hotplug so the stick never re-enumerates): a real power-off/on cycle.
type Shelly struct {
	Host string // ip or host, no scheme
	HTTP *http.Client
}

func NewShelly(host string) *Shelly {
	return &Shelly{Host: host, HTTP: &http.Client{Timeout: 8 * time.Second}}
}

func (s *Shelly) do(turn string) (string, error) {
	url := fmt.Sprintf("http://%s/relay/0?turn=%s", s.Host, turn)
	resp, err := s.HTTP.Get(url)
	if err != nil {
		return "", fmt.Errorf("shelly %s: %w", turn, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != 200 {
		return string(body), fmt.Errorf("shelly %s: HTTP %d", turn, resp.StatusCode)
	}
	return string(body), nil
}

func (s *Shelly) On() (string, error)  { return s.do("on") }
func (s *Shelly) Off() (string, error) { return s.do("off") }

// Cycle powers off, waits, powers on — a hard reboot of the instrument.
func (s *Shelly) Cycle(off time.Duration) error {
	if _, err := s.Off(); err != nil {
		return err
	}
	time.Sleep(off)
	_, err := s.On()
	return err
}

// State reports the relay's on/off state.
func (s *Shelly) State() (bool, error) {
	resp, err := s.HTTP.Get(fmt.Sprintf("http://%s/relay/0", s.Host))
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	var v struct {
		IsOn bool `json:"ison"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return false, err
	}
	return v.IsOn, nil
}
