package sender

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/inkronik/kubernetes-agent/internal/model"
)

type Client struct {
	baseURL             string
	apiKey              string
	applicationID       string
	maxRequestBodyBytes int
	client              *http.Client
}

type ClientOptions struct {
	BaseURL             string
	APIKey              string
	ApplicationID       string
	MaxRequestBodyBytes int
	Timeout             time.Duration
}

const defaultMaxRequestBodyBytes = 750_000

func New(options ClientOptions) *Client {
	maxRequestBodyBytes := options.MaxRequestBodyBytes
	if maxRequestBodyBytes <= 0 {
		maxRequestBodyBytes = defaultMaxRequestBodyBytes
	}

	return &Client{
		baseURL:             strings.TrimRight(options.BaseURL, "/"),
		apiKey:              options.APIKey,
		applicationID:       options.ApplicationID,
		maxRequestBodyBytes: maxRequestBodyBytes,
		client: &http.Client{
			Timeout: options.Timeout,
		},
	}
}

func (c *Client) SendTelemetry(ctx context.Context, signals []model.TelemetrySignal) error {
	if len(signals) == 0 {
		return nil
	}

	batches, err := telemetryBatches(signals, c.maxRequestBodyBytes)
	if err != nil {
		return err
	}

	for index, batch := range batches {
		if err := c.postJSON(ctx, "/v1/telemetry", batch); err != nil {
			return fmt.Errorf("send telemetry batch %d/%d: %w", index+1, len(batches), err)
		}
	}

	return nil
}

func telemetryBatches(signals []model.TelemetrySignal, maxRequestBodyBytes int) ([]model.TelemetryBatch, error) {
	const emptyBatchBytes = len(`{"signals":[]}`)

	batches := make([]model.TelemetryBatch, 0, 1)
	currentSignals := make([]model.TelemetrySignal, 0)
	currentBytes := emptyBatchBytes

	for index, signal := range signals {
		encodedSignal, err := json.Marshal(signal)
		if err != nil {
			return nil, fmt.Errorf("marshal telemetry signal %d: %w", index+1, err)
		}

		signalBytes := len(encodedSignal)
		if emptyBatchBytes+signalBytes > maxRequestBodyBytes {
			return nil, fmt.Errorf("telemetry signal %d requires %d bytes and exceeds request body limit %d", index+1, emptyBatchBytes+signalBytes, maxRequestBodyBytes)
		}

		separatorBytes := 0
		if len(currentSignals) > 0 {
			separatorBytes = 1
		}

		if currentBytes+separatorBytes+signalBytes > maxRequestBodyBytes {
			batches = append(batches, model.TelemetryBatch{Signals: currentSignals})
			currentSignals = make([]model.TelemetrySignal, 0)
			currentBytes = emptyBatchBytes
			separatorBytes = 0
		}

		currentSignals = append(currentSignals, signal)
		currentBytes += separatorBytes + signalBytes
	}

	if len(currentSignals) > 0 {
		batches = append(batches, model.TelemetryBatch{Signals: currentSignals})
	}

	return batches, nil
}

func (c *Client) postJSON(ctx context.Context, path string, payload any) error {
	request, err := c.newRequest(ctx, path, payload)
	if err != nil {
		return err
	}

	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("send telemetry request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("telemetry request failed with status %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	return nil
}

func (c *Client) newRequest(ctx context.Context, path string, payload any) (*http.Request, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal telemetry request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create telemetry request: %w", err)
	}

	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "inkronik-k8s-agent")
	if c.applicationID != "" {
		request.Header.Set("x-application-id", c.applicationID)
	}

	return request, nil
}
