package templater

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestURLSafe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in, out string
	}{
		{"simple", "simple"},
		{"foo:bar", "foo:bar"},
		{"ns:task@v1", "ns:task|v1"},
		{"a/b/c", "a%2Fb%2Fc"},
		{"hello world", "hello%20world"},
		{"already-safe_123", "already-safe_123"},
		{"", ""},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.out, urlSafe(tt.in), "urlSafe(%q)", tt.in)
	}
}
