// auxmap drives the FULL verified acquisition (engine + analog front end, so a
// REAL varying signal is digitised into the ADC) while our `mapall` fabric
// watches every non-interface Cyclone ball with a sticky CHANGE bit. After a
// couple of live frames it stops the engine, freezes the bitmap, and dumps it as
// 10 hex words. Any set bit = a pin the AUX toggled during a live capture. The
// data-vs-flat comparison isolates the AUX->FPGA ADC data bus: run once with the
// signal present (this = live) and once flat (signal source off / detent that
// rails); the bits that drop out are the ADC data lines.
//
// Protocol (the mapall fabric decodes DATA value 0xF0xx on any CS1 write as a
// command; a read returns the selected 16-bit slice):
//
//	write 0xF001            -> reset all sticky bits, unfreeze
//	write 0xF002|(w<<4)     -> freeze + select bitmap word w
//	read  (any non-ver sel) -> bitmap word w
//
// Stop the app first (single-owner /dev/Gpmc). Usage: auxmap [tdivS] [detent] [flat]
package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/bus"
	"open-sds/app/internal/engine"
)

const cmdSel = 0x50 // any non-version, non-forbidden CS1 selector

func findGpmcFD() int {
	ents, _ := os.ReadDir("/proc/self/fd")
	for _, e := range ents {
		if tgt, err := os.Readlink("/proc/self/fd/" + e.Name()); err == nil && tgt == "/dev/Gpmc" {
			if fd, err := strconv.Atoi(e.Name()); err == nil {
				return fd
			}
		}
	}
	return -1
}

func main() {
	tdiv := 1e-7
	detent := analog.BootDetent
	flat := false
	if len(os.Args) > 1 {
		if v, err := strconv.ParseFloat(os.Args[1], 64); err == nil {
			tdiv = v
		}
	}
	if len(os.Args) > 2 {
		if v, err := strconv.Atoi(os.Args[2]); err == nil {
			detent = v
		}
	}
	if len(os.Args) > 3 && os.Args[3] == "flat" {
		flat = true
	}

	fd := findGpmcFD()
	if fd < 0 {
		fmt.Fprintln(os.Stderr, "no inherited /dev/Gpmc fd")
		os.Exit(1)
	}
	b, err := bus.New(fd, true)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bus:", err)
		os.Exit(1)
	}

	// Clear any config-time glitches on the sticky bits before we drive.
	b.Write(bus.PlaneCS1, cmdSel, 0xF001)

	// Optional drive command (0xE... for the auxdrive fabric): latch which ball
	// group + source to actively drive onto the AUX while we capture. Passed as a
	// 4th arg in hex, e.g. 0xE073 = drive group 3 with clk (80MHz), drive-on.
	if len(os.Args) > 4 {
		if dv, err := strconv.ParseUint(os.Args[4], 0, 16); err == nil {
			b.Write(bus.PlaneCS1, cmdSel, uint16(dv))
			fmt.Printf("drive cmd = 0x%04x\n", uint16(dv))
		}
	}

	e := engine.New(engine.Config{Bus: b, Logf: func(string, ...any) {}})

	feOK := false
	if !flat {
		if dev, err := analog.NewDev(); err != nil {
			fmt.Fprintf(os.Stderr, "front end: %v\n", err)
		} else {
			fe := analog.New(dev, nil, nil)
			fe.OnOffset(e.SetOffsetDAC)
			fe.OnOffsetV(e.SetChannelOffsetV)
			fe.OnVdiv(e.SetChannelVdiv)
			for ch := 0; ch < 2; ch++ {
				fe.SetProbe(ch, 1)
				fe.SetCoupling(ch, analog.CplDC)
				if err := fe.SetVdiv(ch, detent); err != nil {
					fmt.Fprintf(os.Stderr, "SetVdiv ch=%d: %v\n", ch, err)
				}
				fe.SetOffset(ch, 0)
			}
			feOK = true
		}
	}

	// "noarm" = snapshot without driving the acquisition (idle AUX) — for the
	// levelsnap idle reference. Otherwise run the full capture like adcdump.
	noarm := false
	for _, a := range os.Args[1:] {
		if a == "noarm" {
			noarm = true
		}
	}
	if !noarm {
		e.SetTdiv(tdiv)
		e.SetRunning(true)
		go e.Run()
		// Let the AUX stream a couple of live frames; sticky/level accumulates the
		// whole time (frozen defaults low after the reset). Shorten for fast sweeps.
		ms := 2500
		if v, err := strconv.Atoi(os.Getenv("AUXMAP_MS")); err == nil && v > 0 {
			ms = v
		}
		time.Sleep(time.Duration(ms) * time.Millisecond)
		// Freeze WHILE the acquisition is still active so levelsnap latches ACTIVE
		// levels (harmless for the cumulative sticky bitmap).
		b.Write(bus.PlaneCS1, cmdSel, 0xF002)
		e.Stop(2 * time.Second)
	} else {
		b.Write(bus.PlaneCS1, cmdSel, 0xF002)
	}
	time.Sleep(100 * time.Millisecond)

	// Scan the 10-word bitmap (freeze already latched above).
	const nwords = 10
	b.Write(bus.PlaneCS1, cmdSel, 0xF002)
	fmt.Printf("feOK=%v flat=%v tdiv=%g detent=%d\n", feOK, flat, tdiv, detent)
	total := 0
	for w := 0; w < nwords; w++ {
		b.Write(bus.PlaneCS1, cmdSel, uint16(0xF002|(w<<4)))
		v, _ := b.Read(bus.PlaneCS1, cmdSel)
		fmt.Printf("word %d = 0x%04x\n", w, v)
		for i := 0; i < 16; i++ {
			if v&(1<<uint(i)) != 0 {
				total++
			}
		}
	}
	fmt.Printf("lit bits total = %d\n", total)
	// word 15 = the A2 edge counter (auxdrive fabric) — the crosstalk-immune detector.
	b.Write(bus.PlaneCS1, cmdSel, uint16(0xF002|(15<<4)))
	a2, _ := b.Read(bus.PlaneCS1, cmdSel)
	fmt.Printf("a2cnt = %d\n", a2)
}
