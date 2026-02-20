# KCP on Kind with OpenTelemetry Observability Stack

Local development setup running [KCP](https://kcp.io) on a Kind cluster with a full observability stack: Prometheus, Grafana, OpenTelemetry Collector, and a custom KCP resource exporter.

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

**Metrics flow:**
1. **KCP native metrics** (API request rates, latencies, errors): Scraped by Prometheus via ServiceMonitor
2. **KCP resource metrics** (workspace/APIExport/APIBinding counts): Custom Go exporter querying KCP API
3. **OTel Collector**: Central OTLP pipeline forwarding to Prometheus via remote write

## Prerequisites

- Docker Desktop with **12GB+ RAM** allocated (14GB recommended)
- [kind](https://kind.sigs.k8s.io/) v0.20+
- [kubectl](https://kubernetes.io/docs/tasks/tools/) v1.28+
- [helm](https://helm.sh/) v3.12+
- [kcp kubectl plugin](https://docs.kcp.io/kcp/main/setup/kubectl-plugin/) (for demo.sh)

## Quick Start

```bash
# Deploy everything
make setup

# Create sample KCP resources
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

```bash
# Use KCP
export KUBECONFIG=./kcp-admin.kubeconfig
kubectl get workspaces
kubectl ws tree
```

## Grafana Dashboards

- **KCP Server Health**: API request rate, latency percentiles, error rates, inflight requests
- **KCP etcd Health**: Leader changes, WAL fsync, DB size, gRPC rates
- **KCP Resources**: Workspace/APIExport/APIBinding/Schema counts by phase

## Components

| Component | Namespace | Purpose |
|-----------|-----------|---------|
| KCP (server + front-proxy) | kcp | Multi-tenant Kubernetes API server |
| etcd | kcp | KCP backing store |
| kube-prometheus-stack | observability | Prometheus + Grafana + Prometheus Operator |
| OTel Collector | observability | OTLP telemetry pipeline |
| KCP Exporter | kcp | Custom metrics for KCP resources |
| cert-manager | cert-manager | TLS certificate management |

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
| etcd | 1Gi | 2Gi |
| Front-proxy | 128Mi | 256Mi |
| Prometheus + Grafana | ~512Mi | ~1Gi |
| OTel Collector | 128Mi | 256Mi |
| KCP Exporter | 64Mi | 128Mi |
| **Total** | **~2.5Gi** | **~4.5Gi** |

System overhead and Kind node bring the total to ~8-10GB.

## Troubleshooting

**Pods stuck in Pending**: Check node resources with `kubectl describe node`.

**KCP not accessible**: Verify front-proxy NodePort: `kubectl get svc -n kcp`.

**Prometheus not scraping**: Check targets at http://localhost:9090/targets.

**Grafana dashboards empty**: Verify dashboard ConfigMaps have `grafana_dashboard=1` label.

**Exporter errors**: Check logs with `make logs-exporter`. Ensure kubeconfig secret exists.

## Cleanup

```bash
make teardown
```

This deletes the Kind cluster and removes the generated kubeconfig file.
