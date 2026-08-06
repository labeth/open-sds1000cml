package panel

import (
	"testing"

	"open-sds/app/internal/superres"
)

// armDeviceSR puts the controller in the state srSeedAndStart leaves for DEVICE
// super-res: active + device source, a live stacker channel, the running shadow
// forced true while the engine FSM is actually stopped (SetRunning(false)), and the
// softkeys mapped to the super-res page. It does NOT spin srLoop — the tests exercise
// only the RUN/SINGLE contention guard, which owns srStop via srCancel.
func armDeviceSR(c *Controller) chan struct{} {
	stop := make(chan struct{})
	c.mu.Lock()
	c.srActive, c.srDevice = true, true
	c.srStack = superres.New(srDevGridL*srDevK, srDevK)
	c.srStop = stop
	c.menuPage = pgSuperres
	c.running, c.single = true, false // device arm: shadow run=true, engine stopped
	c.mu.Unlock()
	return stop
}

func lastCall(eng *fakeEng, what string) (call, bool) {
	for i := len(eng.calls) - 1; i >= 0; i-- {
		if eng.calls[i].what == what {
			return eng.calls[i], true
		}
	}
	return call{}, false
}

// TestRunCancelsDeviceSuperres: pressing RUN while device super-res is active must
// cancel it (srActive=false, srStop released, softkey page closed) and RESUME normal
// acquisition (SetRunning(true)) — never toggle from the desynced device shadow into
// a stop. Leaves the panel + engine in a clean, single-owner state.
func TestRunCancelsDeviceSuperres(t *testing.T) {
	c, eng, _ := newC(t)
	stop := armDeviceSR(c)

	c.button(btnRunStop)

	c.mu.Lock()
	active, srStop, running, single, page := c.srActive, c.srStop, c.running, c.single, c.menuPage
	c.mu.Unlock()
	if active {
		t.Fatalf("srActive still true after RUN — device super-res not cancelled")
	}
	if srStop != nil {
		t.Fatalf("srStop not released after cancel (goroutine owner not stopped)")
	}
	if page == pgSuperres {
		t.Fatalf("super-res softkey page still open after cancel")
	}
	if !running || single {
		t.Fatalf("shadow after RUN = {running:%v single:%v}, want {true false} (resume)", running, single)
	}
	select {
	case <-stop:
	default:
		t.Fatalf("srStop channel was not closed — srLoop would leak")
	}
	if cl, ok := lastCall(eng, "run"); !ok || cl.a != 1 {
		t.Fatalf("engine not resumed: last run call = %+v, ok=%v (want SetRunning(true))", cl, ok)
	}
}

// TestSingleCancelsDeviceSuperres: pressing SINGLE while device super-res is active
// must cancel it and issue a clean single-shot (SetSingle), so the single-shot drive
// and the CombineDrain owner path can't contend the FSM.
func TestSingleCancelsDeviceSuperres(t *testing.T) {
	c, eng, _ := newC(t)
	stop := armDeviceSR(c)

	c.button(btnSingle)

	c.mu.Lock()
	active, srStop, running, single, norm, page := c.srActive, c.srStop, c.running, c.single, c.norm, c.menuPage
	c.mu.Unlock()
	if active {
		t.Fatalf("srActive still true after SINGLE — device super-res not cancelled")
	}
	if srStop != nil {
		t.Fatalf("srStop not released after cancel")
	}
	if page == pgSuperres {
		t.Fatalf("super-res softkey page still open after cancel")
	}
	if !running || !single || !norm {
		t.Fatalf("shadow after SINGLE = {running:%v single:%v norm:%v}, want all true", running, single, norm)
	}
	select {
	case <-stop:
	default:
		t.Fatalf("srStop channel was not closed")
	}
	if _, ok := lastCall(eng, "single"); !ok {
		t.Fatalf("engine SetSingle not issued after SINGLE")
	}
}

// TestHostSuperresUnchangedOnRun pins the invariant that HOST-drizzle super-res is
// NOT cancelled by RUN/SINGLE (it never owns the FSM, so it can't contend). The RUN
// press must take its ordinary toggle path with the stack left running.
func TestHostSuperresUnchangedOnRun(t *testing.T) {
	c, eng, _ := newC(t)
	stop := make(chan struct{})
	c.mu.Lock()
	c.srActive, c.srDevice = true, false // HOST drizzle
	c.srStack = superres.New(256, 4)
	c.srStop = stop
	c.running = true // ordinary running state
	c.mu.Unlock()

	c.button(btnRunStop)

	c.mu.Lock()
	active, srStop, running := c.srActive, c.srStop, c.running
	c.mu.Unlock()
	if !active {
		t.Fatalf("host super-res cancelled by RUN — must be unchanged")
	}
	if srStop != stop {
		t.Fatalf("host super-res srStop mutated by RUN (stacker disturbed)")
	}
	// Ordinary toggle path ran: RUN from running=true → SetRunning(false).
	if running {
		t.Fatalf("RUN did not toggle host running shadow (still true)")
	}
	if cl, ok := lastCall(eng, "run"); !ok || cl.a != 0 {
		t.Fatalf("host RUN toggle mis-driven: last run call = %+v, ok=%v (want SetRunning(false))", cl, ok)
	}
	select {
	case <-stop:
		t.Fatalf("host srStop was closed by RUN — stacker killed")
	default:
	}
}

// TestSingleDoesNotCancelHostSuperres: SINGLE must likewise leave host drizzle running.
func TestSingleDoesNotCancelHostSuperres(t *testing.T) {
	c, eng, _ := newC(t)
	stop := make(chan struct{})
	c.mu.Lock()
	c.srActive, c.srDevice = true, false
	c.srStack = superres.New(256, 4)
	c.srStop = stop
	c.mu.Unlock()

	c.button(btnSingle)

	c.mu.Lock()
	active, srStop := c.srActive, c.srStop
	c.mu.Unlock()
	if !active {
		t.Fatalf("host super-res cancelled by SINGLE — must be unchanged")
	}
	if srStop != stop {
		t.Fatalf("host super-res srStop mutated by SINGLE")
	}
	if _, ok := lastCall(eng, "single"); !ok {
		t.Fatalf("SINGLE did not issue SetSingle")
	}
}
