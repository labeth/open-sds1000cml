package lcd

import (
	"net"
	"sync"
	"time"
)

// Device-URL discovery for the on-screen support line: after a takeover the
// #1 question is "what address do I browse to?" — so the HUD shows
// "http://<ip>:8080" (the app's fixed control-page port, cmd/app main.go).
// The IP is the first global-unicast IPv4 the kernel reports; loopback,
// link-local (169.254/16 — a failed DHCP) and IPv6 never make a URL a LAN
// browser can be told over the phone.

// interfaceAddrs is the enumeration seam — tests inject address lists.
var interfaceAddrs = net.InterfaceAddrs

// PickDeviceURL returns "http://<ip>:8080" for the first global-unicast IPv4
// in addrs, or "" when the device has no reachable address (render nothing).
func PickDeviceURL(addrs []net.Addr) string {
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		default:
			continue
		}
		ip4 := ip.To4()
		if ip4 == nil || !ip.IsGlobalUnicast() {
			continue
		}
		return "http://" + ip4.String() + ":8080"
	}
	return ""
}

// urlRefresh: how stale the cached answer may go before the interfaces are
// enumerated again — cheap enough for every HUD build (~20 Hz across the LCD
// loop + screenshot paths), fresh enough that a DHCP lease appearing after
// boot shows up within seconds.
const urlRefresh = 5 * time.Second

var (
	urlMu  sync.Mutex
	urlVal string
	urlAt  time.Time
)

// DeviceURL returns the current device URL ("" without usable network),
// re-checking the interfaces at most every urlRefresh. Safe from the LCD,
// HTTP-screenshot and SCDP goroutines.
func DeviceURL() string {
	urlMu.Lock()
	defer urlMu.Unlock()
	if !urlAt.IsZero() && time.Since(urlAt) < urlRefresh {
		return urlVal
	}
	urlAt = time.Now()
	urlVal = ""
	if addrs, err := interfaceAddrs(); err == nil {
		urlVal = PickDeviceURL(addrs)
	}
	return urlVal
}
