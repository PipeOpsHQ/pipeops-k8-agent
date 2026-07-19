package auth

import (
	"net/http"
	"testing"
)

func TestSetBearer(t *testing.T) {
	h := make(http.Header)
	SetBearer(h, "sat_abc")
	if got := h.Get("Authorization"); got != "Bearer sat_abc" {
		t.Fatalf("Authorization=%q", got)
	}
}

func TestSetBearer_StripsPrefix(t *testing.T) {
	h := make(http.Header)
	SetBearer(h, "Bearer already")
	if got := h.Get("Authorization"); got != "Bearer already" {
		t.Fatalf("Authorization=%q", got)
	}
}

func TestBearerHeader_Empty(t *testing.T) {
	h := BearerHeader("  ")
	if h.Get("Authorization") != "" {
		t.Fatal("expected empty Authorization for blank token")
	}
}

func TestSetBearer_NilHeader(t *testing.T) {
	// must not panic
	SetBearer(nil, "token")
}
