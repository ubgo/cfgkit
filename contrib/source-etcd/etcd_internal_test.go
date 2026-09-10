package etcd

import (
	"bytes"
	"testing"
)

// TestPrefixEnd unit-tests the range-successor helper directly, because it is
// the one piece of real algorithm in this package and its edge case cannot be
// reached through Prefix — the range key always ends with "/", so the
// all-0xFF branch is unreachable from outside.
//
// etcd expresses a prefix scan as a range from the key to its successor: the
// same bytes with the last one incremented, dropping whatever followed. A
// prefix that is entirely 0xFF has no successor, and etcd's own convention for
// that is a range_end of a single zero byte, meaning "to the end of the
// keyspace".
func TestPrefixEnd(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		want   []byte
	}{
		{"ordinary", "app/", []byte("app0")}, // '/'+1 == '0'
		{"single byte", "a", []byte("b")},
		{"trailing 0xFF is skipped", "a\xff", []byte("b")},
		{"several trailing 0xFF", "a\xff\xff", []byte("b")},
		{"all 0xFF has no successor", "\xff\xff", []byte{0}},
		{"empty", "", []byte{0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := prefixEnd(tc.prefix); !bytes.Equal(got, tc.want) {
				t.Errorf("prefixEnd(%q) = %q, want %q", tc.prefix, got, tc.want)
			}
		})
	}
}
