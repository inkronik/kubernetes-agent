package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/inkronik/kubernetes-agent/internal/version"
)

const defaultCollectorURL = "https://collector.inkronik.codemask.dev"

type Config struct {
	CollectorURL         string
	IngestAPIKey         string
	ApplicationID        string
	Environment          string
	ClusterName          string
	ClusterProvider      string
	AgentVersion         string
	HealthAddress        string
	KubeconfigPath       string
	Namespaces           []string
	EventTypes           []string
	MetricsInterval      time.Duration
	EventSyncInterval    time.Duration
	InitialEventLookback time.Duration
	RequestTimeout       time.Duration
	KubeletStatsEnabled  bool
}

func Load() (Config, error) {
	parseErrors := []string{}

	cfg := Config{
		CollectorURL:         strings.TrimRight(defaultString(os.Getenv("INKRONIK_COLLECTOR_URL"), defaultCollectorURL), "/"),
		IngestAPIKey:         os.Getenv("INKRONIK_INGEST_API_KEY"),
		ApplicationID:        os.Getenv("INKRONIK_APPLICATION_ID"),
		Environment:          defaultString(os.Getenv("INKRONIK_ENVIRONMENT"), "prod"),
		ClusterName:          os.Getenv("INKRONIK_CLUSTER_NAME"),
		ClusterProvider:      defaultString(os.Getenv("INKRONIK_CLUSTER_PROVIDER"), "kubernetes"),
		AgentVersion:         defaultString(os.Getenv("INKRONIK_K8S_AGENT_VERSION"), version.Value),
		HealthAddress:        defaultString(os.Getenv("INKRONIK_HEALTH_ADDR"), ":8080"),
		KubeconfigPath:       os.Getenv("INKRONIK_KUBECONFIG"),
		Namespaces:           splitCSV(os.Getenv("INKRONIK_NAMESPACES")),
		EventTypes:           defaultSlice(splitCSV(os.Getenv("INKRONIK_EVENT_TYPES")), []string{"Warning"}),
		MetricsInterval:      durationFromSeconds("INKRONIK_METRICS_INTERVAL_SECONDS", 30, &parseErrors),
		EventSyncInterval:    durationFromSeconds("INKRONIK_EVENT_SYNC_INTERVAL_SECONDS", 30, &parseErrors),
		InitialEventLookback: durationFromSeconds("INKRONIK_INITIAL_EVENT_LOOKBACK_SECONDS", 300, &parseErrors),
		RequestTimeout:       durationFromSeconds("INKRONIK_REQUEST_TIMEOUT_SECONDS", 10, &parseErrors),
		KubeletStatsEnabled:  boolFromEnv("INKRONIK_KUBELET_STATS_ENABLED", true, &parseErrors),
	}

	if len(parseErrors) > 0 {
		return Config{}, errors.New(strings.Join(parseErrors, "; "))
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	validationErrors := []string{}

	collectorURL, collectorURLError := url.ParseRequestURI(c.CollectorURL)
	if collectorURLError != nil || collectorURL.Scheme != "https" || collectorURL.Host == "" {
		validationErrors = append(validationErrors, "INKRONIK_COLLECTOR_URL must be a valid HTTPS URL")
	}

	if c.IngestAPIKey == "" {
		validationErrors = append(validationErrors, "INKRONIK_INGEST_API_KEY is required")
	}

	if c.ClusterName == "" {
		validationErrors = append(validationErrors, "INKRONIK_CLUSTER_NAME is required")
	}

	if c.MetricsInterval <= 0 {
		validationErrors = append(validationErrors, "INKRONIK_METRICS_INTERVAL_SECONDS must be greater than 0")
	}

	if c.EventSyncInterval <= 0 {
		validationErrors = append(validationErrors, "INKRONIK_EVENT_SYNC_INTERVAL_SECONDS must be greater than 0")
	}

	if c.RequestTimeout <= 0 {
		validationErrors = append(validationErrors, "INKRONIK_REQUEST_TIMEOUT_SECONDS must be greater than 0")
	}

	if len(validationErrors) > 0 {
		return errors.New(strings.Join(validationErrors, "; "))
	}

	return nil
}

func boolFromEnv(key string, fallback bool, validationErrors *[]string) bool {
	rawValue := os.Getenv(key)
	if rawValue == "" {
		return fallback
	}

	value, err := strconv.ParseBool(rawValue)
	if err != nil {
		*validationErrors = append(*validationErrors, fmt.Sprintf("%s must be a boolean, got %q", key, rawValue))
		return false
	}

	return value
}

func durationFromSeconds(key string, fallback int, validationErrors *[]string) time.Duration {
	rawValue := os.Getenv(key)
	if rawValue == "" {
		return time.Duration(fallback) * time.Second
	}

	value, err := strconv.Atoi(rawValue)
	if err != nil || value <= 0 {
		*validationErrors = append(*validationErrors, fmt.Sprintf("%s must be a positive integer, got %q", key, rawValue))
		return 0
	}

	return time.Duration(value) * time.Second
}

func splitCSV(value string) []string {
	if value == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))

	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}

	return result
}

func defaultString(value string, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}

func defaultSlice(value []string, fallback []string) []string {
	if len(value) == 0 {
		return fallback
	}

	return value
}
