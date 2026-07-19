package controlplane

import (
	"net/http"
	"testing"
)

func TestApplyAuthHeader_BearerOnly(t *testing.T) {
	h := make(http.Header)
	ApplyAuthHeader(h, "sat_test_token")
	if got := h.Get("Authorization"); got != "Bearer sat_test_token" {
		t.Fatalf("Authorization=%q", got)
	}
}

func TestApplyAuthHeader_StripsExistingBearer(t *testing.T) {
	h := AuthHeader("Bearer already")
	if got := h.Get("Authorization"); got != "Bearer already" {
		t.Fatalf("Authorization=%q", got)
	}
}

func TestApplyAuthHeader_Empty(t *testing.T) {
	h := AuthHeader("  ")
	if h.Get("Authorization") != "" {
		t.Fatal("expected empty header for empty token")
	}
}
