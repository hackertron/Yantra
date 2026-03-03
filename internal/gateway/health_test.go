package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hackertron/Yantra/internal/types"
)

func TestHandleHealth(t *testing.T) {
	s := &Server{cfg: types.GatewayConfig{}}

	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	s.handleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", body["status"])
	}
	if body["version"] != "0.1.0" {
		t.Errorf("expected version=0.1.0, got %q", body["version"])
	}
}

func TestHandleReady(t *testing.T) {
	s := &Server{cfg: types.GatewayConfig{}}

	req := httptest.NewRequest("GET", "/ready", nil)
	rec := httptest.NewRecorder()
	s.handleReady(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]bool
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !body["ready"] {
		t.Error("expected ready=true")
	}
}
