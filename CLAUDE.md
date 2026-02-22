# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

Local dev environment running KCP (kcp.io) on a Kind cluster with Prometheus, Grafana, OpenTelemetry Collector, and a custom Go metrics exporter. Everything deploys via Helm charts and shell scripts.

## Commands

```bash
make setup              # Create Kind cluster + deploy full stack (idempotent, ~5min)
make teardown           # Delete Kind cluster + remove generated kubeconfigs
make demo               # Create sample KCP resources (workspaces, APIExports, widgets)
make status             # Show pod status + Prometheus scrape targets
make build-exporter     # Rebuild Go exporter image, load to Kind, restart deployment
make logs-kcp           # Tail KCP server logs
make logs-exporter      # Tail kcp-exporter logs
```

To work with KCP after setup:
```bash
export KUBECONFIG=./kcp-admin.kubeconfig
KCP=https://kcp.localhost:8443

# All KCP requests MUST include /clusters/<workspace-path> in the server URL
kubectl --server=$KCP/clusters/root get workspaces
kubectl --server=$KCP/clusters/root:org-alpha get apiexports
kubectl --server=$KCP/clusters/root:org-beta get widgets
```

**Important**: bare `kubectl get workspaces` will fail with "Forbidden" — KCP requires workspace-scoped requests via the `/clusters/<path>` URL prefix.

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

## KCP API Specifics

- All API requests must be scoped to a workspace: `/clusters/root/apis/...`, `/clusters/root:child/apis/...`
- The exporter queries `/clusters/root/apis/tenancy.kcp.io/v1alpha1/workspaces`, etc.
- KCP serves both `v1alpha1` and `v1alpha2` for APIExport/APIBinding — the exporter uses `v1alpha1`
- The `system:kcp:admin` group grants broad access; `system:kcp:external-logical-cluster-admin` (chart default) does NOT have list permissions
- Front-proxy cert SAN is `kcp.localhost` only — in-cluster clients must use `insecure-skip-tls-verify` or the hostname

## Helm Chart Value Structure (gotchas)

The KCP chart (kcp/kcp v0.14.0) has specific value paths that differ from what you might guess:
- `externalHostname` and `externalPort` are **top-level**, not nested under `kcp:`
- Front-proxy config is under `kcpFrontProxy:`, not `frontProxy:`
- etcd is `etcd.enabled: true` at root level, not `kcp.etcd.embedded`
- etcd volume size is `etcd.volumeSize`, not `etcd.persistence.size`
- Front-proxy NodePort is `kcpFrontProxy.service.nodePort`
- etcd defaults to **3 replicas** (chart default) even for local dev

The OTel Collector chart (v0.145.0) merges in default receivers (jaeger, zipkin, prometheus). Null them out explicitly in values to avoid port conflicts. Do NOT pin the image tag to old versions — the chart's telemetry config format (`readers` block) requires a matching collector binary version.

## Kind NodePort Mappings

| Container Port | Host Port | Service |
|---------------|-----------|---------|
| 30080 | 3000 | Grafana (admin/admin) |
| 30090 | 9090 | Prometheus |
| 30443 | 8443 | KCP front-proxy (mTLS) |

## KCP Authentication

Setup generates two self-contained kubeconfigs with **embedded cert data** (no file-path references):
- **`kcp-admin.kubeconfig`** — for local use, server `https://kcp.localhost:8443`, cert issued by `kcp-front-proxy-client-issuer` with `O=system:kcp:admin`
- **`kcp-exporter.kubeconfig`** — for in-cluster use, server `https://kcp-front-proxy.kcp.svc.cluster.local:8443` with `insecure-skip-tls-verify: true` (cert SAN only covers `kcp.localhost`)

The chart-generated kubeconfig (`kcp-external-admin-kubeconfig` secret) uses file-path references that only work inside KCP pods — do NOT use it for external clients.

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
6. Build self-contained admin kubeconfigs from cert-manager secrets (`kcp-admin-client-cert` + `kcp-ca`)
7. Docker build kcp-exporter -> `kind load docker-image` -> deploy manifests
8. Grafana dashboards as ConfigMaps with `grafana_dashboard=1` label

All steps are idempotent (skip-if-exists or helm upgrade).

## Resource Budget

Docker Desktop needs 12GB+ RAM. etcd runs 3 replicas at ~1Gi each. Total stack: ~10-12GB with system overhead.

## KCP RBAC & Bind Permissions (lessons learned)

- APIExports must be in `root` workspace for bind permissions to work (Deep SAR evaluates bootstrap RBAC only in root)
- Child workspaces need explicit `cluster-admin` ClusterRoleBinding for `system:kcp:admin` — the Helm chart may not enable the admin battery
- `resourceNames: ["*"]` does NOT work as a wildcard for bind RBAC
- The `apis.kcp.io:binding:` prefix is mandatory for maximal permission policy subjects
- demo.sh creates explicit RBAC in each child workspace before creating APIBindings

## Exporter Notes

- Exporter silently skips child workspace subtrees on 403 (logs warning with response body, increments scrape_errors counter)
- `kcp_shard_condition` requires synthetic Ready condition for KCP v0.30.0 (no real conditions populated)
- Collection intervals: root metrics=30s, workspace tree=5min, per-workspace=60s
- Error responses now include response body for actionable debug info

## Dashboard Notes

- Use `or vector(0)` pattern for stat panels that may have no data initially
- Use `clamp_min(denominator, 1)` to prevent division-by-zero in ratio panels
- Shard Status panel falls back to `kcp_shard_info{ready="true"}` when `kcp_shard_condition` is empty
- Table panels use `noValue` in fieldConfig to show helpful messages when empty
