// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"io"
	"net/http"
	"testing"
)

// httpGet sends one GET through c (nil: the default client), carrying the
// test's context so a test that ends cancels its request.
// TRLC-LINKS: REQ-SDS-220
func httpGet(tb testing.TB, c *http.Client, url string) (*http.Response, error) {
	tb.Helper()
	req, err := http.NewRequestWithContext(tb.Context(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if c == nil {
		c = http.DefaultClient
	}
	return c.Do(req)
}

// httpPost sends one POST of body with the content type through c (nil: the
// default client), carrying the test's context.
// TRLC-LINKS: REQ-SDS-220
func httpPost(tb testing.TB, c *http.Client, url, contentType string, body io.Reader) (*http.Response, error) {
	tb.Helper()
	req, err := http.NewRequestWithContext(tb.Context(), http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	if c == nil {
		c = http.DefaultClient
	}
	return c.Do(req)
}

// jsonAt is the value at path inside a decoded JSON document v (string keys
// into objects, int indexes into arrays) as a T, failing the test where the
// document does not have that shape.
// TRLC-LINKS: REQ-SDS-220
func jsonAt[T any](tb testing.TB, v any, path ...any) T {
	tb.Helper()
	for i, step := range path {
		switch k := step.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				tb.Fatalf("json %v: %T is not an object", path[:i+1], v)
			}
			v = m[k]
		case int:
			a, ok := v.([]any)
			if !ok || k < 0 || k >= len(a) {
				tb.Fatalf("json %v: %T has no element %d", path[:i+1], v, k)
			}
			v = a[k]
		default:
			tb.Fatalf("json path step %v is %T", step, step)
		}
	}
	t, ok := v.(T)
	if !ok {
		var want T
		tb.Fatalf("json %v: %T, want %T", path, v, want)
	}
	return t
}
