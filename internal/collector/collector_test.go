package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	agentk8s "github.com/inkronik/kubernetes-agent/internal/k8s"
	"github.com/inkronik/kubernetes-agent/internal/model"
)

func TestCollectMetricsIncludesClusterStateSignals(t *testing.T) {
	replicas := int32(3)
	minReplicas := int32(2)
	targetCPU := int32(80)
	isController := true
	applicationEnv := []corev1.EnvVar{
		{Name: "INKRONIK_APPLICATION_ID", Value: "application-nest"},
		{Name: "INKRONIK_SERVICE_NAME", Value: "api"},
		{Name: "INKRONIK_INGEST_API_KEY", Value: "ik_live_abc123def45_secret"},
	}
	client := &agentk8s.Client{
		Kube: fake.NewSimpleClientset(
			&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:              resource.MustParse("4"),
						corev1.ResourceMemory:           resource.MustParse("8Gi"),
						corev1.ResourceEphemeralStorage: resource.MustParse("120Gi"),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:              resource.MustParse("3900m"),
						corev1.ResourceMemory:           resource.MustParse("7Gi"),
						corev1.ResourceEphemeralStorage: resource.MustParse("110Gi"),
					},
					Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue, Reason: "KubeletReady"}},
				},
			},
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "api-7d9",
					Namespace: "default",
					OwnerReferences: []metav1.OwnerReference{{
						Kind:       "ReplicaSet",
						Name:       "api-7d9",
						Controller: &isController,
					}},
				},
				Spec: corev1.PodSpec{
					NodeName: "node-a",
					Containers: []corev1.Container{{
						Name: "api",
						Env:  applicationEnv,
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("500m"),
								corev1.ResourceMemory: resource.MustParse("512Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("1"),
								corev1.ResourceMemory: resource.MustParse("1Gi"),
							},
						},
					}},
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:         "api",
							Ready:        true,
							RestartCount: 2,
							Image:        "api:1",
							ContainerID:  "containerd://abc",
							LastTerminationState: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"},
							},
						},
					},
				},
			},
			&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default", Labels: map[string]string{"app.kubernetes.io/name": "api"}},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Env: applicationEnv}}},
					},
				},
				Status: appsv1.DeploymentStatus{AvailableReplicas: 2, UpdatedReplicas: 3, UnavailableReplicas: 1},
			},
			&autoscalingv2.HorizontalPodAutoscaler{
				ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default", Labels: map[string]string{"app.kubernetes.io/name": "api"}},
				Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
					ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{APIVersion: "apps/v1", Kind: "Deployment", Name: "api"},
					MinReplicas:    &minReplicas,
					MaxReplicas:    8,
					Metrics: []autoscalingv2.MetricSpec{
						{
							Type: autoscalingv2.ResourceMetricSourceType,
							Resource: &autoscalingv2.ResourceMetricSource{
								Name:   corev1.ResourceCPU,
								Target: autoscalingv2.MetricTarget{Type: autoscalingv2.UtilizationMetricType, AverageUtilization: &targetCPU},
							},
						},
					},
				},
				Status: autoscalingv2.HorizontalPodAutoscalerStatus{CurrentReplicas: 3, DesiredReplicas: 4},
			},
			&appsv1.ReplicaSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "api-7d9",
					Namespace: "default",
					OwnerReferences: []metav1.OwnerReference{{
						Kind:       "Deployment",
						Name:       "api",
						Controller: &isController,
					}},
				},
				Spec: appsv1.ReplicaSetSpec{
					Replicas: &replicas,
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Env: applicationEnv}}},
					},
				},
				Status: appsv1.ReplicaSetStatus{ReadyReplicas: 2, AvailableReplicas: 2, Replicas: 3},
			},
		),
		Metrics: metricsfake.NewSimpleClientset(
			&metricsv1beta1.NodeMetrics{
				ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
				Usage: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("250m"),
					corev1.ResourceMemory: resource.MustParse("1Gi"),
				},
			},
			&metricsv1beta1.PodMetrics{
				ObjectMeta: metav1.ObjectMeta{Name: "api-7d9", Namespace: "default"},
				Containers: []metricsv1beta1.ContainerMetrics{
					{
						Name: "api",
						Usage: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("125m"),
							corev1.ResourceMemory: resource.MustParse("256Mi"),
						},
					},
				},
			},
		),
	}
	collector := New(Options{
		Client:     client,
		Cluster:    ClusterMetadata{Name: "local", Provider: "kind", Environment: "test", AgentVersion: "test"},
		EventTypes: []string{"Warning"},
	})

	signals, err := collector.CollectMetrics(context.Background())
	if err != nil {
		t.Fatalf("expected metrics collection to succeed: %v", err)
	}

	metricNames := metricNames(signals)
	for _, expected := range []string{
		"k8s.node.cpu.capacity",
		"k8s.node.cpu.allocatable",
		"k8s.node.memory.capacity",
		"k8s.node.memory.allocatable",
		"k8s.node.ephemeral_storage.capacity",
		"k8s.node.ephemeral_storage.allocatable",
		"k8s.node.condition",
		"k8s.pod.phase",
		"k8s.container.restart_count",
		"k8s.container.ready",
		"k8s.container.resource.info",
		"k8s.container.cpu.request",
		"k8s.container.cpu.limit",
		"k8s.container.memory.request",
		"k8s.container.memory.limit",
		"k8s.deployment.replicas.desired",
		"k8s.hpa.replicas.max",
		"k8s.hpa.replicas.desired",
		"k8s.hpa.target.cpu_utilization",
		"k8s.replicaset.replicas.ready",
	} {
		if !metricNames[expected] {
			t.Fatalf("expected metric %q in collected signals", expected)
		}
	}

	for _, metricName := range []string{
		"k8s.pod.phase",
		"k8s.container.ready",
		"k8s.deployment.replicas.available",
		"k8s.hpa.replicas.desired",
		"k8s.replicaset.replicas.ready",
	} {
		assertMetricResourceAttribute(t, signals, metricName, "inkronik.application_id", "application-nest")
		assertMetricResourceAttribute(t, signals, metricName, "inkronik.service_name", "api")
	}

	assertMetricResourceAttribute(t, signals, "k8s.pod.phase", "inkronik.ingest_key_prefix", "abc123def45")
	assertMetricResourceAttribute(t, signals, "k8s.pod.phase", "k8s.workload_kind", "Deployment")
	assertMetricResourceAttribute(t, signals, "k8s.pod.phase", "k8s.workload_name", "api")
	assertMetricResourceAttribute(t, signals, "k8s.container.restart_count", "k8s.last_termination_reason", "OOMKilled")
	assertMetricValue(t, signals, "k8s.container.cpu.request", 500)
	assertMetricValue(t, signals, "k8s.container.cpu.limit", 1000)
	assertMetricValue(t, signals, "k8s.container.memory.request", 512*1024*1024)
	assertMetricValue(t, signals, "k8s.container.memory.limit", 1024*1024*1024)
}

func TestCollectMetricsKeepsPodSignalsWhenOptionalResourceListsFail(t *testing.T) {
	applicationEnv := []corev1.EnvVar{
		{Name: "INKRONIK_APPLICATION_ID", Value: "application-nest"},
		{Name: "INKRONIK_SERVICE_NAME", Value: "api"},
	}
	kubeClient := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "api-7d9", Namespace: "default"},
			Spec: corev1.PodSpec{
				NodeName:   "node-a",
				Containers: []corev1.Container{{Name: "api", Env: applicationEnv}},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "api", Ready: true, RestartCount: 1, Image: "api:1", ContainerID: "containerd://abc"},
				},
			},
		},
	)
	for _, resource := range []string{"nodes", "deployments", "horizontalpodautoscalers", "replicasets"} {
		kubeClient.PrependReactor("list", resource, func(_ ktesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("forbidden")
		})
	}
	client := &agentk8s.Client{
		Kube: kubeClient,
		Metrics: metricsfake.NewSimpleClientset(
			&metricsv1beta1.NodeMetrics{
				ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
				Usage: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("250m"),
					corev1.ResourceMemory: resource.MustParse("1Gi"),
				},
			},
			&metricsv1beta1.PodMetrics{
				ObjectMeta: metav1.ObjectMeta{Name: "api-7d9", Namespace: "default"},
				Containers: []metricsv1beta1.ContainerMetrics{
					{
						Name: "api",
						Usage: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("125m"),
							corev1.ResourceMemory: resource.MustParse("256Mi"),
						},
					},
				},
			},
		),
	}
	collector := New(Options{
		Client:     client,
		Cluster:    ClusterMetadata{Name: "local", Provider: "kind", Environment: "test", AgentVersion: "test"},
		EventTypes: []string{"Warning"},
	})

	signals, err := collector.CollectMetrics(context.Background())
	if err != nil {
		t.Fatalf("expected metrics collection to degrade gracefully: %v", err)
	}

	metricNames := metricNames(signals)
	for _, expected := range []string{
		"k8s.pod.phase",
		"k8s.container.ready",
	} {
		if !metricNames[expected] {
			t.Fatalf("expected metric %q in collected signals", expected)
		}
	}

	assertMetricResourceAttribute(t, signals, "k8s.pod.phase", "inkronik.application_id", "application-nest")
	assertMetricResourceAttribute(t, signals, "k8s.pod.phase", "inkronik.service_name", "api")
}

func TestContainerResourceSignalsPreservePartialAndUnconfiguredContainers(t *testing.T) {
	collector := New(Options{
		Client:  &agentk8s.Client{},
		Cluster: ClusterMetadata{Name: "local", Provider: "kind", Environment: "test", AgentVersion: "test"},
	})
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-7d9", Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: "node-a",
			Containers: []corev1.Container{
				{
					Name: "api",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("512Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				},
				{
					Name: "proxy",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m")},
					},
				},
				{Name: "unconfigured-sidecar"},
			},
		},
	}

	signals := collector.containerResourceSignals(time.Now(), pod, map[string]string{"inkronik.application_id": "application-nest"})
	metricCounts := map[string]int{}
	for _, signal := range signals {
		payload, ok := signal.Payload.(model.MetricGaugePayload)
		if ok {
			metricCounts[payload.MetricName]++
		}
	}

	if metricCounts["k8s.container.resource.info"] != 3 {
		t.Fatalf("expected resource info for every container, got %d", metricCounts["k8s.container.resource.info"])
	}
	if metricCounts["k8s.container.cpu.request"] != 2 {
		t.Fatalf("expected CPU requests for configured and partial containers, got %d", metricCounts["k8s.container.cpu.request"])
	}
	for _, metricName := range []string{
		"k8s.container.cpu.limit",
		"k8s.container.memory.request",
		"k8s.container.memory.limit",
	} {
		if metricCounts[metricName] != 1 {
			t.Fatalf("expected only the configured container to emit %s, got %d", metricName, metricCounts[metricName])
		}
	}
}

func TestCollectEventsFiltersByTypeAndNamespace(t *testing.T) {
	now := metav1.Now()
	applicationEnv := []corev1.EnvVar{
		{Name: "INKRONIK_APPLICATION_ID", Value: "application-nest"},
		{Name: "INKRONIK_SERVICE_NAME", Value: "api"},
	}
	client := &agentk8s.Client{
		Kube: fake.NewSimpleClientset(
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "api-7d9", Namespace: "default"},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "api", Env: applicationEnv}},
				},
			},
			&corev1.Event{
				ObjectMeta:    metav1.ObjectMeta{Name: "warning", Namespace: "default", CreationTimestamp: now},
				LastTimestamp: now,
				InvolvedObject: corev1.ObjectReference{
					Kind: "Pod",
					Name: "api-7d9",
				},
				Type:    "Warning",
				Reason:  "BackOff",
				Message: "Back-off restarting failed container",
				Count:   1,
			},
			&corev1.Event{
				ObjectMeta:    metav1.ObjectMeta{Name: "normal", Namespace: "default", CreationTimestamp: now},
				LastTimestamp: now,
				InvolvedObject: corev1.ObjectReference{
					Kind: "Pod",
					Name: "api-7d9",
				},
				Type:    "Normal",
				Reason:  "Pulled",
				Message: "Container image pulled",
				Count:   1,
			},
		),
		Metrics: metricsfake.NewSimpleClientset(),
	}
	collector := New(Options{
		Client:     client,
		Cluster:    ClusterMetadata{Name: "local", Provider: "kind", Environment: "test", AgentVersion: "test"},
		Namespaces: []string{"default"},
		EventTypes: []string{"Warning"},
	})

	signals, _, err := collector.CollectEvents(context.Background(), now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("expected event collection to succeed: %v", err)
	}

	if len(signals) != 1 {
		t.Fatalf("expected one warning event signal, got %d", len(signals))
	}

	if signals[0].SignalType != "k8s_event" {
		t.Fatalf("expected k8s_event signal, got %q", signals[0].SignalType)
	}

	payload, ok := signals[0].Payload.(model.K8sEventPayload)
	if !ok {
		t.Fatalf("expected k8s event payload, got %T", signals[0].Payload)
	}

	if payload.Attributes["inkronik.application_id"] != "application-nest" {
		t.Fatalf("expected k8s event to inherit pod application id, got %q", payload.Attributes["inkronik.application_id"])
	}
}

func TestNetworkCountersFallsBackToNodeInterfaces(t *testing.T) {
	result := networkCounters(agentk8s.NetworkStats{
		Interfaces: []agentk8s.NetworkInterfaceStats{
			{Name: "eth0", RxBytes: 100, TxBytes: 50, RxErrors: 1, TxErrors: 2},
			{Name: "cni0", RxBytes: 300, TxBytes: 70, RxErrors: 3, TxErrors: 4},
		},
	}, nodeNetworkMetricScope)

	if result.rxBytes != 100 {
		t.Fatalf("expected rx bytes from interfaces, got %d", result.rxBytes)
	}

	if result.txBytes != 50 {
		t.Fatalf("expected tx bytes from interfaces, got %d", result.txBytes)
	}

	if result.rxErrors != 1 {
		t.Fatalf("expected rx errors from interfaces, got %d", result.rxErrors)
	}

	if result.txErrors != 2 {
		t.Fatalf("expected tx errors from interfaces, got %d", result.txErrors)
	}
}

func TestNetworkCountersSumsPodInterfaces(t *testing.T) {
	result := networkCounters(agentk8s.NetworkStats{
		Interfaces: []agentk8s.NetworkInterfaceStats{
			{Name: "eth0", RxBytes: 100, TxBytes: 50, RxErrors: 1, TxErrors: 2},
			{Name: "net1", RxBytes: 300, TxBytes: 70, RxErrors: 3, TxErrors: 4},
		},
	}, podNetworkMetricScope)

	if result.rxBytes != 400 {
		t.Fatalf("expected rx bytes from interfaces, got %d", result.rxBytes)
	}

	if result.txBytes != 120 {
		t.Fatalf("expected tx bytes from interfaces, got %d", result.txBytes)
	}

	if result.rxErrors != 4 {
		t.Fatalf("expected rx errors from interfaces, got %d", result.rxErrors)
	}

	if result.txErrors != 6 {
		t.Fatalf("expected tx errors from interfaces, got %d", result.txErrors)
	}
}

func metricNames(signals []model.TelemetrySignal) map[string]bool {
	names := map[string]bool{}
	for _, signal := range signals {
		payload, ok := signal.Payload.(model.MetricGaugePayload)
		if ok {
			names[payload.MetricName] = true
		}
	}

	return names
}

func assertMetricResourceAttribute(t *testing.T, signals []model.TelemetrySignal, metricName string, attributeName string, expectedValue string) {
	t.Helper()

	for _, signal := range signals {
		payload, ok := signal.Payload.(model.MetricGaugePayload)
		if !ok || payload.MetricName != metricName {
			continue
		}

		if payload.ResourceAttributes[attributeName] != expectedValue {
			t.Fatalf("expected %s %s=%q, got %q", metricName, attributeName, expectedValue, payload.ResourceAttributes[attributeName])
		}

		return
	}

	t.Fatalf("expected metric %q in collected signals", metricName)
}

func assertMetricValue(t *testing.T, signals []model.TelemetrySignal, metricName string, expectedValue float64) {
	t.Helper()

	for _, signal := range signals {
		payload, ok := signal.Payload.(model.MetricGaugePayload)
		if !ok || payload.MetricName != metricName {
			continue
		}

		if payload.Value != expectedValue {
			t.Fatalf("expected %s value %v, got %v", metricName, expectedValue, payload.Value)
		}

		return
	}

	t.Fatalf("expected metric %q", metricName)
}
