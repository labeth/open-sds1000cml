// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"context"
	"net/http/httptest"
	"open-sds/app/internal/testenv"
	"os/exec"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
)

type contextFPGAStackScope struct {
	fakeScope
	seen context.Context
}

// TRLC-LINKS: REQ-SDS-141
func (f *contextFPGAStackScope) FPGAStack(ctx context.Context, _ engine.FPGAStackRequest) (engine.FPGAStackResult, error) {
	f.seen = ctx
	return engine.FPGAStackResult{}, nil
}

// TRLC-LINKS: REQ-SDS-141
func TestFPGAStackUsesRequestLifetime(t *testing.T) {
	f := &contextFPGAStackScope{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "/api/superres/fpga", strings.NewReader(`{"records":20}`)).WithContext(ctx)
	w := httptest.NewRecorder()
	New(f, nil, nil, nil).Handler().ServeHTTP(w, r)
	if w.Code != 200 || f.seen == nil {
		t.Fatalf("request: %d %s", w.Code, w.Body.String())
	}
	if _, ok := f.seen.Deadline(); ok {
		t.Fatal("large progressing stack received a fixed session deadline")
	}
	cancel()
	select {
	case <-f.seen.Done():
	default:
		t.Fatal("request cancellation did not propagate")
	}
}

// TRLC-LINKS: REQ-SDS-141
func TestFPGAStackBrowserCancel(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "fpga_cancel.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("FPGA cancel: %v\n%s", err, out)
	}
}
