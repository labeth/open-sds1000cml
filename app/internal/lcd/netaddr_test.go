package lcd

import (
	"errors"
	"net"
	"testing"
	"time"
)

func ipnet(s string) *net.IPNet {
	return &net.IPNet{IP: net.ParseIP(s), Mask: net.CIDRMask(24, 32)}
}

func TestPickDeviceURL(t *testing.T) {
	cases := []struct {
		name  string
		addrs []net.Addr
		want  string
	}{
		{"typical box", []net.Addr{
			ipnet("127.0.0.1"),                         // loopback — never
			&net.IPNet{IP: net.ParseIP("::1")},         // v6 loopback
			ipnet("169.254.7.9"),                       // link-local = failed DHCP — never
			&net.IPNet{IP: net.ParseIP("fe80::1234")},  // v6 link-local
			&net.IPNet{IP: net.ParseIP("2001:db8::5")}, // v6 global: not phone-dictable here
			ipnet("192.168.1.209"),                     // the one we want
			ipnet("10.0.0.5"),                          // later address ignored (first wins)
		}, "http://192.168.1.209:8080"},
		{"first global v4 wins", []net.Addr{
			ipnet("10.0.0.5"), ipnet("192.168.1.209"),
		}, "http://10.0.0.5:8080"},
		{"plain IPAddr entries too", []net.Addr{
			&net.IPAddr{IP: net.ParseIP("192.0.2.7")},
		}, "http://192.0.2.7:8080"},
		{"loopback only", []net.Addr{ipnet("127.0.0.1")}, ""},
		{"link-local only", []net.Addr{ipnet("169.254.1.1")}, ""},
		{"v6 only", []net.Addr{&net.IPNet{IP: net.ParseIP("2001:db8::5")}}, ""},
		{"empty", nil, ""},
		{"nil IP entry", []net.Addr{&net.IPNet{}}, ""},
	}
	for _, tc := range cases {
		if got := PickDeviceURL(tc.addrs); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// DeviceURL: seam-injected enumeration, error handling, and the low-rate cache
// (a fresh answer inside the refresh window must NOT re-enumerate).
func TestDeviceURLCaching(t *testing.T) {
	oldFn := interfaceAddrs
	defer func() {
		interfaceAddrs = oldFn
		urlMu.Lock()
		urlAt, urlVal = time.Time{}, ""
		urlMu.Unlock()
	}()

	calls := 0
	addrs := []net.Addr{ipnet("192.168.1.30")}
	var fnErr error
	interfaceAddrs = func() ([]net.Addr, error) { calls++; return addrs, fnErr }

	reset := func() {
		urlMu.Lock()
		urlAt, urlVal = time.Time{}, ""
		urlMu.Unlock()
	}

	reset()
	if got := DeviceURL(); got != "http://192.168.1.30:8080" {
		t.Fatalf("DeviceURL = %q", got)
	}
	// Within the refresh window: cached, no second enumeration even though the
	// injected list changed.
	addrs = []net.Addr{ipnet("10.9.9.9")}
	if got := DeviceURL(); got != "http://192.168.1.30:8080" {
		t.Errorf("cached DeviceURL = %q, want the previous answer", got)
	}
	if calls != 1 {
		t.Errorf("enumerated %d times inside the refresh window, want 1", calls)
	}
	// Cache expired → the new address shows up.
	urlMu.Lock()
	urlAt = time.Now().Add(-urlRefresh - time.Second)
	urlMu.Unlock()
	if got := DeviceURL(); got != "http://10.9.9.9:8080" {
		t.Errorf("post-expiry DeviceURL = %q", got)
	}
	// Enumeration error → "" (render nothing), not a stale/garbage URL.
	fnErr = errors.New("boom")
	reset()
	if got := DeviceURL(); got != "" {
		t.Errorf("DeviceURL on error = %q, want empty", got)
	}
}

// The top-bar render: a URL paints dim text in the reserved top-bar span; an
// empty URL paints nothing there (the no-network case renders nothing).
func TestRenderDeviceURL(t *testing.T) {
	region := func(m *MemSurface) int { return countColorIn(m, colDim, 380, 0, 670, 12) }

	h := defaultHUD()
	h.URL = "http://192.168.1.209:8080"
	with := NewMemSurface()
	Render(with, testFrame(2048), h, true)
	if region(with) == 0 {
		t.Error("URL set but nothing drawn on the top bar")
	}

	h.URL = ""
	without := NewMemSurface()
	Render(without, testFrame(2048), h, true)
	if n := region(without); n != 0 {
		t.Errorf("no-network HUD painted %d URL pixels, want none", n)
	}

	// The URL must not collide with the right-aligned trigger readout even at
	// its widest, nor with the math legend: check its extent stays in-lane.
	h.URL = "http://255.255.255.255:8080" // widest possible
	h.MathMode = 4                        // "M:C1xC2" legend at x=300
	wide := NewMemSurface()
	Render(wide, testFrame(2048), h, true)
	if countColorIn(wide, colDim, 0, 0, 380, 12) != 0 {
		t.Error("widest URL leaked left of x=380 into the legend zone")
	}
	if countColorIn(wide, colDim, 665, 0, W, 12) != 0 {
		t.Error("URL leaked right of x=664 into the trigger readout zone")
	}
}
