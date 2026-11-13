package jsonc

import (
	"testing"

	"github.com/ubgo/textdata/jsonc"
)

// TestLocateKeysArrayIndex covers the element form of an encoding/json field
// path ("hosts.1"), which Go 1.27+ reports for a type error inside an array:
// the index must resolve to that element, and a bad or missing index to
// nothing rather than to the array.
func TestLocateKeysArrayIndex(t *testing.T) {
	doc, err := jsonc.Parse([]byte("{\n  \"hosts\": [\"a\",\n    1]\n}"))
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := locateKeys(doc, []string{"hosts", "1"}); !ok || p != "hosts[1]" {
		t.Errorf("locateKeys(hosts.1) = %q, %v; want hosts[1]", p, ok)
	}
	for _, keys := range [][]string{{"hosts", "x"}, {"hosts", "-1"}, {"hosts", "2"}, {"hosts", "0", "deeper"}} {
		if p, ok := locateKeys(doc, keys); ok {
			t.Errorf("locateKeys(%v) = %q, want not found", keys, p)
		}
	}
}
