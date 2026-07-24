package config

import (
	"strings"
	"testing"
)

func TestLoadUsesHostedCollectorByDefault(t *testing.T) {
	t.Setenv("INKRONIK_COLLECTOR_URL", "")
	t.Setenv("INKRONIK_INGEST_API_KEY", "ik_live_prefix_secret")
	t.Setenv("INKRONIK_CLUSTER_NAME", "staging-eu")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected config to load: %v", err)
	}

	if cfg.CollectorURL != defaultCollectorURL {
		t.Fatalf("expected default collector URL %q, got %q", defaultCollectorURL, cfg.CollectorURL)
	}
	if !cfg.KubeletStatsEnabled {
		t.Fatal("expected kubelet stats to be enabled by default")
	}
}

func TestLoadRejectsNonHTTPSCollector(t *testing.T) {
	t.Setenv("INKRONIK_COLLECTOR_URL", "http://collector.example.test")
	t.Setenv("INKRONIK_INGEST_API_KEY", "ik_live_prefix_secret")
	t.Setenv("INKRONIK_CLUSTER_NAME", "staging-eu")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "must be a valid HTTPS URL") {
		t.Fatalf("expected HTTPS validation error, got %v", err)
	}
}

func TestLoadAllowsKubeletStatsOptOut(t *testing.T) {
	t.Setenv("INKRONIK_INGEST_API_KEY", "ik_live_prefix_secret")
	t.Setenv("INKRONIK_CLUSTER_NAME", "staging-eu")
	t.Setenv("INKRONIK_KUBELET_STATS_ENABLED", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected config to load: %v", err)
	}

	if cfg.KubeletStatsEnabled {
		t.Fatal("expected kubelet stats opt-out to be disabled")
	}
}

func TestLoadAllowsApplicationScopedKeysWithoutApplicationID(t *testing.T) {
	t.Setenv("INKRONIK_COLLECTOR_URL", "https://collector.inkronik.test/")
	t.Setenv("INKRONIK_INGEST_API_KEY", "ik_live_prefix_secret")
	t.Setenv("INKRONIK_CLUSTER_NAME", "staging-eu")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected config to load without application id: %v", err)
	}

	if cfg.ApplicationID != "" {
		t.Fatalf("expected optional application id to be empty, got %q", cfg.ApplicationID)
	}

	if cfg.HealthAddress != ":8080" {
		t.Fatalf("expected default health address, got %q", cfg.HealthAddress)
	}

	if cfg.AgentVersion != "dev" {
		t.Fatalf("expected build-time agent version, got %q", cfg.AgentVersion)
	}
}

func TestLoadKeepsLegacyApplicationIDWhenProvided(t *testing.T) {
	t.Setenv("INKRONIK_COLLECTOR_URL", "https://collector.inkronik.test/")
	t.Setenv("INKRONIK_INGEST_API_KEY", "ik_live_prefix_secret")
	t.Setenv("INKRONIK_APPLICATION_ID", "00000000-0000-4000-8000-000000000201")
	t.Setenv("INKRONIK_CLUSTER_NAME", "staging-eu")
	t.Setenv("INKRONIK_HEALTH_ADDR", ":9090")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected config to load with legacy application id: %v", err)
	}

	if cfg.ApplicationID != "00000000-0000-4000-8000-000000000201" {
		t.Fatalf("expected legacy application id to be preserved, got %q", cfg.ApplicationID)
	}

	if cfg.HealthAddress != ":9090" {
		t.Fatalf("expected configured health address, got %q", cfg.HealthAddress)
	}
}

func TestLoadAllowsAgentVersionOverride(t *testing.T) {
	t.Setenv("INKRONIK_COLLECTOR_URL", "https://collector.inkronik.test/")
	t.Setenv("INKRONIK_INGEST_API_KEY", "ik_live_prefix_secret")
	t.Setenv("INKRONIK_CLUSTER_NAME", "staging-eu")
	t.Setenv("INKRONIK_K8S_AGENT_VERSION", "1.2.3-custom")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected config to load: %v", err)
	}

	if cfg.AgentVersion != "1.2.3-custom" {
		t.Fatalf("expected configured agent version, got %q", cfg.AgentVersion)
	}
}
