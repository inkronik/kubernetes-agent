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
	baseURL       string
	apiKey        string
	applicationID string
	client        *http.Client
}

type ClientOptions struct {
	BaseURL       string
	APIKey        string
	ApplicationID string
	Timeout       time.Duration
}

func New(options ClientOptions) *Client {
	return &Client{
		baseURL:       strings.TrimRight(options.BaseURL, "/"),
		apiKey:        options.APIKey,
		applicationID: options.ApplicationID,
		client: &http.Client{
			Timeout: options.Timeout,
		},
	}
}

func (c *Client) SendTelemetry(ctx context.Context, signals []model.TelemetrySignal) error {
	if len(signals) == 0 {
		return nil
	}

	return c.postJSON(ctx, "/v1/telemetry", model.TelemetryBatch{Signals: signals})
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
