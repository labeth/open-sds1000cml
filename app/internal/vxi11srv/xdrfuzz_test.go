package vxi11srv

import (
	"encoding/binary"
	"math/rand"
	"testing"
)

// XDR/RPC fuzz: the VXI-11 layer is network-exposed. Hostile length fields in
// opaque()/skipAuth() must never panic or overflow the offset — on the 32-bit
// ARM target `int` is 32-bit, so a naive `off+n > len` bound check wraps for a
// length near 0x7fffffff and slips into a panicking slice. This drives the
// exact call-parse + dispatch path serve() runs.
func TestXDRParseFuzz(t *testing.T) {
	srv := &Server{h: func(line []byte) []byte { return append([]byte("echo:"), line...) }}
	rng := rand.New(rand.NewSource(0x11223344))

	be := func(vals ...uint32) []byte {
		b := make([]byte, 4*len(vals))
		for i, v := range vals {
			binary.BigEndian.PutUint32(b[4*i:], v)
		}
		return b
	}
	hostileLen := []uint32{0, 1, 3, 4, 0x7fffffff, 0x7ffffffc, 0x80000000, 0xffffffff, 0xfffffffc, 1 << 20}

	for i := 0; i < 20000; i++ {
		// build a plausible RPC CALL header with attacker-chosen auth + payload lengths
		var msg []byte
		msg = append(msg, be(uint32(rng.Int31()), 0, 2, progCore, 1, uint32(10+rng.Intn(16)))...) // xid,CALL,vers,prog,vers,proc
		// two auth blocks with hostile lengths
		for a := 0; a < 2; a++ {
			msg = append(msg, be(0, hostileLen[rng.Intn(len(hostileLen))])...)
			// sometimes include some bytes, usually not (so the length lies)
			if rng.Intn(3) == 0 {
				pad := make([]byte, rng.Intn(20))
				msg = append(msg, pad...)
			}
		}
		// a device-write-style body: lid, timeouts, flags, then an opaque with a hostile length
		msg = append(msg, be(1, 0, 0, 0, hostileLen[rng.Intn(len(hostileLen))])...)
		body := make([]byte, rng.Intn(64))
		rng.Read(body)
		msg = append(msg, body...)
		// randomly truncate the whole thing
		if rng.Intn(2) == 0 && len(msg) > 0 {
			msg = msg[:rng.Intn(len(msg))]
		}

		cn := &conn{srv: srv}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("iter %d panicked: %v (msg len %d)", i, r, len(msg))
				}
			}()
			// mirror serve()'s parse prologue exactly
			x := &xdr{b: msg}
			x.u32() // xid
			if x.u32() != 0 {
				return
			}
			x.u32()
			prog := x.u32()
			x.u32()
			proc := x.u32()
			x.skipAuth()
			x.skipAuth()
			if x.off < 0 || x.off > len(x.b) {
				t.Fatalf("iter %d: offset escaped buffer: off=%d len=%d", i, x.off, len(x.b))
			}
			if prog == progCore {
				cn.dispatch(proc, x)
			}
		}()
	}
}
