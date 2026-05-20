package jsonutil

import (
	"strings"
	"testing"
)

func TestPrettyDoesNotEscapeHTML(t *testing.T) {
	got, err := Pretty(map[string]string{"comment": "/gitmoot audit <review>"})
	if err != nil {
		t.Fatalf("Pretty returned error: %v", err)
	}
	if strings.Contains(string(got), "\\u003c") {
		t.Fatalf("Pretty escaped HTML characters:\n%s", string(got))
	}
}
