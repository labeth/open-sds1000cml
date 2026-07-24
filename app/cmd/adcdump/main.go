// adcdump reuses the VERIFIED acquisition path (main branch) to capture one real
// frame and dump raw ADC samples — driving the bus EXACTLY like the app: the
// engine (CS1 acquisition) AND the analog FrontEnd (SPI relay/gain + CS3 offset
// DAC), which conditions the signal into the ADC. Stop the app first
// (single-owner /dev/Gpmc). Usage: adcdump [tdivS] [detent]  (default 1e-6, 8).
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
	tdiv := 1e-6
	detent := analog.BootDetent
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
	e := engine.New(engine.Config{Bus: b, Logf: func(string, ...any) {}})

	// Drive the analog front end EXACTLY like the app: emit the relay word +
	// gain over SPI and refresh the offset DAC over CS3 (the AUX). This is what
	// conditions the live signal into the ADC — the engine alone never touches it.
	feOK := false
	if dev, err := analog.NewDev(); err != nil {
		fmt.Fprintf(os.Stderr, "front end: %v\n", err)
	} else {
		fe := analog.New(dev, nil, nil) // nil tab -> cal.Defaults()
		fe.OnOffset(e.SetOffsetDAC)
		fe.OnOffsetV(e.SetChannelOffsetV)
		fe.OnVdiv(e.SetChannelVdiv)
		for ch := 0; ch < 2; ch++ {
			fe.SetProbe(ch, 1)
			fe.SetCoupling(ch, analog.CplDC)
			if err := fe.SetVdiv(ch, detent); err != nil { // emits relay/gain + CS3 offset
				fmt.Fprintf(os.Stderr, "SetVdiv ch=%d: %v\n", ch, err)
			}
			fe.SetOffset(ch, 0) // centre in the ADC range
		}
		feOK = true
	}

	band, ok := e.SetTdiv(tdiv)
	e.SetRunning(true)
	go e.Run()

	var best *engine.Frame
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if f, got := e.Consume(); got && f.Valid > 0 {
			best = f
			if f.Ptp > 5 && f.Coherent {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if best == nil {
		fmt.Println("no frame captured")
		os.Exit(1)
	}
	f := best
	n := f.Valid
	mn1, mx1, mn2, mx2 := byte(255), byte(0), byte(255), byte(0)
	for i := 0; i < n; i++ {
		if f.C1[i] < mn1 {
			mn1 = f.C1[i]
		}
		if f.C1[i] > mx1 {
			mx1 = f.C1[i]
		}
		if f.C2[i] < mn2 {
			mn2 = f.C2[i]
		}
		if f.C2[i] > mx2 {
			mx2 = f.C2[i]
		}
	}
	fmt.Printf("feOK=%v tdiv=%g detent=%d band(class=0x%02x lo=%d hi=%d ok=%v) valid=%d ptp=%d trigd=%v coherent=%v\n",
		feOK, tdiv, detent, band.Class, band.Lo, band.Hi, ok, n, f.Ptp, f.Trigd, f.Coherent)
	fmt.Printf("C1: min=%d max=%d ptp=%d\n", mn1, mx1, mx1-mn1)
	fmt.Printf("C2: min=%d max=%d ptp=%d\n", mn2, mx2, mx2-mn2)
	lo := n/2 - 24
	if lo < 0 {
		lo = 0
	}
	fmt.Printf("C1[%d..]:", lo)
	for i := lo; i < lo+48 && i < n; i++ {
		fmt.Printf(" %d", f.C1[i])
	}
	fmt.Println()
}
