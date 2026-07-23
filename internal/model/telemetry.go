package model

import "time"

type TelemetryBatch struct {
	Signals []TelemetrySignal `json:"signals"`
}

type TelemetrySignal struct {
	SignalType  string            `json:"signal_type"`
	Environment string            `json:"environment"`
	Timestamp   time.Time         `json:"timestamp"`
	Source      string            `json:"source"`
	Attributes  map[string]string `json:"attributes"`
	Payload     any               `json:"payload"`
}

type MetricGaugePayload struct {
	MetricKind         string            `json:"metric_kind"`
	ServiceName        string            `json:"service_name"`
	MetricName         string            `json:"metric_name"`
	Unit               string            `json:"unit"`
	Value              float64           `json:"value"`
	ResourceAttributes map[string]string `json:"resource_attributes"`
	MetricAttributes   map[string]string `json:"metric_attributes"`
}

// A rollout the agent witnessed. CommitSha, Repository, Branch and Actor stay empty: Kubernetes knows what image
// is running, not what produced it. Consumers must render the absence honestly rather than as a blank commit.
type DeploymentEventPayload struct {
	DeploymentID string            `json:"deployment_id"`
	ServiceName  string            `json:"service_name"`
	Version      string            `json:"version"`
	CommitSha    string            `json:"commit_sha"`
	Repository   string            `json:"repository"`
	Branch       string            `json:"branch"`
	Actor        string            `json:"actor"`
	Status       string            `json:"status"`
	Attributes   map[string]string `json:"attributes"`
}

type K8sEventPayload struct {
	EventID      string            `json:"event_id"`
	ClusterName  string            `json:"cluster_name"`
	Namespace    string            `json:"namespace"`
	ResourceKind string            `json:"resource_kind"`
	ResourceName string            `json:"resource_name"`
	Reason       string            `json:"reason"`
	EventType    string            `json:"event_type"`
	Message      string            `json:"message"`
	Count        int32             `json:"count"`
	FirstSeen    time.Time         `json:"first_seen"`
	LastSeen     time.Time         `json:"last_seen"`
	Attributes   map[string]string `json:"attributes"`
}
