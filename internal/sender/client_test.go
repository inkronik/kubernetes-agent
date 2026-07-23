package sender

import (
	"context"
	"testing"
	"time"

	"github.com/inkronik/kubernetes-agent/internal/model"
)

func TestSendTelemetryOmitsApplicationHeaderForApplicationScopedKeys(t *testing.T) {
	client := New(ClientOptions{
		BaseURL: "https://collector.inkronik.test",
		APIKey:  "ik_live_prefix_secret",
		Timeout: time.Second,
	})

	request, err := client.newRequest(context.Background(), "/v1/telemetry", model.TelemetryBatch{Signals: []model.TelemetrySignal{{SignalType: "metric"}}})
	if err != nil {
		t.Fatalf("expected request build to succeed: %v", err)
	}

	if request.Header.Get("x-application-id") != "" {
		t.Fatalf("expected no x-application-id header, got %q", request.Header.Get("x-application-id"))
	}
}

func TestSendTelemetryKeepsLegacyApplicationHeaderWhenConfigured(t *testing.T) {
	client := New(ClientOptions{
		BaseURL:       "https://collector.inkronik.test",
		APIKey:        "ik_live_prefix_secret",
		ApplicationID: "00000000-0000-4000-8000-000000000201",
		Timeout:       time.Second,
	})

	request, err := client.newRequest(context.Background(), "/v1/telemetry", model.TelemetryBatch{Signals: []model.TelemetrySignal{{SignalType: "metric"}}})
	if err != nil {
		t.Fatalf("expected request build to succeed: %v", err)
	}

	if request.Header.Get("x-application-id") != "00000000-0000-4000-8000-000000000201" {
		t.Fatalf("expected legacy x-application-id header, got %q", request.Header.Get("x-application-id"))
	}
}
