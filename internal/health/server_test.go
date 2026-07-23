package health

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadyEndpointRequiresReadyState(t *testing.T) {
	server := New(":0", slog.Default())
	response := httptest.NewRecorder()

	server.handleReady(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected not ready status, got %d", response.Code)
	}

	server.MarkReady()
	readyResponse := httptest.NewRecorder()
	server.handleReady(readyResponse, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if readyResponse.Code != http.StatusOK {
		t.Fatalf("expected ready status, got %d", readyResponse.Code)
	}
}

func TestHealthEndpointAlwaysReturnsOK(t *testing.T) {
	server := New(":0", slog.Default())
	response := httptest.NewRecorder()

	server.handleHealth(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("expected health status ok, got %d", response.Code)
	}
}
