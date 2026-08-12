# Inkronik Kubernetes Agent Helm chart

This chart installs one Inkronik Kubernetes Agent with cluster-wide, read-only
access to the Kubernetes resources needed for telemetry collection.

## Prerequisites

- Kubernetes cluster access;
- Helm 3;
- an Inkronik cluster-agent ingest key;
- Metrics Server for node and pod resource metrics.

## Install

Create the namespace and the Secret outside Helm:

```sh
export CLUSTER_NAME=my-production-cluster
export INKRONIK_INGEST_API_KEY=ik_live_prefix_secret

kubectl create namespace inkronik \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl -n inkronik create secret generic inkronik-k8s-agent \
  --from-literal=INKRONIK_INGEST_API_KEY="$INKRONIK_INGEST_API_KEY" \
  --dry-run=client -o yaml | kubectl apply -f -
```

Install the versioned OCI chart:

```sh
helm upgrade --install inkronik-kubernetes-agent \
  oci://ghcr.io/inkronik/charts/inkronik-kubernetes-agent \
  --version 1.1.3 \
  --namespace inkronik \
  --create-namespace \
  --set-string clusterName="$CLUSTER_NAME" \
  --wait
```

The ingest key is never accepted as a chart value. The Deployment references
the existing `inkronik-k8s-agent` Secret by default, which keeps the key out of
the chart source and Helm release values.

## Configuration

| Value | Description | Default |
| --- | --- | --- |
| `clusterName` | Stable cluster name; required | `""` |
| `collectorUrl` | Inkronik Collector base URL | `https://collector.inkronik.codemask.dev` |
| `environment` | Telemetry environment | `prod` |
| `clusterProvider` | Cluster provider label | `kubernetes` |
| `applicationId` | Optional legacy batch-owner application UUID | `""` |
| `namespaces` | Namespace allowlist; empty watches all namespaces | `[]` |
| `eventTypes` | Kubernetes event types to collect | `[Warning]` |
| `ingestSecret.name` | Existing Secret name | `inkronik-k8s-agent` |
| `ingestSecret.key` | Existing Secret data key | `INKRONIK_INGEST_API_KEY` |
| `secretsStoreCsi.enabled` | Mount a Secrets Store CSI `SecretProviderClass` to synchronize the ingest Secret | `false` |
| `secretsStoreCsi.secretProviderClassName` | Existing `SecretProviderClass` name; required when CSI integration is enabled | `""` |
| `secretsStoreCsi.mountPath` | Read-only CSI mount path | `/mnt/secrets-store` |
| `image.repository` | Agent image repository | `ghcr.io/inkronik/kubernetes-agent` |
| `image.tag` | Image tag; empty uses chart `appVersion` | `""` |
| `image.digest` | Optional immutable `sha256:` image digest | `""` |
| `image.pullPolicy` | Kubernetes image pull policy | `IfNotPresent` |
| `imagePullSecrets` | Private registry pull Secret references | `[]` |
| `serviceAccount.create` | Create the agent ServiceAccount | `true` |
| `serviceAccount.name` | ServiceAccount override or externally managed name | `""` |
| `rbac.create` | Create ClusterRole and ClusterRoleBinding | `true` |
| `kubeletStats.enabled` | Collect filesystem/network stats through `nodes/proxy` RBAC | `true` |
| `resources` | Agent resource requests and limits | See `values.yaml` |
| `podSecurityContext` | Pod-level security context | Non-root UID/GID `65532` |
| `securityContext` | Container security context | Read-only, no privilege escalation or capabilities |
| `nodeSelector` | Pod node selector | `{}` |
| `tolerations` | Pod tolerations | `[]` |
| `affinity` | Pod affinity rules | `{}` |
| `priorityClassName` | Optional priority class | `""` |
| `tests.enabled` | Render the `helm test` version check | `true` |

All supported values and validation constraints are defined in
[`values.yaml`](values.yaml) and [`values.schema.json`](values.schema.json).

### Namespace allowlist

```sh
helm upgrade inkronik-kubernetes-agent \
  oci://ghcr.io/inkronik/charts/inkronik-kubernetes-agent \
  --version 1.1.3 \
  --namespace inkronik \
  --reuse-values \
  --set 'namespaces={payments,orders}' \
  --wait
```

### Custom Collector

Collector overrides must use HTTPS. When omitted, the chart uses the hosted
Inkronik Collector.

```sh
helm upgrade inkronik-kubernetes-agent \
  oci://ghcr.io/inkronik/charts/inkronik-kubernetes-agent \
  --version 1.1.3 \
  --namespace inkronik \
  --reuse-values \
  --set-string collectorUrl=https://collector.example.internal \
  --wait
```

### Kubelet stats and reduced-permission mode

Filesystem and network stats require access to the kubelet API through the
Kubernetes API Server proxy. This grants the agent ServiceAccount the broad
`nodes/proxy` permission. The feature is enabled by default so a standard
installation collects the complete telemetry set. To trade those metrics for a
smaller RBAC surface, disable it:

```sh
helm upgrade inkronik-kubernetes-agent \
  oci://ghcr.io/inkronik/charts/inkronik-kubernetes-agent \
  --version 1.1.3 \
  --namespace inkronik \
  --reuse-values \
  --set kubeletStats.enabled=false \
  --wait
```

### Externally managed access

When platform administrators provide the ServiceAccount and RBAC separately:

```sh
helm upgrade --install inkronik-kubernetes-agent \
  oci://ghcr.io/inkronik/charts/inkronik-kubernetes-agent \
  --version 1.1.3 \
  --namespace inkronik \
  --set-string clusterName="$CLUSTER_NAME" \
  --set serviceAccount.create=false \
  --set-string serviceAccount.name=platform-agent \
  --set rbac.create=false \
  --wait
```

The externally managed account must have the permissions shown in the chart's
`templates/clusterrole.yaml`. The `nodes/proxy` permission is required only
when `kubeletStats.enabled=true`.

### Secrets Store CSI

When the ingest Secret is synchronized by an existing Secrets Store CSI
`SecretProviderClass`, enable its mount on the agent pod. Mounting the provider
volume triggers synchronization of the Kubernetes Secret referenced by
`ingestSecret`:

```sh
helm upgrade --install inkronik-kubernetes-agent \
  oci://ghcr.io/inkronik/charts/inkronik-kubernetes-agent \
  --version 1.1.3 \
  --namespace inkronik \
  --set-string clusterName="$CLUSTER_NAME" \
  --set secretsStoreCsi.enabled=true \
  --set-string secretsStoreCsi.secretProviderClassName=vault-development \
  --set-string ingestSecret.name=vault-development \
  --wait
```

The chart does not create the `SecretProviderClass`; keep it in the platform's
secret-management configuration. Existing installations are unaffected because
the integration is disabled by default.

## Verify

```sh
kubectl -n inkronik rollout status deployment/inkronik-kubernetes-agent
kubectl -n inkronik logs deployment/inkronik-kubernetes-agent --follow
helm test inkronik-kubernetes-agent --namespace inkronik
```

## Upgrade and rollback

Upgrade by specifying a new immutable chart version:

```sh
helm upgrade inkronik-kubernetes-agent \
  oci://ghcr.io/inkronik/charts/inkronik-kubernetes-agent \
  --version 1.1.0 \
  --namespace inkronik \
  --reuse-values \
  --wait
```

Inspect revisions and roll back when necessary:

```sh
helm history inkronik-kubernetes-agent --namespace inkronik
helm rollback inkronik-kubernetes-agent 1 --namespace inkronik --wait
```

## Uninstall

```sh
helm uninstall inkronik-kubernetes-agent --namespace inkronik
```

The externally created ingest Secret and namespace remain after uninstall.
