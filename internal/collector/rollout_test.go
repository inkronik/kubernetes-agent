package collector

import (
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/inkronik/kubernetes-agent/internal/model"
)

func rolloutDeployment(revision string, image string) appsv1.Deployment {
	return appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "api",
			Namespace:   "default",
			Annotations: map[string]string{"deployment.kubernetes.io/revision": revision},
			Labels:      map[string]string{"app.kubernetes.io/name": "api"},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: image}}},
			},
		},
	}
}

func rolloutCollector() *Collector {
	return New(Options{
		Cluster: ClusterMetadata{Name: "local", Provider: "kind", Environment: "test", AgentVersion: "test"},
	})
}

// The agent cannot distinguish "just started" from "deployed while I was down", so the first sighting only
// records the revision. Emitting here would invent a deploy on every agent restart.
func TestDeploymentRolloutSignalsIgnoresFirstObservation(t *testing.T) {
	collector := rolloutCollector()

	signals := collector.deploymentRolloutSignals(time.Now(), rolloutDeployment("3", "registry/api:1.4.2"), nil)

	if len(signals) != 0 {
		t.Fatalf("expected no signal on first observation, got %d", len(signals))
	}
}

func TestDeploymentRolloutSignalsEmitsOnRevisionChange(t *testing.T) {
	collector := rolloutCollector()
	now := time.Now()

	collector.deploymentRolloutSignals(now, rolloutDeployment("3", "registry/api:1.4.2"), nil)
	signals := collector.deploymentRolloutSignals(now, rolloutDeployment("4", "registry/api:1.4.3"), map[string]string{
		"inkronik.application_id": "app-uuid",
		"inkronik.service_name":   "checkout-api",
	})

	if len(signals) != 1 {
		t.Fatalf("expected one deployment signal, got %d", len(signals))
	}

	if signals[0].SignalType != "deployment_event" {
		t.Fatalf("expected a deployment_event, got %q", signals[0].SignalType)
	}

	payload, ok := signals[0].Payload.(model.DeploymentEventPayload)
	if !ok {
		t.Fatalf("expected a DeploymentEventPayload, got %T", signals[0].Payload)
	}

	if payload.Version != "1.4.3" {
		t.Errorf("expected version from the image tag, got %q", payload.Version)
	}

	if payload.DeploymentID != "k8s-default-api-4" {
		t.Errorf("expected a revision-keyed deployment id, got %q", payload.DeploymentID)
	}

	if payload.ServiceName != "checkout-api" {
		t.Errorf("expected the annotated service name, got %q", payload.ServiceName)
	}

	// The agent runs on an org-scoped ingest key, so this attribute is the only thing tying the marker to an
	// application. Losing it would make the deploy invisible on the application's tab.
	if payload.Attributes["inkronik.application_id"] != "app-uuid" {
		t.Errorf("expected the application id to ride in payload attributes, got %q", payload.Attributes["inkronik.application_id"])
	}

	// Kubernetes has no commit to report, and a fabricated one would be worse than none.
	if payload.CommitSha != "" {
		t.Errorf("expected no commit sha, got %q", payload.CommitSha)
	}
}

// A resync re-lists every Deployment unchanged; that must not read as a redeploy.
func TestDeploymentRolloutSignalsIgnoresUnchangedRevision(t *testing.T) {
	collector := rolloutCollector()
	now := time.Now()

	collector.deploymentRolloutSignals(now, rolloutDeployment("3", "registry/api:1.4.2"), nil)
	collector.deploymentRolloutSignals(now, rolloutDeployment("4", "registry/api:1.4.3"), nil)
	signals := collector.deploymentRolloutSignals(now, rolloutDeployment("4", "registry/api:1.4.3"), nil)

	if len(signals) != 0 {
		t.Fatalf("expected no signal for an unchanged revision, got %d", len(signals))
	}
}

func TestDeploymentRolloutSignalsSkipsWorkloadWithoutApplicationAssociation(t *testing.T) {
	collector := rolloutCollector()
	now := time.Now()

	collector.deploymentRolloutSignals(now, rolloutDeployment("3", "registry/redis:2.13.0"), nil)
	signals := collector.deploymentRolloutSignals(now, rolloutDeployment("4", "registry/redis:2.13.1-debian-12-r4"), nil)

	if len(signals) != 0 {
		t.Fatalf("expected no deployment signal for an unassociated workload, got %d", len(signals))
	}
}

func TestDeploymentRolloutSignalsSkipsUnversionedImages(t *testing.T) {
	collector := rolloutCollector()
	now := time.Now()

	collector.deploymentRolloutSignals(now, rolloutDeployment("3", "registry/api"), nil)
	signals := collector.deploymentRolloutSignals(now, rolloutDeployment("4", "registry/api"), nil)

	if len(signals) != 0 {
		t.Fatalf("expected no signal without a resolvable version, got %d", len(signals))
	}
}

func TestImageVersion(t *testing.T) {
	cases := []struct {
		name     string
		image    string
		expected string
	}{
		{name: "plain tag", image: "checkout-api:1.4.2", expected: "1.4.2"},
		{name: "registry and tag", image: "ghcr.io/org/checkout-api:1.4.2", expected: "1.4.2"},
		// The colon in a registry port must not be mistaken for the tag separator.
		{name: "registry port", image: "registry:5000/org/checkout-api:1.4.2", expected: "1.4.2"},
		{name: "untagged", image: "checkout-api", expected: ""},
		{name: "untagged with registry port", image: "registry:5000/checkout-api", expected: ""},
		{name: "digest pinned", image: "ghcr.io/org/api@sha256:abcdef0123456789abcdef", expected: "abcdef012345"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if version := imageVersion(testCase.image); version != testCase.expected {
				t.Errorf("expected %q, got %q", testCase.expected, version)
			}
		})
	}
}

func TestApplicationImageVersionSelectsMatchingContainer(t *testing.T) {
	podSpec := corev1.PodSpec{Containers: []corev1.Container{
		{Name: "sidecar", Image: "registry/sidecar:2.13.1-debian-12-r4"},
		{
			Name:  "api",
			Image: "harbor.codemask.dev/voice-analytics/voice-analytics-api:main-1f9b74a-1785308233",
			Env: []corev1.EnvVar{
				{Name: "INKRONIK_APPLICATION_ID", Value: "voice-api"},
				{Name: "INKRONIK_SERVICE_NAME", Value: "voice-analytics-api"},
			},
		},
	}}

	version := applicationImageVersion(podSpec, map[string]string{
		"inkronik.application_id": "voice-api",
		"inkronik.service_name":   "voice-analytics-api",
	})

	if version != "main-1f9b74a-1785308233" {
		t.Errorf("expected the explicitly associated application image, got %q", version)
	}
}

func TestApplicationImageVersionRejectsAmbiguousMultiContainerWorkload(t *testing.T) {
	podSpec := corev1.PodSpec{Containers: []corev1.Container{
		{Name: "api", Image: "registry/api:1.2.3"},
		{Name: "sidecar", Image: "registry/sidecar:4.5.6"},
	}}

	if version := applicationImageVersion(podSpec, map[string]string{"inkronik.application_id": "voice-api"}); version != "" {
		t.Errorf("expected no guessed version for an ambiguous workload, got %q", version)
	}
}

// The container ready/restart metrics must carry the parsed image version under k8s.image_version, produced by
// the same applicationImageVersion() that versions the rollout marker — that identity is what lets the writer join an app's
// telemetry to its release without re-parsing. A pod running an untagged image carries no version rather than a
// misleading one.
func TestPodStateSignalsStampImageVersion(t *testing.T) {
	collector := rolloutCollector()
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-api-7d9f", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "ghcr.io/org/checkout-api:1.4.3"}}},
		Status: corev1.PodStatus{
			Phase:             corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{Name: "api", Image: "ghcr.io/org/checkout-api:1.4.3", Ready: true}},
		},
	}

	version := containerMetricAttribute(t, collector.podStateSignals(time.Now(), pod), "k8s.container.ready", "k8s.image_version")

	if version != "1.4.3" {
		t.Errorf("expected k8s.image_version to be the parsed tag 1.4.3, got %q", version)
	}
}

func TestPodStateSignalsOmitsImageVersionForUntaggedImages(t *testing.T) {
	collector := rolloutCollector()
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-1", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "ghcr.io/org/checkout-api"}}},
		Status: corev1.PodStatus{
			Phase:             corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{Name: "api", Image: "ghcr.io/org/checkout-api", Ready: true}},
		},
	}

	// Empty attributes are dropped by mergeAttributes, so an untagged image leaves no k8s.image_version at all.
	if version := containerMetricAttribute(t, collector.podStateSignals(time.Now(), pod), "k8s.container.ready", "k8s.image_version"); version != "" {
		t.Errorf("expected no image version for an untagged image, got %q", version)
	}
}

func TestPodStateSignalsStampStartTimeAndApplicationImageVersion(t *testing.T) {
	collector := rolloutCollector()
	startedAt := metav1.NewTime(time.Date(2026, time.July, 29, 7, 1, 2, 123_000_000, time.UTC))
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-1",
			Namespace: "default",
			Annotations: map[string]string{
				"inkronik.com/application-id": "voice-api",
				"inkronik.com/service-name":   "voice-analytics-api",
			},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "sidecar", Image: "registry/sidecar:2.13.1-debian-12-r4"},
			{
				Name:  "api",
				Image: "registry/voice-api:main-1f9b74a-1785308233",
				Env:   []corev1.EnvVar{{Name: "INKRONIK_SERVICE_NAME", Value: "voice-analytics-api"}},
			},
		}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, StartTime: &startedAt},
	}

	signals := collector.podStateSignals(time.Now(), pod)

	if value := containerMetricAttribute(t, signals, "k8s.pod.phase", "k8s.pod.started_at"); value != "2026-07-29T07:01:02.123Z" {
		t.Errorf("expected Kubernetes pod start time, got %q", value)
	}
	if value := containerMetricAttribute(t, signals, "k8s.pod.phase", "k8s.image_version"); value != "main-1f9b74a-1785308233" {
		t.Errorf("expected application image version, got %q", value)
	}
}

func containerMetricAttribute(t *testing.T, signals []model.TelemetrySignal, metricName string, attribute string) string {
	t.Helper()

	for _, signal := range signals {
		payload, ok := signal.Payload.(model.MetricGaugePayload)
		if ok && payload.MetricName == metricName {
			return payload.ResourceAttributes[attribute]
		}
	}

	t.Fatalf("no %s metric found", metricName)

	return ""
}
