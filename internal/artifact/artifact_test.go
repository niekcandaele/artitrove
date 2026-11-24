package artifact

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestValidIDAcceptsGeneratedIDs(t *testing.T) {
	for range 1000 {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		id := base64.RawURLEncoding.EncodeToString(b)
		if !ValidID(id) {
			t.Fatalf("ValidID(%q) = false", id)
		}
	}
}

func TestValidIDRejects(t *testing.T) {
	valid := strings.Repeat("a", 22)
	bad := []string{
		"", strings.Repeat("a", 21), strings.Repeat("a", 23),
		"+" + valid[1:], "/" + valid[1:], "=" + valid[1:], "." + valid[1:], "%" + valid[1:],
		" " + valid[1:], "\n" + valid[1:], "\x00" + valid[1:], "\\" + valid[1:],
		strings.Repeat(".", 22), "../../../../../etc/pass",
		strings.Repeat("é", 11), valid[:21] + "\r",
	}
	for _, id := range bad {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true", id)
		}
	}
}

func TestErrors(t *testing.T) {
	if !errors.Is(ErrInvalidID, ErrNotFound) {
		t.Error("ErrInvalidID must wrap ErrNotFound")
	}
	if errors.Is(ErrEmpty, ErrNotFound) || errors.Is(ErrEmpty, ErrInvalidID) {
		t.Error("ErrEmpty must be distinct")
	}
}
