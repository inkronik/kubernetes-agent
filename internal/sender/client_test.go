package sender

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
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

func TestSendTelemetryChunksRequestsBySerializedSize(t *testing.T) {
	const maxRequestBodyBytes = 900

	receivedSignals := []model.TelemetrySignal{}
	requestSizes := []int{}
	client := New(ClientOptions{
		BaseURL:             "https://collector.inkronik.test",
		APIKey:              "ik_live_prefix_secret",
		MaxRequestBodyBytes: maxRequestBodyBytes,
		Timeout:             time.Second,
	})
	client.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("expected request body read to succeed: %v", err)
		}

		if request.Header.Get("Authorization") != "Bearer ik_live_prefix_secret" {
			t.Fatalf("expected ingest authorization header")
		}

		var batch model.TelemetryBatch
		if err := json.Unmarshal(body, &batch); err != nil {
			t.Fatalf("expected request body to decode: %v", err)
		}

		requestSizes = append(requestSizes, len(body))
		receivedSignals = append(receivedSignals, batch.Signals...)
		return acceptedResponse(), nil
	})
	signals := []model.TelemetrySignal{
		metricSignal("first", strings.Repeat("a", 300)),
		metricSignal("second", strings.Repeat("b", 300)),
		metricSignal("third", strings.Repeat("c", 300)),
	}

	if err := client.SendTelemetry(context.Background(), signals); err != nil {
		t.Fatalf("expected chunked telemetry send to succeed: %v", err)
	}

	if len(requestSizes) <= 1 {
		t.Fatalf("expected more than one request, got %d", len(requestSizes))
	}
	for _, requestSize := range requestSizes {
		if requestSize > maxRequestBodyBytes {
			t.Fatalf("expected request at most %d bytes, got %d", maxRequestBodyBytes, requestSize)
		}
	}
	if len(receivedSignals) != len(signals) {
		t.Fatalf("expected %d signals, got %d", len(signals), len(receivedSignals))
	}
	for index, signal := range receivedSignals {
		payload, ok := signal.Payload.(map[string]any)
		if !ok || payload["metric_name"] != []string{"first", "second", "third"}[index] {
			t.Fatalf("expected signal order to be preserved at index %d", index)
		}
	}
}

func TestSendTelemetryRejectsSingleSignalLargerThanRequestLimit(t *testing.T) {
	requestCount := 0
	client := New(ClientOptions{
		BaseURL:             "https://collector.inkronik.test",
		APIKey:              "ik_live_prefix_secret",
		MaxRequestBodyBytes: 200,
		Timeout:             time.Second,
	})
	client.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestCount++
		return acceptedResponse(), nil
	})

	err := client.SendTelemetry(context.Background(), []model.TelemetrySignal{metricSignal("oversized", strings.Repeat("x", 500))})
	if err == nil || !strings.Contains(err.Error(), "exceeds request body limit") {
		t.Fatalf("expected oversized signal error, got %v", err)
	}
	if requestCount != 0 {
		t.Fatalf("expected no requests, got %d", requestCount)
	}
}

func TestSendTelemetryReportsFailedBatchPosition(t *testing.T) {
	requestCount := 0
	client := New(ClientOptions{
		BaseURL:             "https://collector.inkronik.test",
		APIKey:              "ik_live_prefix_secret",
		MaxRequestBodyBytes: 900,
		Timeout:             time.Second,
	})
	client.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestCount++
		if requestCount == 2 {
			return &http.Response{
				StatusCode: http.StatusRequestEntityTooLarge,
				Body:       io.NopCloser(strings.NewReader("request too large")),
				Header:     make(http.Header),
			}, nil
		}

		return acceptedResponse(), nil
	})

	err := client.SendTelemetry(context.Background(), []model.TelemetrySignal{
		metricSignal("first", strings.Repeat("a", 300)),
		metricSignal("second", strings.Repeat("b", 300)),
		metricSignal("third", strings.Repeat("c", 300)),
	})
	if err == nil || !strings.Contains(err.Error(), "send telemetry batch 2/3") {
		t.Fatalf("expected failed batch position, got %v", err)
	}
	if requestCount != 2 {
		t.Fatalf("expected sending to stop after second batch, got %d requests", requestCount)
	}
}

type roundTripFunc func(request *http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func acceptedResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusAccepted,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
	}
}

func metricSignal(name string, padding string) model.TelemetrySignal {
	return model.TelemetrySignal{
		SignalType:  "metric",
		Environment: "production",
		Timestamp:   time.Unix(0, 0).UTC(),
		Source:      "inkronik-k8s-agent",
		Attributes:  map[string]string{"padding": padding},
		Payload: model.MetricGaugePayload{
			MetricKind:         "gauge",
			ServiceName:        "kubernetes",
			MetricName:         name,
			Unit:               "bytes",
			Value:              1,
			ResourceAttributes: map[string]string{"inkronik.application_id": "app-uuid"},
			MetricAttributes:   map[string]string{},
		},
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
