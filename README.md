# KCP on Kind with OpenTelemetry Observability Stack

> **This is a proof of concept.** It demonstrates how to observe a KCP installation using standard cloud-native tooling. Not intended for production use.

## Why This Exists

KCP exposes standard Kubernetes API server metrics (`apiserver_request_*`, `etcd_*`) out of the box, but has **no built-in observability for its own resource model** — there is no native way to monitor how many Workspaces exist, which APIBindings are Bound vs failing, or how APIExport adoption is growing over time.

This POC solves that with three key pieces:

### 1. Custom KCP Resource Exporter (`kcp-exporter/`)

The core of this POC. A small Go service that bridges the gap between KCP's resource model and Prometheus:

- Queries the KCP API every 30s via `/clusters/root/apis/...` endpoints
- Exposes Prometheus gauges: `kcp_workspaces_total{phase}`, `kcp_apiexports_total`, `kcp_apibindings_total{phase}`, `kcp_apiresourceschemas_total`, `kcp_logical_clusters_total`
- Authenticates via `system:kcp:admin` client certificate (KCP's standard admin identity, not the chart-default `external-logical-cluster-admin` which lacks list permissions)
- Discovered by Prometheus automatically via ServiceMonitor

This is the component that would need to be productionized — everything else is standard infrastructure wiring.

### 2. Prometheus + Grafana Stack

Standard kube-prometheus-stack deployment with:
- ServiceMonitor auto-discovery across namespaces (scrapes KCP server, etcd, front-proxy, and the custom exporter)
- Three purpose-built Grafana dashboards: **KCP Server Health** (API latencies, error rates), **KCP etcd Health** (WAL fsync, DB size), **KCP Resources** (workspace/export/binding counts by phase)

### 3. OpenTelemetry Collector

An OTLP pipeline that forwards metrics to Prometheus via remote write. Currently the exporter pushes directly to Prometheus via scrape; the OTel Collector is wired up as the extensibility point for future traces and logs ingestion.

## Architecture

```
KCP Pods ──(ServiceMonitor)──> Prometheus Operator ──> Prometheus ──> Grafana
                                                          ^
KCP Exporter ──(ServiceMonitor)──────────────────────────┘
                                                          ^
OTel Collector ──(prometheusremotewrite)──────────────────┘
     ^
  OTLP ingest (future extensibility for traces/logs)
```

## What Would Need to Change for Production

| POC Shortcut | Production Requirement |
|---|---|
| Exporter queries `/clusters/root/` only | Recursive workspace traversal or wildcard endpoint via cache server |
| `insecure-skip-tls-verify` for in-cluster exporter | Proper cert SANs covering the service DNS name |
| cert-manager Certificate for admin auth | Dedicated ServiceAccount with scoped RBAC, or KCP's `KubeConfig` CRD |
| Kind cluster with NodePorts | Real cluster with Ingress/LoadBalancer |
| Single exporter instance | HA deployment with leader election |
| Dashboards as ConfigMaps | Grafana provisioning via Helm values or dashboard-as-code |

## Prerequisites

- Docker Desktop with **12GB+ RAM** allocated (14GB recommended)
- [kind](https://kind.sigs.k8s.io/) v0.20+
- [kubectl](https://kubernetes.io/docs/tasks/tools/) v1.28+
- [helm](https://helm.sh/) v3.12+

> The kcp kubectl plugin (`kubectl ws`) is **not** required. All scripts use explicit `--server` URL switching for workspace context.

## Quick Start

```bash
# Deploy everything (~5 minutes)
make setup

# Create sample KCP resources (3 workspaces, APIExport, APIBindings, Widgets)
make demo

# Check status
make status
```

## Access

| Service    | URL                          | Credentials  |
|------------|------------------------------|--------------|
| Grafana    | http://localhost:3000         | admin/admin  |
| Prometheus | http://localhost:9090         |              |
| KCP        | https://kcp.localhost:8443    | mTLS cert    |

### Using KCP

KCP requires all API requests to be scoped to a workspace via the `/clusters/<path>` URL prefix. The generated `kcp-admin.kubeconfig` authenticates as `system:kcp:admin`.

```bash
export KUBECONFIG=./kcp-admin.kubeconfig
KCP=https://kcp.localhost:8443

# List workspaces in root
kubectl --server=$KCP/clusters/root get workspaces

# List APIExports in a child workspace
kubectl --server=$KCP/clusters/root:org-alpha get apiexports

# List resources in a consumer workspace
kubectl --server=$KCP/clusters/root:org-beta get widgets
```

## Grafana Dashboards

- **KCP Server Health**: API request rate by verb/resource/code, latency p50/p95/p99, error rates, inflight requests
- **KCP etcd Health**: Leader changes, WAL fsync duration, DB size, gRPC rates, active watchers
- **KCP Resources**: Workspace count by phase, APIExport/APIBinding/Schema totals, time series, summary table

## Components

| Component | Namespace | Purpose |
|-----------|-----------|---------|
| KCP server (1 replica) | kcp | Multi-tenant Kubernetes API server |
| KCP front-proxy (1 replica) | kcp | TLS termination, auth, workspace routing |
| etcd (3 replicas) | kcp | KCP backing store |
| kube-prometheus-stack | observability | Prometheus + Grafana + Prometheus Operator |
| OTel Collector | observability | OTLP telemetry pipeline |
| **KCP Exporter** | kcp | **Custom metrics for KCP resources (the novel piece)** |
| cert-manager | cert-manager | TLS certificate management |

## Authentication

Setup generates two self-contained kubeconfig files (embedded cert data, no file-path references):

- **`kcp-admin.kubeconfig`** — for local use, points to `https://kcp.localhost:8443`, cert with `O=system:kcp:admin`
- **`kcp-exporter.kubeconfig`** — for in-cluster use by the exporter, points to the front-proxy ClusterIP service with TLS verification skipped (cert SAN only covers `kcp.localhost`)

## Makefile Targets

```
make setup               # Create cluster and deploy full stack
make teardown             # Delete cluster and clean up
make demo                 # Create sample KCP resources
make status               # Show status of all components
make build-exporter       # Rebuild and redeploy KCP exporter
make logs-kcp             # Tail KCP server logs
make logs-exporter        # Tail KCP exporter logs
make logs-prometheus       # Tail Prometheus logs
make logs-otel            # Tail OTel Collector logs
make port-forward-grafana  # Port-forward Grafana (fallback)
make port-forward-prometheus # Port-forward Prometheus (fallback)
```

## Resource Requirements

| Component | Memory Request | Memory Limit |
|-----------|---------------|--------------|
| KCP server | 512Mi | 1Gi |
| etcd (x3) | 1Gi each | 2Gi each |
| Front-proxy | 128Mi | 256Mi |
| Prometheus + Grafana | ~512Mi | ~1Gi |
| OTel Collector | 128Mi | 256Mi |
| KCP Exporter | 64Mi | 128Mi |
| **Total** | **~4.5Gi** | **~8Gi** |

System overhead and Kind node bring the total to ~10-12GB. Allocate at least **12GB RAM** to Docker Desktop.

## Troubleshooting

**Pods stuck in Pending**: Check node resources with `kubectl describe node`.

**KCP not accessible**: Verify front-proxy NodePort: `kubectl get svc -n kcp`. The service should expose port 8443 on NodePort 30443.

**`kubectl` returns "Forbidden" or "unknown"**: KCP requires workspace-scoped requests. Use `--server=https://kcp.localhost:8443/clusters/root` (or another workspace path).

**Prometheus not scraping**: Check targets at http://localhost:9090/targets. All `kcp`, `kcp-etcd`, `kcp-front-proxy`, and `kcp-exporter` targets should show `UP`.

**Grafana dashboards empty**: Verify dashboard ConfigMaps have `grafana_dashboard=1` label: `kubectl get cm -n observability -l grafana_dashboard=1`.

**Exporter errors**: Check logs with `make logs-exporter`. Common issues:
- TLS errors: the exporter kubeconfig should use `insecure-skip-tls-verify: true` for the in-cluster service
- 403 Forbidden: the client cert must have `O=system:kcp:admin` (not `system:kcp:external-logical-cluster-admin`)
- API paths must include `/clusters/root/` prefix

## Cleanup

```bash
make teardown
```

This deletes the Kind cluster and removes generated kubeconfig files.
