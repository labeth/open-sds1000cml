// ENGMODEL-OWNER-UNIT: FU-APP-APP
package main

import (
	"hash/maphash"
	"math"
	"reflect"
)

// sigHash is a 64-bit fingerprint of values walked by reflection: the
// display's repaint check runs it on the whole workspace view at every tick,
// where printing the view with fmt allocated more than the rest of a stopped
// scope and kept the collector busy. Every field, exported or not, feeds it,
// strings and slices with their lengths, so two views it calls equal print
// the same; a pointer is followed; funcs and channels are skipped, and so is
// a field tagged sig:"-", which a generation field beside it stands for.
type sigHash uint64

const (
	sigOffset sigHash = 14695981039346656037
	sigPrime  sigHash = 1099511628211
)

// TRLC-LINKS: REQ-SDS-168
func (h *sigHash) byte1(b byte) { *h = (*h ^ sigHash(b)) * sigPrime }

// TRLC-LINKS: REQ-SDS-168
func (h *sigHash) word(v uint64) {
	// One multiply a word: the fingerprint only needs to tell views apart.
	*h = (*h ^ sigHash(v)) * sigPrime
	*h ^= *h >> 29
}

// TRLC-LINKS: REQ-SDS-168
func (h *sigHash) str(s string) {
	h.word(uint64(len(s)))
	h.word(maphash.String(sigSeed, s))
}

// sigSeed seeds the string hashes: the fingerprints are compared only
// within one run.
var sigSeed = maphash.MakeSeed()

// add feeds one value.
// TRLC-LINKS: REQ-SDS-168
func (h *sigHash) add(v reflect.Value) {
	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			h.byte1(1)
		} else {
			h.byte1(0)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		h.word(uint64(v.Int()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		h.word(v.Uint())
	case reflect.Float32, reflect.Float64:
		h.word(math.Float64bits(v.Float()))
	case reflect.Complex64, reflect.Complex128:
		c := v.Complex()
		h.word(math.Float64bits(real(c)))
		h.word(math.Float64bits(imag(c)))
	case reflect.String:
		h.str(v.String())
	case reflect.Slice:
		if v.IsNil() {
			h.byte1(0xfe)
			return
		}
		fallthrough
	case reflect.Array:
		n := v.Len()
		h.word(uint64(n))
		if v.Type().Elem().Kind() == reflect.Uint8 && v.Kind() == reflect.Slice {
			h.word(maphash.Bytes(sigSeed, v.Bytes()))
			return
		}
		for i := 0; i < n; i++ {
			h.add(v.Index(i))
		}
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			if t.Field(i).Tag.Get("sig") == "-" { // a generation field stands for it
				continue
			}
			h.add(v.Field(i))
		}
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			h.byte1(0xfd)
			return
		}
		h.byte1(0xfc)
		if v.Kind() == reflect.Interface {
			h.str(v.Elem().Type().String())
		}
		h.add(v.Elem())
	case reflect.Map:
		// Order-free: the sum of each entry's own fingerprint.
		h.word(uint64(v.Len()))
		var sum sigHash
		it := v.MapRange()
		for it.Next() {
			e := sigOffset
			e.add(it.Key())
			e.add(it.Value())
			sum += e
		}
		h.word(uint64(sum))
	}
}

// addAny feeds x, tagged so that adjacent values do not run together.
// TRLC-LINKS: REQ-SDS-168
func (h *sigHash) addAny(tag string, x any) {
	h.str(tag)
	h.add(reflect.ValueOf(x))
}
