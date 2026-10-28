// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"reflect"
	"testing"

	"open-sds/app/internal/decode"
)

// TestEnclosingSpans: a reading over its bytes and a length over its fields
// are written above them; a field over its frame's start marker is not.
// TRLC-LINKS: REQ-SDS-021
func TestEnclosingSpans(t *testing.T) {
	sp := func(kind, text string, i0, i1 int) decode.Span {
		return decode.Span{Kind: kind, Text: text, I0: i0, I1: i1}
	}
	caliper := []decode.Span{sp("sof", "", 0, 0), sp("data", "D4", 0, 90), sp("data", "04", 100, 180), sp("data", "00", 190, 270), sp("rw", "12.34mm", 0, 270)}
	if got := enclosingSpans(caliper); !reflect.DeepEqual(got, []bool{false, false, false, false, true}) {
		t.Fatalf("caliper: %v", got)
	}
	wiegand := []decode.Span{sp("sof", "", 0, 2), sp("rw", "26b", 0, 500), sp("data", "F42", 20, 200), sp("data", "C4660", 220, 480)}
	if got := enclosingSpans(wiegand); !reflect.DeepEqual(got, []bool{false, true, false, false}) {
		t.Fatalf("wiegand: %v", got)
	}
	modbus := []decode.Span{sp("data", "A11", 0, 100), sp("sof", "SOF", 0, 5), sp("data", "03", 110, 200)}
	if got := enclosingSpans(modbus); !reflect.DeepEqual(got, []bool{false, false, false}) {
		t.Fatalf("a field over its frame's start stays in the byte row: %v", got)
	}
}
