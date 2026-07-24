# Inkronik Kubernetes Agent

The Inkronik Kubernetes Agent collects operational telemetry from a Kubernetes
cluster and sends it to the Inkronik Collector. It runs as a read-only workload
inside the cluster and supports `linux/amd64` and `linux/arm64`.

## Install

Prerequisites:

- a Kubernetes cluster with `kubectl` and Helm 3;
- an Inkronik cluster-agent ingest key;
- Metrics Server when node and pod resource metrics are required.

Set the cluster name and ingest key locally:

```sh
export CLUSTER_NAME=my-production-cluster
export INKRONIK_INGEST_API_KEY=ik_live_prefix_secret
```

Create the namespace and Secret. These commands are safe to run again when the
key changes:

```sh
kubectl create namespace inkronik \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl -n inkronik create secret generic inkronik-k8s-agent \
  --from-literal=INKRONIK_INGEST_API_KEY="$INKRONIK_INGEST_API_KEY" \
  --dry-run=client -o yaml | kubectl apply -f -
```

Install the version-pinned OCI chart:

```sh
helm upgrade --install inkronik-kubernetes-agent \
  oci://ghcr.io/inkronik/charts/inkronik-kubernetes-agent \
  --version 1.0.0 \
  --namespace inkronik \
  --create-namespace \
  --set-string clusterName="$CLUSTER_NAME" \
  --wait
```

Check the agent logs:

```sh
kubectl -n inkronik logs deployment/inkronik-k8s-agent --follow
```

The chart uses the version-pinned image
`ghcr.io/inkronik/kubernetes-agent:1.0.0` and the hosted Collector at
`https://collector.inkronik.codemask.dev`. See the
[chart documentation](charts/inkronik-kubernetes-agent/README.md) for all
values, upgrades, rollback, externally managed RBAC, and image digest pinning.

### Raw manifest alternative

The raw manifest remains available for environments without Helm:

```sh
kubectl apply -f \
  https://raw.githubusercontent.com/inkronik/kubernetes-agent/v1.0.0/deploy/kubernetes.yaml

kubectl -n inkronik set env deployment/inkronik-k8s-agent \
  INKRONIK_CLUSTER_NAME="$CLUSTER_NAME"

kubectl -n inkronik rollout status deployment/inkronik-k8s-agent
```

Review the raw manifest before applying it when your environment requires a
proxy, private registry mirror, or custom Collector URL.

## Collected telemetry

- Kubernetes warning events;
- node CPU, memory, and condition metrics;
- pod CPU, memory, phase, container readiness, and restart metrics;
- Deployment replica metrics;
- ReplicaSet replica metrics;
- HorizontalPodAutoscaler metrics.

Kubelet filesystem and network metrics are enabled by default. Kubernetes
authorizes the API Server proxy request with the broad `nodes/proxy` permission.
Set `kubeletStats.enabled=false` in the Helm chart, or
`INKRONIK_KUBELET_STATS_ENABLED=false` when running the binary directly, to
disable those metrics and remove the permission from chart-managed RBAC.

The agent reads only the Kubernetes resources listed in
[`deploy/kubernetes.yaml`](deploy/kubernetes.yaml). It does not require write
permissions to cluster resources.

## Configuration

Required environment variables:

- `INKRONIK_INGEST_API_KEY` — cluster-agent ingest key;
- `INKRONIK_CLUSTER_NAME` — stable, human-readable cluster name.

Optional environment variables:

- `INKRONIK_COLLECTOR_URL` — HTTPS Collector base URL without a trailing slash;
  defaults to `https://collector.inkronik.codemask.dev`;
- `INKRONIK_APPLICATION_ID` — legacy batch-owner application UUID; leave unset
  for cluster-agent keys;
- `INKRONIK_ENVIRONMENT` — defaults to `prod`;
- `INKRONIK_CLUSTER_PROVIDER` — defaults to `kubernetes`;
- `INKRONIK_K8S_AGENT_VERSION` — overrides the build-time version;
- `INKRONIK_HEALTH_ADDR` — defaults to `:8080`;
- `INKRONIK_KUBECONFIG` — kubeconfig path for local development;
- `INKRONIK_NAMESPACES` — comma-separated namespace allowlist; empty means all;
- `INKRONIK_EVENT_TYPES` — comma-separated event types, defaults to `Warning`;
- `INKRONIK_METRICS_INTERVAL_SECONDS` — defaults to `30`;
- `INKRONIK_EVENT_SYNC_INTERVAL_SECONDS` — defaults to `30`;
- `INKRONIK_INITIAL_EVENT_LOOKBACK_SECONDS` — defaults to `300`;
- `INKRONIK_REQUEST_TIMEOUT_SECONDS` — defaults to `10`.
- `INKRONIK_KUBELET_STATS_ENABLED` — defaults to `true`; set to `false` to
  disable filesystem and network stats. Chart-managed RBAC then omits the broad
  `nodes/proxy` permission.

See [`.env.example`](.env.example) for a local-development template.

## Application correlation

Add `INKRONIK_APPLICATION_ID` to monitored application pods to scope their pod,
container, Deployment, ReplicaSet, and HPA metrics to an Inkronik application.
The agent also understands `INKRONIK_SERVICE_NAME`, `OTEL_SERVICE_NAME`, and the
`inkronik.com/application-id` or `inkronik.io/application-id` metadata keys.

The full ingest key is never emitted. When a key is present as a literal pod
environment variable, only its visible prefix can be attached as telemetry
metadata. Kubernetes does not expose Secret values referenced by `secretKeyRef`
through the Pod specification.

## Health endpoints

- `GET /healthz` — process liveness;
- `GET /readyz` — readiness after configuration and startup.

## Local development

Copy `.env.example`, export the values, and run:

```sh
go run ./cmd/inkronik-k8s-agent
```

Run the checks:

```sh
test -z "$(gofmt -l .)"
go vet ./...
go test -race ./...
helm lint --strict charts/inkronik-kubernetes-agent \
  --set-string clusterName=local-validation
helm template inkronik-kubernetes-agent charts/inkronik-kubernetes-agent \
  --namespace inkronik \
  --set-string clusterName=local-validation
```

Build a local image:

```sh
docker build --build-arg VERSION="$(tr -d '\n' < VERSION)" \
  -t inkronik-kubernetes-agent:local .
```

Print the version embedded in a binary or image:

```sh
./inkronik-k8s-agent --version
docker run --rm ghcr.io/inkronik/kubernetes-agent:1.0.0 --version
```

## Upgrading

Upgrade with an explicitly selected OCI chart version. Helm keeps revision
history for rollback. For raw-manifest installations, change both the raw
manifest tag and container image tag to the same published version. Full
version tags are immutable; `latest` is provided only as a convenience and
should not be used for production installations.

## License

MIT License. See [`LICENSE`](LICENSE).
