package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestValidateToken(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(make([]byte, 32)) // 32 zero bytes
	if err := validateToken(good); err != nil {
		t.Errorf("32-byte token should pass, got %v", err)
	}
	min := base64.StdEncoding.EncodeToString(make([]byte, 16))
	if err := validateToken(min); err != nil {
		t.Errorf("16-byte token should pass (min), got %v", err)
	}

	// Failures.
	if err := validateToken(""); err == nil {
		t.Error("empty token must fail")
	}
	short := base64.StdEncoding.EncodeToString(make([]byte, 8))
	if err := validateToken(short); err == nil {
		t.Error("8-byte token must fail (below min)")
	}
	if err := validateToken("!!! not base64 !!!"); err == nil {
		t.Error("non-base64 token must fail")
	}
	// A short raw string that happens to decode must still be rejected by length.
	if err := validateToken("YWJj"); err == nil { // "abc" = 3 bytes
		t.Error("3-byte token must fail")
	} else if !strings.Contains(err.Error(), "too short") {
		t.Errorf("expected length error, got %v", err)
	}
}
