# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

Local dev environment running KCP (kcp.io) on a Kind cluster with Prometheus, Grafana, OpenTelemetry Collector, and a custom Go metrics exporter. Everything deploys via Helm charts and shell scripts.

## Commands

```bash
make setup              # Create Kind cluster + deploy full stack (idempotent, ~5min)
make teardown           # Delete Kind cluster + remove generated kubeconfig
make demo               # Create sample KCP resources (workspaces, APIExports, widgets)
make status             # Show pod status + Prometheus scrape targets
make build-exporter     # Rebuild Go exporter image, load to Kind, restart deployment
make logs-kcp           # Tail KCP server logs
make logs-exporter      # Tail kcp-exporter logs
```

To work with KCP after setup:
```bash
export KUBECONFIG=./kcp-admin.kubeconfig
kubectl get workspaces
kubectl ws tree
```

### Go Exporter Development

```bash
cd kcp-exporter
go build .                    # Verify compilation
go vet ./...                  # Lint
go test -race ./...           # Tests (none yet)
make build-exporter           # Full rebuild + deploy cycle (from project root)
```

## Architecture

```
KCP Pods ──(ServiceMonitor)──> Prometheus ──> Grafana (localhost:3000)
KCP Exporter ──(ServiceMonitor)──> Prometheus (localhost:9090)
OTel Collector ──(prometheusremotewrite)──> Prometheus
     ↑
  OTLP (4317/4318) — future traces/logs ingestion
```

**Two metric sources feed Prometheus:**
1. **KCP native metrics** — standard `apiserver_request_*` / `etcd_*` metrics scraped via ServiceMonitor from kcp, front-proxy, and etcd pods (namespace: `kcp`)
2. **KCP resource metrics** — custom `kcp_workspaces_total`, `kcp_apiexports_total`, `kcp_apibindings_total`, `kcp_apiresourceschemas_total`, `kcp_logical_clusters_total` from the Go exporter querying KCP API every 30s

The OTel Collector sits as a central OTLP pipeline forwarding to Prometheus via remote write. Currently only metrics flow through it; traces/logs pipelines exist but export to debug only.

## Helm Chart Value Structure (gotchas)

The KCP chart (kcp/kcp v0.14.0) has specific value paths that differ from what you might guess:
- `externalHostname` and `externalPort` are **top-level**, not nested under `kcp:`
- Front-proxy config is under `kcpFrontProxy:`, not `frontProxy:`
- etcd is `etcd.enabled: true` at root level, not `kcp.etcd.embedded`
- etcd volume size is `etcd.volumeSize`, not `etcd.persistence.size`
- Front-proxy NodePort is `kcpFrontProxy.service.nodePort`

The OTel Collector chart (v0.145.0) merges in default receivers (jaeger, zipkin, prometheus). Null them out explicitly in values to avoid port conflicts. The image tag must not be pinned to old versions — the chart's telemetry config format (`readers` block) requires a matching collector binary version.

## Kind NodePort Mappings

| Container Port | Host Port | Service |
|---------------|-----------|---------|
| 30080 | 3000 | Grafana (admin/admin) |
| 30090 | 9090 | Prometheus |
| 30443 | 8443 | KCP front-proxy (mTLS) |

## KCP Authentication

cert-manager issues a client certificate with org `system:kcp:admin`. The `kcp/generate-kubeconfig.sh` script extracts certs from the K8s secret `kcp-admin-client-cert` and builds a kubeconfig pointing to `https://kcp.localhost:8443`. The exporter mounts this kubeconfig from secret `kcp-exporter-kubeconfig`.

## Namespaces

- `kcp` — KCP server, etcd, front-proxy, kcp-exporter
- `observability` — Prometheus, Grafana, OTel Collector, kube-state-metrics
- `cert-manager` — cert-manager

## setup.sh Flow (8 steps)

1. Kind cluster from `kind/cluster-config.yaml`
2. cert-manager (Helm, jetstack repo)
3. kube-prometheus-stack (Helm, prometheus-community repo) — installed **before** KCP so ServiceMonitor CRD exists
4. OTel Collector (Helm, open-telemetry repo)
5. KCP (Helm, kcp repo)
6. Admin kubeconfig generation via `kcp/generate-kubeconfig.sh`
7. Docker build kcp-exporter → `kind load docker-image` → deploy manifests
8. Grafana dashboards as ConfigMaps with `grafana_dashboard=1` label

All steps are idempotent (skip-if-exists or helm upgrade).

## Resource Budget

Docker Desktop needs 12GB+ RAM. etcd alone takes ~1-2GB. Total stack: ~8-10GB with system overhead.
