package collector

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"

	"github.com/inkronik/kubernetes-agent/internal/k8s"
	"github.com/inkronik/kubernetes-agent/internal/model"
)

const agentSource = "inkronik-k8s-agent"
const ingestAPIKeyPrefix = "ik_live_"
const ingestAPIKeyVisiblePrefixLength = 11

type Collector struct {
	client             *k8s.Client
	cluster            ClusterMetadata
	namespaces         []string
	allowedEventTypes  map[string]struct{}
	enableKubeletStats bool
	// Last revision seen per workload, so a rollout is reported once rather than on every collection tick. The
	// runner drives collection from a single goroutine today; the mutex keeps that from being load-bearing.
	revisionsMutex      sync.Mutex
	revisionsByWorkload map[string]string
}

type networkMetricSignalsInput struct {
	timestamp    time.Time
	metricPrefix string
	network      k8s.NetworkStats
	scope        networkMetricScope
	attributes   map[string]string
}

type networkMetricScope string

const (
	nodeNetworkMetricScope networkMetricScope = "node"
	podNetworkMetricScope  networkMetricScope = "pod"
)

type nodeResourceSignalsInput struct {
	ctx       context.Context
	timestamp time.Time
	node      corev1.Node
	podsByKey map[string]corev1.Pod
}

type workloadAttributesInput struct {
	kind                  string
	namespace             string
	name                  string
	labels                map[string]string
	applicationAttributes map[string]string
}

type hpaAttributesInput struct {
	hpa                   autoscalingv2.HorizontalPodAutoscaler
	applicationAttributes map[string]string
}

func New(options Options) *Collector {
	allowedTypes := make(map[string]struct{}, len(options.EventTypes))
	for _, eventType := range options.EventTypes {
		if eventType != "" {
			allowedTypes[eventType] = struct{}{}
		}
	}

	return &Collector{
		client:              options.Client,
		cluster:             options.Cluster,
		namespaces:          slices.Clone(options.Namespaces),
		allowedEventTypes:   allowedTypes,
		enableKubeletStats:  options.EnableKubeletStats,
		revisionsByWorkload: map[string]string{},
	}
}

func (c *Collector) CollectMetrics(ctx context.Context) ([]model.TelemetrySignal, error) {
	nodeMetrics, err := c.client.Metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		nodeMetrics = &metricsv1beta1.NodeMetricsList{}
	}

	podMetrics, err := c.listPodMetrics(ctx)
	if err != nil {
		podMetrics = []metricsv1beta1.PodMetrics{}
	}
	pods, err := c.listPods(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	nodes, err := c.client.Kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		slog.Warn("list nodes failed", slog.Any("error", err))
		nodes = &corev1.NodeList{}
	}
	deployments, err := c.listDeployments(ctx)
	if err != nil {
		slog.Warn("list deployments failed", slog.Any("error", err))
		deployments = []appsv1.Deployment{}
	}
	horizontalPodAutoscalers, err := c.listHorizontalPodAutoscalers(ctx)
	if err != nil {
		slog.Warn("list horizontal pod autoscalers failed", slog.Any("error", err))
		horizontalPodAutoscalers = []autoscalingv2.HorizontalPodAutoscaler{}
	}
	replicaSets, err := c.listReplicaSets(ctx)
	if err != nil {
		slog.Warn("list replicasets failed", slog.Any("error", err))
		replicaSets = []appsv1.ReplicaSet{}
	}

	now := time.Now().UTC()
	podsByKey := podsByNamespacedName(pods)
	applicationAttributesByWorkloadID := workloadApplicationAttributesByID(deployments, replicaSets)
	signals := make([]model.TelemetrySignal, 0, len(nodeMetrics.Items)*4+len(nodes.Items)*14+len(podMetrics)*2+len(pods)*12+len(deployments)*4+len(replicaSets)*4)

	for _, nodeMetric := range nodeMetrics.Items {
		attributes := c.baseAttributes(map[string]string{
			"k8s.scope": "node",
			"k8s.node":  nodeMetric.Name,
		})

		signals = append(signals,
			c.metricSignal(now, "k8s.node.cpu.usage", "mcores", float64(nodeMetric.Usage.Cpu().MilliValue()), attributes),
			c.metricSignal(now, "k8s.node.memory.usage", "bytes", float64(nodeMetric.Usage.Memory().Value()), attributes),
		)
	}

	for _, node := range nodes.Items {
		signals = append(signals, c.nodeResourceSignals(nodeResourceSignalsInput{ctx: ctx, timestamp: now, node: node, podsByKey: podsByKey})...)
		signals = append(signals, c.nodeConditionSignals(now, node)...)
	}

	for _, podMetric := range podMetrics {
		cpuUsage, memoryUsage := sumPodMetricUsage(podMetric)
		pod := podsByKey[namespacedName(podMetric.Namespace, podMetric.Name)]
		attributes := c.baseAttributes(map[string]string{
			"k8s.scope":     "pod",
			"k8s.namespace": podMetric.Namespace,
			"k8s.pod":       podMetric.Name,
			"k8s.node":      pod.Spec.NodeName,
		})
		attributes = mergeAttributes(attributes, applicationAttributesFromPod(pod))

		signals = append(signals,
			c.metricSignal(now, "k8s.pod.cpu.usage", "mcores", float64(cpuUsage), attributes),
			c.metricSignal(now, "k8s.pod.memory.usage", "bytes", float64(memoryUsage), attributes),
		)
	}

	for _, pod := range pods {
		signals = append(signals, c.podStateSignals(now, pod)...)
	}

	for _, deployment := range deployments {
		signals = append(signals, c.deploymentStateSignals(now, deployment)...)
		signals = append(signals, c.deploymentRolloutSignals(
			now,
			deployment,
			applicationAttributesFromDeployment(deployment),
		)...)
	}

	for _, horizontalPodAutoscaler := range horizontalPodAutoscalers {
		targetWorkloadID := workloadID(strings.ToLower(horizontalPodAutoscaler.Spec.ScaleTargetRef.Kind), horizontalPodAutoscaler.Namespace, horizontalPodAutoscaler.Spec.ScaleTargetRef.Name)
		signals = append(signals, c.horizontalPodAutoscalerSignals(now, horizontalPodAutoscaler, applicationAttributesByWorkloadID[targetWorkloadID])...)
	}

	for _, replicaSet := range replicaSets {
		signals = append(signals, c.replicaSetStateSignals(now, replicaSet)...)
	}

	return signals, nil
}

func (c *Collector) CollectEvents(ctx context.Context, since time.Time) ([]model.TelemetrySignal, time.Time, error) {
	events, err := c.listEvents(ctx)
	if err != nil {
		return nil, since, fmt.Errorf("list events: %w", err)
	}
	pods, err := c.listPods(ctx)
	if err != nil {
		slog.Warn("list pods for event attribution failed", slog.Any("error", err))
		pods = []corev1.Pod{}
	}

	signals := make([]model.TelemetrySignal, 0, len(events))
	latestTimestamp := since
	podsByKey := podsByNamespacedName(pods)

	for _, event := range events {
		if !c.isAllowedEventType(event.Type) {
			continue
		}

		timestamp := getEventTimestamp(event)
		if !timestamp.After(since) {
			continue
		}

		if timestamp.After(latestTimestamp) {
			latestTimestamp = timestamp
		}

		signals = append(signals, c.k8sEventSignal(event, timestamp, podsByKey))
	}

	slices.SortFunc(signals, func(left model.TelemetrySignal, right model.TelemetrySignal) int {
		return left.Timestamp.Compare(right.Timestamp)
	})

	return signals, latestTimestamp, nil
}

func (c *Collector) metricSignal(timestamp time.Time, metricName string, unit string, value float64, attributes map[string]string) model.TelemetrySignal {
	return model.TelemetrySignal{
		SignalType:  "metric",
		Environment: c.cluster.Environment,
		Timestamp:   timestamp,
		Source:      agentSource,
		Attributes:  c.baseAttributes(nil),
		Payload: model.MetricGaugePayload{
			MetricKind:         "gauge",
			ServiceName:        "kubernetes",
			MetricName:         metricName,
			Unit:               unit,
			Value:              value,
			ResourceAttributes: attributes,
			MetricAttributes:   map[string]string{},
		},
	}
}

func (c *Collector) nodeResourceSignals(input nodeResourceSignalsInput) []model.TelemetrySignal {
	node := input.node
	attributes := c.baseAttributes(map[string]string{
		"k8s.scope": "node",
		"k8s.node":  node.Name,
	})
	signals := []model.TelemetrySignal{
		c.metricSignal(input.timestamp, "k8s.node.cpu.capacity", "mcores", float64(node.Status.Capacity.Cpu().MilliValue()), attributes),
		c.metricSignal(input.timestamp, "k8s.node.cpu.allocatable", "mcores", float64(node.Status.Allocatable.Cpu().MilliValue()), attributes),
		c.metricSignal(input.timestamp, "k8s.node.memory.capacity", "bytes", float64(node.Status.Capacity.Memory().Value()), attributes),
		c.metricSignal(input.timestamp, "k8s.node.memory.allocatable", "bytes", float64(node.Status.Allocatable.Memory().Value()), attributes),
		c.metricSignal(input.timestamp, "k8s.node.ephemeral_storage.capacity", "bytes", float64(node.Status.Capacity.StorageEphemeral().Value()), attributes),
		c.metricSignal(input.timestamp, "k8s.node.ephemeral_storage.allocatable", "bytes", float64(node.Status.Allocatable.StorageEphemeral().Value()), attributes),
	}
	if !c.enableKubeletStats {
		return signals
	}

	summary, err := c.client.NodeStatsSummary(input.ctx, node.Name)
	if err != nil {
		slog.Warn("node filesystem stats unavailable", slog.String("node", node.Name), slog.Any("error", err))
		return signals
	}

	signals = append(signals,
		c.metricSignal(input.timestamp, "k8s.node.filesystem.capacity", "bytes", float64(summary.Node.Fs.CapacityBytes), attributes),
		c.metricSignal(input.timestamp, "k8s.node.filesystem.available", "bytes", float64(summary.Node.Fs.AvailableBytes), attributes),
		c.metricSignal(input.timestamp, "k8s.node.filesystem.used", "bytes", float64(summary.Node.Fs.UsedBytes), attributes),
		c.metricSignal(input.timestamp, "k8s.node.filesystem.inodes", "inodes", float64(summary.Node.Fs.Inodes), attributes),
		c.metricSignal(input.timestamp, "k8s.node.filesystem.inodes_free", "inodes", float64(summary.Node.Fs.InodesFree), attributes),
		c.metricSignal(input.timestamp, "k8s.node.filesystem.inodes_used", "inodes", float64(summary.Node.Fs.InodesUsed), attributes),
	)
	signals = append(signals, c.networkMetricSignals(networkMetricSignalsInput{
		timestamp:    input.timestamp,
		metricPrefix: "k8s.node.network",
		network:      summary.Node.Network,
		scope:        nodeNetworkMetricScope,
		attributes:   attributes,
	})...)

	for _, podStats := range summary.Pods {
		pod, ok := input.podsByKey[namespacedName(podStats.PodRef.Namespace, podStats.PodRef.Name)]
		if !ok {
			continue
		}

		signals = append(signals, c.networkMetricSignals(networkMetricSignalsInput{
			timestamp:    input.timestamp,
			metricPrefix: "k8s.pod.network",
			network:      podStats.Network,
			scope:        podNetworkMetricScope,
			attributes:   c.podNetworkAttributes(podStats, pod),
		})...)
	}

	return signals
}

func (c *Collector) networkMetricSignals(input networkMetricSignalsInput) []model.TelemetrySignal {
	if !hasNetworkStats(input.network) {
		return nil
	}

	counters := networkCounters(input.network, input.scope)

	return []model.TelemetrySignal{
		c.metricSignal(input.timestamp, input.metricPrefix+".rx_bytes", "bytes", float64(counters.rxBytes), input.attributes),
		c.metricSignal(input.timestamp, input.metricPrefix+".tx_bytes", "bytes", float64(counters.txBytes), input.attributes),
		c.metricSignal(input.timestamp, input.metricPrefix+".rx_errors", "errors", float64(counters.rxErrors), input.attributes),
		c.metricSignal(input.timestamp, input.metricPrefix+".tx_errors", "errors", float64(counters.txErrors), input.attributes),
	}
}

func (c *Collector) podNetworkAttributes(podStats k8s.PodStats, pod corev1.Pod) map[string]string {
	return mergeAttributes(c.baseAttributes(map[string]string{
		"k8s.scope":     "pod",
		"k8s.namespace": podStats.PodRef.Namespace,
		"k8s.pod":       podStats.PodRef.Name,
		"k8s.pod_uid":   podStats.PodRef.UID,
		"k8s.node":      pod.Spec.NodeName,
		"k8s.app":       pod.Labels["app"],
		"k8s.app_name":  pod.Labels["app.kubernetes.io/name"],
		"k8s.version":   pod.Labels["app.kubernetes.io/version"],
	}), applicationAttributesFromPod(pod))
}

func (c *Collector) nodeConditionSignals(timestamp time.Time, node corev1.Node) []model.TelemetrySignal {
	signals := make([]model.TelemetrySignal, 0, len(node.Status.Conditions))

	for _, condition := range node.Status.Conditions {
		attributes := c.baseAttributes(map[string]string{
			"k8s.scope":            "node",
			"k8s.node":             node.Name,
			"k8s.node_condition":   string(condition.Type),
			"k8s.condition_status": string(condition.Status),
			"k8s.condition_reason": condition.Reason,
		})
		signals = append(signals, c.metricSignal(timestamp, "k8s.node.condition", "state", conditionStatusValue(condition.Status), attributes))
	}

	return signals
}

func (c *Collector) podStateSignals(timestamp time.Time, pod corev1.Pod) []model.TelemetrySignal {
	applicationAttributes := applicationAttributesFromPod(pod)
	// Parsed with the SAME function that versions the rollout marker, from the pod's own spec image — so the
	// version stamped here is byte-identical to the marker's, and the writer can join app telemetry to it
	// without re-parsing. Per-pod (not the deployment template) is correct mid-rollout: old pods keep the old tag.
	imageVersionAttribute := applicationImageVersion(pod.Spec, applicationAttributes)
	podStartedAt := ""
	if pod.Status.StartTime != nil {
		podStartedAt = pod.Status.StartTime.UTC().Format(time.RFC3339Nano)
	}
	phaseAttributes := mergeAttributes(c.baseAttributes(map[string]string{
		"k8s.scope":          "pod",
		"k8s.namespace":      pod.Namespace,
		"k8s.pod":            pod.Name,
		"k8s.node":           pod.Spec.NodeName,
		"k8s.pod_phase":      string(pod.Status.Phase),
		"k8s.pod.started_at": podStartedAt,
		"k8s.image_version":  imageVersionAttribute,
	}), applicationAttributes)
	signals := []model.TelemetrySignal{
		c.metricSignal(timestamp, "k8s.pod.phase", "state", 1, phaseAttributes),
	}
	signals = append(signals, c.containerResourceSignals(timestamp, pod, applicationAttributes)...)

	for _, status := range pod.Status.ContainerStatuses {
		terminationReason := ""
		if status.LastTerminationState.Terminated != nil {
			terminationReason = status.LastTerminationState.Terminated.Reason
		} else if status.State.Terminated != nil {
			terminationReason = status.State.Terminated.Reason
		}
		attributes := mergeAttributes(c.baseAttributes(map[string]string{
			"k8s.scope":                   "container",
			"k8s.namespace":               pod.Namespace,
			"k8s.pod":                     pod.Name,
			"k8s.container":               status.Name,
			"k8s.container_id":            status.ContainerID,
			"k8s.image":                   status.Image,
			"k8s.image_version":           imageVersionAttribute,
			"k8s.node":                    pod.Spec.NodeName,
			"k8s.last_termination_reason": terminationReason,
		}), applicationAttributes)
		signals = append(signals,
			c.metricSignal(timestamp, "k8s.container.restart_count", "restarts", float64(status.RestartCount), attributes),
			c.metricSignal(timestamp, "k8s.container.ready", "state", boolValue(status.Ready), attributes),
		)
	}

	return signals
}

func (c *Collector) containerResourceSignals(timestamp time.Time, pod corev1.Pod, applicationAttributes map[string]string) []model.TelemetrySignal {
	signals := []model.TelemetrySignal{}

	for _, container := range pod.Spec.Containers {
		attributes := mergeAttributes(c.baseAttributes(map[string]string{
			"k8s.scope":     "container",
			"k8s.namespace": pod.Namespace,
			"k8s.pod":       pod.Name,
			"k8s.container": container.Name,
			"k8s.image":     container.Image,
			"k8s.node":      pod.Spec.NodeName,
		}), applicationAttributes)
		signals = append(signals, c.metricSignal(timestamp, "k8s.container.resource.info", "state", 1, attributes))

		if quantity, exists := container.Resources.Requests[corev1.ResourceCPU]; exists {
			signals = append(signals, c.metricSignal(timestamp, "k8s.container.cpu.request", "mcores", float64(quantity.MilliValue()), attributes))
		}
		if quantity, exists := container.Resources.Limits[corev1.ResourceCPU]; exists {
			signals = append(signals, c.metricSignal(timestamp, "k8s.container.cpu.limit", "mcores", float64(quantity.MilliValue()), attributes))
		}
		if quantity, exists := container.Resources.Requests[corev1.ResourceMemory]; exists {
			signals = append(signals, c.metricSignal(timestamp, "k8s.container.memory.request", "bytes", float64(quantity.Value()), attributes))
		}
		if quantity, exists := container.Resources.Limits[corev1.ResourceMemory]; exists {
			signals = append(signals, c.metricSignal(timestamp, "k8s.container.memory.limit", "bytes", float64(quantity.Value()), attributes))
		}
	}

	return signals
}

func (c *Collector) deploymentStateSignals(timestamp time.Time, deployment appsv1.Deployment) []model.TelemetrySignal {
	attributes := c.workloadAttributes(workloadAttributesInput{
		kind:                  "deployment",
		namespace:             deployment.Namespace,
		name:                  deployment.Name,
		labels:                deployment.Labels,
		applicationAttributes: applicationAttributesFromDeployment(deployment),
	})

	return []model.TelemetrySignal{
		c.metricSignal(timestamp, "k8s.deployment.replicas.desired", "replicas", float64(orZero(deployment.Spec.Replicas)), attributes),
		c.metricSignal(timestamp, "k8s.deployment.replicas.available", "replicas", float64(deployment.Status.AvailableReplicas), attributes),
		c.metricSignal(timestamp, "k8s.deployment.replicas.updated", "replicas", float64(deployment.Status.UpdatedReplicas), attributes),
		c.metricSignal(timestamp, "k8s.deployment.replicas.unavailable", "replicas", float64(deployment.Status.UnavailableReplicas), attributes),
	}
}

// Reports a rollout that had no CI involvement — someone ran `kubectl set image`, or a GitOps controller synced a
// new tag. GitHub never sees these, so without this the Deployments tab would silently omit them.
//
// Emitted only on an observed transition. The first sighting of a workload records its revision without emitting:
// the agent cannot tell a fresh start from a deploy that happened while it was down, and inventing a deploy on
// every agent restart would be worse than missing one.
func (c *Collector) deploymentRolloutSignals(timestamp time.Time, deployment appsv1.Deployment, applicationAttributes map[string]string) []model.TelemetrySignal {
	revision := deployment.Annotations["deployment.kubernetes.io/revision"]
	version := applicationImageVersion(deployment.Spec.Template.Spec, applicationAttributes)

	// No revision means this is not a rollout-managed Deployment; no version means nothing to attribute telemetry
	// to, which is the entire point of the marker.
	if revision == "" || version == "" {
		return nil
	}

	id := workloadID("deployment", deployment.Namespace, deployment.Name)

	if !c.recordRevision(id, revision) {
		return nil
	}
	if applicationAttributes["inkronik.application_id"] == "" {
		return nil
	}

	serviceName := applicationAttributes["inkronik.service_name"]
	if serviceName == "" {
		serviceName = deployment.Name
	}

	return []model.TelemetrySignal{{
		SignalType:  "deployment_event",
		Environment: c.cluster.Environment,
		Timestamp:   timestamp,
		Source:      agentSource,
		Attributes:  c.baseAttributes(nil),
		Payload: model.DeploymentEventPayload{
			DeploymentID: fmt.Sprintf("k8s-%s-%s-%s", deployment.Namespace, deployment.Name, revision),
			ServiceName:  serviceName,
			Version:      version,
			// Kubernetes has no commit, repository, branch or author to report. Left empty rather than guessed.
			Status: "success",
			// The agent runs on an organisation-scoped ingest key, so `application_id` on the row comes from the
			// key, not from this workload. Attribution rides in the payload attributes under the same key the
			// pod-level k8s metrics already use (see migration 0007's effective-application expression).
			Attributes: mergeAttributes(applicationAttributes, map[string]string{
				"k8s.namespace":  deployment.Namespace,
				"k8s.deployment": deployment.Name,
				"k8s.revision":   revision,
				"deploy.source":  "kubernetes",
			}),
		},
	}}
}

// Returns true when the revision differs from the one last seen, recording it either way. The first observation
// of a workload always returns false — see deploymentRolloutSignals.
func (c *Collector) recordRevision(id string, revision string) bool {
	c.revisionsMutex.Lock()
	defer c.revisionsMutex.Unlock()

	previous, seen := c.revisionsByWorkload[id]
	c.revisionsByWorkload[id] = revision

	return seen && previous != revision
}

// Selects the application container explicitly when a workload has sidecars. A single-container workload is
// unambiguous; a multi-container workload without matching Inkronik environment metadata is deliberately left
// unattributed rather than turning the first sidecar image into the application's release version.
func applicationImageVersion(podSpec corev1.PodSpec, applicationAttributes map[string]string) string {
	containers := podSpec.Containers
	if len(containers) == 0 {
		return ""
	}
	if len(containers) == 1 {
		return imageVersion(containers[0].Image)
	}

	serviceName := applicationAttributes["inkronik.service_name"]
	serviceMatches := slices.DeleteFunc(slices.Clone(containers), func(container corev1.Container) bool {
		return serviceName == "" || firstNonEmpty([]string{containerEnvValue(container, "INKRONIK_SERVICE_NAME"), containerEnvValue(container, "OTEL_SERVICE_NAME")}) != serviceName
	})
	if len(serviceMatches) == 1 {
		return imageVersion(serviceMatches[0].Image)
	}

	applicationID := applicationAttributes["inkronik.application_id"]
	applicationMatches := slices.DeleteFunc(slices.Clone(containers), func(container corev1.Container) bool {
		return applicationID == "" || containerEnvValue(container, "INKRONIK_APPLICATION_ID") != applicationID
	})
	if len(applicationMatches) == 1 {
		return imageVersion(applicationMatches[0].Image)
	}

	return ""
}

// Returns an image tag, or a shortened digest for digest-pinned images. Registry ports
// (`registry:5000/repo:tag`) mean the tag has to be read after the final path segment.
func imageVersion(image string) string {
	if digestIndex := strings.LastIndex(image, "@"); digestIndex != -1 {
		return shortDigest(image[digestIndex+1:])
	}

	lastSegment := image[strings.LastIndex(image, "/")+1:]
	tagIndex := strings.LastIndex(lastSegment, ":")

	if tagIndex == -1 {
		return ""
	}

	return lastSegment[tagIndex+1:]
}

// `sha256:abcdef…` → `abcdef012345`. A full digest is unreadable in a version column and adds nothing: the
// prefix is already unique in practice and is what every registry UI displays.
func shortDigest(digest string) string {
	hex := digest[strings.LastIndex(digest, ":")+1:]

	if len(hex) > 12 {
		return hex[:12]
	}

	return hex
}

func (c *Collector) horizontalPodAutoscalerSignals(timestamp time.Time, hpa autoscalingv2.HorizontalPodAutoscaler, applicationAttributes map[string]string) []model.TelemetrySignal {
	attributes := c.hpaAttributes(hpaAttributesInput{hpa: hpa, applicationAttributes: applicationAttributes})
	signals := []model.TelemetrySignal{
		c.metricSignal(timestamp, "k8s.hpa.replicas.min", "replicas", float64(orZero(hpa.Spec.MinReplicas)), attributes),
		c.metricSignal(timestamp, "k8s.hpa.replicas.max", "replicas", float64(hpa.Spec.MaxReplicas), attributes),
		c.metricSignal(timestamp, "k8s.hpa.replicas.current", "replicas", float64(hpa.Status.CurrentReplicas), attributes),
		c.metricSignal(timestamp, "k8s.hpa.replicas.desired", "replicas", float64(hpa.Status.DesiredReplicas), attributes),
	}

	for _, metric := range hpa.Spec.Metrics {
		signals = append(signals, c.horizontalPodAutoscalerTargetSignals(timestamp, metric, attributes)...)
	}

	return signals
}

func (c *Collector) horizontalPodAutoscalerTargetSignals(
	timestamp time.Time,
	metric autoscalingv2.MetricSpec,
	attributes map[string]string,
) []model.TelemetrySignal {
	if metric.Type != autoscalingv2.ResourceMetricSourceType || metric.Resource == nil || metric.Resource.Target.AverageUtilization == nil {
		return nil
	}

	metricNameByResource := map[corev1.ResourceName]string{
		corev1.ResourceCPU:    "k8s.hpa.target.cpu_utilization",
		corev1.ResourceMemory: "k8s.hpa.target.memory_utilization",
	}
	metricName, ok := metricNameByResource[metric.Resource.Name]
	if !ok {
		return nil
	}

	return []model.TelemetrySignal{
		c.metricSignal(timestamp, metricName, "percent", float64(*metric.Resource.Target.AverageUtilization), attributes),
	}
}

func (c *Collector) replicaSetStateSignals(timestamp time.Time, replicaSet appsv1.ReplicaSet) []model.TelemetrySignal {
	attributes := c.workloadAttributes(workloadAttributesInput{
		kind:                  "replicaset",
		namespace:             replicaSet.Namespace,
		name:                  replicaSet.Name,
		labels:                replicaSet.Labels,
		applicationAttributes: applicationAttributesFromPodSpec(replicaSet.Labels, replicaSet.Annotations, replicaSet.Spec.Template.Spec),
	})

	return []model.TelemetrySignal{
		c.metricSignal(timestamp, "k8s.replicaset.replicas.desired", "replicas", float64(orZero(replicaSet.Spec.Replicas)), attributes),
		c.metricSignal(timestamp, "k8s.replicaset.replicas.ready", "replicas", float64(replicaSet.Status.ReadyReplicas), attributes),
		c.metricSignal(timestamp, "k8s.replicaset.replicas.available", "replicas", float64(replicaSet.Status.AvailableReplicas), attributes),
		c.metricSignal(timestamp, "k8s.replicaset.replicas.observed", "replicas", float64(replicaSet.Status.Replicas), attributes),
	}
}

func (c *Collector) k8sEventSignal(event corev1.Event, timestamp time.Time, podsByKey map[string]corev1.Pod) model.TelemetrySignal {
	namespace := defaultString(event.Namespace, "_cluster")
	resourceName := defaultString(event.InvolvedObject.Name, event.Name)
	eventID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(strings.Join([]string{
		c.cluster.Name,
		namespace,
		event.InvolvedObject.Kind,
		resourceName,
		event.Reason,
		timestamp.Format(time.RFC3339Nano),
	}, "/")))
	eventAttributes := mergeAttributes(c.baseAttributes(map[string]string{
		"k8s.source_component": event.Source.Component,
	}), applicationAttributesFromEvent(event, podsByKey))

	return model.TelemetrySignal{
		SignalType:  "k8s_event",
		Environment: c.cluster.Environment,
		Timestamp:   timestamp,
		Source:      agentSource,
		Attributes:  c.baseAttributes(nil),
		Payload: model.K8sEventPayload{
			EventID:      eventID.String(),
			ClusterName:  c.cluster.Name,
			Namespace:    namespace,
			ResourceKind: defaultString(event.InvolvedObject.Kind, "Unknown"),
			ResourceName: resourceName,
			Reason:       defaultString(event.Reason, "Unknown"),
			EventType:    defaultString(event.Type, "Normal"),
			Message:      event.Message,
			Count:        event.Count,
			FirstSeen:    getFirstEventTimestamp(event, timestamp),
			LastSeen:     timestamp,
			Attributes:   eventAttributes,
		},
	}
}

func applicationAttributesFromEvent(event corev1.Event, podsByKey map[string]corev1.Pod) map[string]string {
	if !strings.EqualFold(event.InvolvedObject.Kind, "Pod") {
		return map[string]string{}
	}

	pod, ok := podsByKey[namespacedName(event.Namespace, event.InvolvedObject.Name)]
	if !ok {
		return map[string]string{}
	}

	return applicationAttributesFromPod(pod)
}

func (c *Collector) listPodMetrics(ctx context.Context) ([]metricsv1beta1.PodMetrics, error) {
	if len(c.namespaces) == 0 {
		result, err := c.client.Metrics.MetricsV1beta1().PodMetricses(corev1.NamespaceAll).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		return result.Items, nil
	}

	pods := make([]metricsv1beta1.PodMetrics, 0)
	for _, namespace := range c.namespaces {
		result, err := c.client.Metrics.MetricsV1beta1().PodMetricses(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		pods = append(pods, result.Items...)
	}

	return pods, nil
}

func (c *Collector) listEvents(ctx context.Context) ([]corev1.Event, error) {
	if len(c.namespaces) == 0 {
		result, err := c.client.Kube.CoreV1().Events(corev1.NamespaceAll).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		return result.Items, nil
	}

	events := make([]corev1.Event, 0)
	for _, namespace := range c.namespaces {
		result, err := c.client.Kube.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		events = append(events, result.Items...)
	}

	return events, nil
}

func (c *Collector) listPods(ctx context.Context) ([]corev1.Pod, error) {
	if len(c.namespaces) == 0 {
		result, err := c.client.Kube.CoreV1().Pods(corev1.NamespaceAll).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		return result.Items, nil
	}

	pods := make([]corev1.Pod, 0)
	for _, namespace := range c.namespaces {
		result, err := c.client.Kube.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		pods = append(pods, result.Items...)
	}

	return pods, nil
}

func (c *Collector) listDeployments(ctx context.Context) ([]appsv1.Deployment, error) {
	if len(c.namespaces) == 0 {
		result, err := c.client.Kube.AppsV1().Deployments(corev1.NamespaceAll).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		return result.Items, nil
	}

	deployments := make([]appsv1.Deployment, 0)
	for _, namespace := range c.namespaces {
		result, err := c.client.Kube.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		deployments = append(deployments, result.Items...)
	}

	return deployments, nil
}

func (c *Collector) listHorizontalPodAutoscalers(ctx context.Context) ([]autoscalingv2.HorizontalPodAutoscaler, error) {
	if len(c.namespaces) == 0 {
		result, err := c.client.Kube.AutoscalingV2().HorizontalPodAutoscalers(corev1.NamespaceAll).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		return result.Items, nil
	}

	horizontalPodAutoscalers := make([]autoscalingv2.HorizontalPodAutoscaler, 0)
	for _, namespace := range c.namespaces {
		result, err := c.client.Kube.AutoscalingV2().HorizontalPodAutoscalers(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		horizontalPodAutoscalers = append(horizontalPodAutoscalers, result.Items...)
	}

	return horizontalPodAutoscalers, nil
}

func (c *Collector) listReplicaSets(ctx context.Context) ([]appsv1.ReplicaSet, error) {
	if len(c.namespaces) == 0 {
		result, err := c.client.Kube.AppsV1().ReplicaSets(corev1.NamespaceAll).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		return result.Items, nil
	}

	replicaSets := make([]appsv1.ReplicaSet, 0)
	for _, namespace := range c.namespaces {
		result, err := c.client.Kube.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		replicaSets = append(replicaSets, result.Items...)
	}

	return replicaSets, nil
}

func (c *Collector) isAllowedEventType(eventType string) bool {
	if len(c.allowedEventTypes) == 0 {
		return true
	}

	_, ok := c.allowedEventTypes[eventType]
	return ok
}

func (c *Collector) baseAttributes(extra map[string]string) map[string]string {
	attributes := map[string]string{
		"k8s.cluster":      c.cluster.Name,
		"k8s.provider":     c.cluster.Provider,
		"agent.name":       agentSource,
		"agent.version":    c.cluster.AgentVersion,
		"telemetry.source": "kubernetes",
	}

	for key, value := range extra {
		if value != "" {
			attributes[key] = value
		}
	}

	return attributes
}

func (c *Collector) hpaAttributes(input hpaAttributesInput) map[string]string {
	hpa := input.hpa
	metadataAttributes := applicationAttributesFromMetadata(hpa.Labels, hpa.Annotations)

	return mergeAttributes(mergeAttributes(c.baseAttributes(map[string]string{
		"k8s.scope":              "hpa",
		"k8s.namespace":          hpa.Namespace,
		"k8s.hpa":                hpa.Name,
		"k8s.hpa_target_kind":    hpa.Spec.ScaleTargetRef.Kind,
		"k8s.hpa_target_name":    hpa.Spec.ScaleTargetRef.Name,
		"k8s.hpa_target_api":     hpa.Spec.ScaleTargetRef.APIVersion,
		"k8s.app":                hpa.Labels["app"],
		"k8s.app_name":           hpa.Labels["app.kubernetes.io/name"],
		"k8s.version":            hpa.Labels["app.kubernetes.io/version"],
		"k8s.target_workload_id": strings.ToLower(hpa.Spec.ScaleTargetRef.Kind) + "/" + hpa.Namespace + "/" + hpa.Spec.ScaleTargetRef.Name,
	}), input.applicationAttributes), metadataAttributes)
}

func (c *Collector) workloadAttributes(input workloadAttributesInput) map[string]string {
	return mergeAttributes(c.baseAttributes(map[string]string{
		"k8s.scope":       "workload",
		"k8s.namespace":   input.namespace,
		"k8s.workload":    input.name,
		"k8s.workload_id": workloadID(input.kind, input.namespace, input.name),
		"k8s.kind":        input.kind,
		"k8s.app":         input.labels["app"],
		"k8s.app_name":    input.labels["app.kubernetes.io/name"],
		"k8s.version":     input.labels["app.kubernetes.io/version"],
	}), input.applicationAttributes)
}

func workloadApplicationAttributesByID(deployments []appsv1.Deployment, replicaSets []appsv1.ReplicaSet) map[string]map[string]string {
	result := make(map[string]map[string]string, len(deployments)+len(replicaSets))

	for _, deployment := range deployments {
		attributes := applicationAttributesFromDeployment(deployment)
		if len(attributes) > 0 {
			result[workloadID("deployment", deployment.Namespace, deployment.Name)] = attributes
		}
	}

	for _, replicaSet := range replicaSets {
		attributes := applicationAttributesFromPodSpec(replicaSet.Labels, replicaSet.Annotations, replicaSet.Spec.Template.Spec)
		if len(attributes) > 0 {
			result[workloadID("replicaset", replicaSet.Namespace, replicaSet.Name)] = attributes
		}
	}

	return result
}

func workloadID(kind string, namespace string, name string) string {
	return kind + "/" + namespace + "/" + name
}

func applicationAttributesFromPod(pod corev1.Pod) map[string]string {
	return applicationAttributesFromPodSpec(pod.Labels, pod.Annotations, pod.Spec)
}

func applicationAttributesFromDeployment(deployment appsv1.Deployment) map[string]string {
	workloadAttributes := applicationAttributesFromMetadata(deployment.Labels, deployment.Annotations)
	templateAttributes := applicationAttributesFromPodSpec(
		deployment.Spec.Template.Labels,
		deployment.Spec.Template.Annotations,
		deployment.Spec.Template.Spec,
	)

	return mergeAttributes(workloadAttributes, templateAttributes)
}

func applicationAttributesFromPodSpec(labels map[string]string, annotations map[string]string, podSpec corev1.PodSpec) map[string]string {
	metadataAttributes := applicationAttributesFromMetadata(labels, annotations)
	envAttributes := applicationAttributesFromEnv(podSpec)

	return mergeAttributes(metadataAttributes, envAttributes)
}

func applicationAttributesFromMetadata(labels map[string]string, annotations map[string]string) map[string]string {
	return mapFromNonEmptyValues(map[string]string{
		"inkronik.application_id": firstNonEmpty([]string{
			annotations["inkronik.com/application-id"],
			annotations["inkronik.io/application-id"],
			labels["inkronik.com/application-id"],
			labels["inkronik.io/application-id"],
		}),
		"inkronik.service_name": firstNonEmpty([]string{
			annotations["inkronik.com/service-name"],
			annotations["inkronik.io/service-name"],
			labels["inkronik.com/service-name"],
			labels["inkronik.io/service-name"],
			labels["app.kubernetes.io/name"],
			labels["app"],
		}),
	})
}

func applicationAttributesFromEnv(podSpec corev1.PodSpec) map[string]string {
	containers := slices.Concat(podSpec.InitContainers, podSpec.Containers)
	ingestKey := firstEnvValue(containers, "INKRONIK_INGEST_API_KEY")

	return mapFromNonEmptyValues(map[string]string{
		"inkronik.application_id":      firstEnvValue(containers, "INKRONIK_APPLICATION_ID"),
		"inkronik.service_name":        firstNonEmpty([]string{firstEnvValue(containers, "INKRONIK_SERVICE_NAME"), firstEnvValue(containers, "OTEL_SERVICE_NAME")}),
		"inkronik.ingest_key_prefix":   extractIngestAPIKeyPrefix(ingestKey),
		"inkronik.ingest_key_detected": boolString(ingestKey != ""),
	})
}

func firstEnvValue(containers []corev1.Container, name string) string {
	for _, container := range containers {
		if value := containerEnvValue(container, name); value != "" {
			return value
		}
	}

	return ""
}

func containerEnvValue(container corev1.Container, name string) string {
	for _, env := range container.Env {
		if env.Name == name {
			return strings.TrimSpace(env.Value)
		}
	}

	return ""
}

func extractIngestAPIKeyPrefix(value string) string {
	keyBody, ok := strings.CutPrefix(strings.TrimSpace(value), ingestAPIKeyPrefix)
	if !ok || len(keyBody) <= ingestAPIKeyVisiblePrefixLength {
		return ""
	}

	if keyBody[ingestAPIKeyVisiblePrefixLength] != '_' {
		return ""
	}

	return keyBody[:ingestAPIKeyVisiblePrefixLength]
}

func mergeAttributes(left map[string]string, right map[string]string) map[string]string {
	result := make(map[string]string, len(left)+len(right))

	for key, value := range left {
		if value != "" {
			result[key] = value
		}
	}

	for key, value := range right {
		if value != "" {
			result[key] = value
		}
	}

	return result
}

func mapFromNonEmptyValues(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))

	for key, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			result[key] = trimmedValue
		}
	}

	return result
}

func firstNonEmpty(values []string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}

	return ""
}

func boolString(value bool) string {
	if value {
		return "true"
	}

	return ""
}

func podsByNamespacedName(pods []corev1.Pod) map[string]corev1.Pod {
	result := make(map[string]corev1.Pod, len(pods))

	for _, pod := range pods {
		result[namespacedName(pod.Namespace, pod.Name)] = pod
	}

	return result
}

func namespacedName(namespace string, name string) string {
	return namespace + "/" + name
}

func hasNetworkStats(network k8s.NetworkStats) bool {
	return network.Name != "" || network.RxBytes > 0 || network.TxBytes > 0 || network.RxErrors > 0 || network.TxErrors > 0 || len(network.Interfaces) > 0
}

type networkCountersResult struct {
	rxBytes  uint64
	txBytes  uint64
	rxErrors uint64
	txErrors uint64
}

func networkCounters(network k8s.NetworkStats, scope networkMetricScope) networkCountersResult {
	if network.RxBytes > 0 || network.TxBytes > 0 || network.RxErrors > 0 || network.TxErrors > 0 {
		return networkCountersResult{
			rxBytes:  network.RxBytes,
			txBytes:  network.TxBytes,
			rxErrors: network.RxErrors,
			txErrors: network.TxErrors,
		}
	}

	var rxBytes uint64
	var txBytes uint64
	var rxErrors uint64
	var txErrors uint64

	for _, networkInterface := range network.Interfaces {
		if !shouldIncludeNetworkInterface(networkInterface, scope) {
			continue
		}

		rxBytes += networkInterface.RxBytes
		txBytes += networkInterface.TxBytes
		rxErrors += networkInterface.RxErrors
		txErrors += networkInterface.TxErrors
	}

	return networkCountersResult{
		rxBytes:  rxBytes,
		txBytes:  txBytes,
		rxErrors: rxErrors,
		txErrors: txErrors,
	}
}

func shouldIncludeNetworkInterface(networkInterface k8s.NetworkInterfaceStats, scope networkMetricScope) bool {
	if scope == podNetworkMetricScope {
		return true
	}

	return isNodeNetworkInterface(networkInterface.Name)
}

func isNodeNetworkInterface(name string) bool {
	normalizedName := strings.ToLower(name)
	if normalizedName == "lo" {
		return false
	}

	excludedPrefixes := []string{
		"br-",
		"cali",
		"cilium",
		"cni",
		"docker",
		"flannel",
		"kube-ipvs",
		"nodelocaldns",
		"tunl",
		"veth",
		"virbr",
		"vxlan",
	}
	for _, prefix := range excludedPrefixes {
		if strings.HasPrefix(normalizedName, prefix) {
			return false
		}
	}

	includedPrefixes := []string{"bond", "eno", "enp", "ens", "eth", "team", "wlan"}
	for _, prefix := range includedPrefixes {
		if strings.HasPrefix(normalizedName, prefix) {
			return true
		}
	}

	return false
}

func sumPodMetricUsage(podMetric metricsv1beta1.PodMetrics) (int64, int64) {
	var cpuUsage int64
	var memoryUsage int64

	for _, container := range podMetric.Containers {
		cpuUsage += container.Usage.Cpu().MilliValue()
		memoryUsage += container.Usage.Memory().Value()
	}

	return cpuUsage, memoryUsage
}

func getEventTimestamp(event corev1.Event) time.Time {
	if !event.LastTimestamp.IsZero() {
		return event.LastTimestamp.Time.UTC()
	}

	if !event.EventTime.IsZero() {
		return event.EventTime.Time.UTC()
	}

	return event.CreationTimestamp.Time.UTC()
}

func getFirstEventTimestamp(event corev1.Event, fallback time.Time) time.Time {
	if !event.FirstTimestamp.IsZero() {
		return event.FirstTimestamp.Time.UTC()
	}

	return fallback
}

func defaultString(value string, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}

func boolValue(value bool) float64 {
	if value {
		return 1
	}

	return 0
}

func conditionStatusValue(status corev1.ConditionStatus) float64 {
	if status == corev1.ConditionTrue {
		return 1
	}

	if status == corev1.ConditionFalse {
		return 0
	}

	return -1
}

func orZero(value *int32) int32 {
	if value == nil {
		return 0
	}

	return *value
}
