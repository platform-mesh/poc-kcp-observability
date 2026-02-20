#!/usr/bin/env bash
# Main orchestration script for KCP + OTel observability stack on Kind.
# Idempotent - safe to re-run.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLUSTER_NAME="kcp-otel"
KCP_NAMESPACE="kcp"
OBS_NAMESPACE="observability"

info()  { echo "===> $*"; }
error() { echo "ERROR: $*" >&2; exit 1; }

# Pre-flight checks
for cmd in kind kubectl helm docker; do
  command -v "$cmd" >/dev/null 2>&1 || error "$cmd is required but not found in PATH"
done

# -------------------------------------------------------------------
# Step 1: Kind cluster
# -------------------------------------------------------------------
info "Step 1/8: Creating Kind cluster '${CLUSTER_NAME}'..."
if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  info "Kind cluster '${CLUSTER_NAME}' already exists, skipping."
else
  kind create cluster --name "${CLUSTER_NAME}" --config "${SCRIPT_DIR}/kind/cluster-config.yaml"
fi
kubectl cluster-info --context "kind-${CLUSTER_NAME}"

# -------------------------------------------------------------------
# Step 2: cert-manager
# -------------------------------------------------------------------
info "Step 2/8: Installing cert-manager..."
helm repo add jetstack https://charts.jetstack.io 2>/dev/null || true
helm repo update jetstack
if helm status cert-manager -n cert-manager >/dev/null 2>&1; then
  info "cert-manager already installed, skipping."
else
  helm install cert-manager jetstack/cert-manager \
    --namespace cert-manager --create-namespace \
    --set crds.enabled=true \
    --wait --timeout 5m
fi
info "Waiting for cert-manager webhook to be ready..."
kubectl wait --for=condition=Available deployment/cert-manager-webhook \
  -n cert-manager --timeout=120s

# -------------------------------------------------------------------
# Step 3: Observability namespace + kube-prometheus-stack
# -------------------------------------------------------------------
info "Step 3/8: Installing kube-prometheus-stack..."
kubectl apply -f "${SCRIPT_DIR}/observability/namespace.yaml"
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts 2>/dev/null || true
helm repo update prometheus-community
if helm status kube-prometheus-stack -n "${OBS_NAMESPACE}" >/dev/null 2>&1; then
  info "kube-prometheus-stack already installed, upgrading..."
  helm upgrade kube-prometheus-stack prometheus-community/kube-prometheus-stack \
    --namespace "${OBS_NAMESPACE}" \
    -f "${SCRIPT_DIR}/observability/prometheus/values.yaml" \
    --wait --timeout 5m
else
  helm install kube-prometheus-stack prometheus-community/kube-prometheus-stack \
    --namespace "${OBS_NAMESPACE}" \
    -f "${SCRIPT_DIR}/observability/prometheus/values.yaml" \
    --wait --timeout 5m
fi

# -------------------------------------------------------------------
# Step 4: OTel Collector
# -------------------------------------------------------------------
info "Step 4/8: Installing OpenTelemetry Collector..."
helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts 2>/dev/null || true
helm repo update open-telemetry
if helm status otel-collector -n "${OBS_NAMESPACE}" >/dev/null 2>&1; then
  info "OTel Collector already installed, upgrading..."
  helm upgrade otel-collector open-telemetry/opentelemetry-collector \
    --namespace "${OBS_NAMESPACE}" \
    -f "${SCRIPT_DIR}/observability/otel-collector/values.yaml" \
    --wait --timeout 3m
else
  helm install otel-collector open-telemetry/opentelemetry-collector \
    --namespace "${OBS_NAMESPACE}" \
    -f "${SCRIPT_DIR}/observability/otel-collector/values.yaml" \
    --wait --timeout 3m
fi

# -------------------------------------------------------------------
# Step 5: KCP via Helm
# -------------------------------------------------------------------
info "Step 5/8: Installing KCP..."
helm repo add kcp https://kcp-dev.github.io/helm-charts 2>/dev/null || true
helm repo update kcp
kubectl create namespace "${KCP_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
if helm status kcp -n "${KCP_NAMESPACE}" >/dev/null 2>&1; then
  info "KCP already installed, upgrading..."
  helm upgrade kcp kcp/kcp \
    --namespace "${KCP_NAMESPACE}" \
    -f "${SCRIPT_DIR}/kcp/values.yaml" \
    --wait --timeout 10m
else
  helm install kcp kcp/kcp \
    --namespace "${KCP_NAMESPACE}" \
    -f "${SCRIPT_DIR}/kcp/values.yaml" \
    --wait --timeout 10m
fi

# Wait for KCP pods to be ready
info "Waiting for KCP pods..."
kubectl wait --for=condition=Ready pods -l app.kubernetes.io/instance=kcp \
  -n "${KCP_NAMESPACE}" --timeout=300s || true

# -------------------------------------------------------------------
# Step 6: Build self-contained KCP admin kubeconfig
# -------------------------------------------------------------------
info "Step 6/8: Building KCP admin kubeconfig..."

# The chart-generated kubeconfig uses file-path references (only works inside KCP pods).
# We build a self-contained kubeconfig with embedded cert data for external use.
CLIENT_CERT=$(kubectl get secret kcp-external-admin-kubeconfig-cert -n "${KCP_NAMESPACE}" -o jsonpath='{.data.tls\.crt}')
CLIENT_KEY=$(kubectl get secret kcp-external-admin-kubeconfig-cert -n "${KCP_NAMESPACE}" -o jsonpath='{.data.tls\.key}')
CA_CERT=$(kubectl get secret kcp-ca -n "${KCP_NAMESPACE}" -o jsonpath='{.data.ca\.crt}')

cat > "${SCRIPT_DIR}/kcp-admin.kubeconfig" <<EOF
apiVersion: v1
kind: Config
clusters:
  - name: kcp
    cluster:
      certificate-authority-data: ${CA_CERT}
      server: https://kcp.localhost:8443
contexts:
  - name: kcp
    context:
      cluster: kcp
      user: kcp-admin
current-context: kcp
users:
  - name: kcp-admin
    user:
      client-certificate-data: ${CLIENT_CERT}
      client-key-data: ${CLIENT_KEY}
EOF
chmod 600 "${SCRIPT_DIR}/kcp-admin.kubeconfig"

# Also build an in-cluster variant for the exporter (uses the front-proxy ClusterIP service)
FRONT_PROXY_PORT="$(kubectl get svc kcp-front-proxy -n "${KCP_NAMESPACE}" -o jsonpath='{.spec.ports[0].port}')"
FRONT_PROXY_SVC="https://kcp-front-proxy.${KCP_NAMESPACE}.svc.cluster.local:${FRONT_PROXY_PORT}"
cat > "${SCRIPT_DIR}/kcp-exporter.kubeconfig" <<EOF
apiVersion: v1
kind: Config
clusters:
  - name: kcp
    cluster:
      # Use insecure-skip since front-proxy cert SAN is kcp.localhost, not the service DNS
      insecure-skip-tls-verify: true
      server: ${FRONT_PROXY_SVC}
contexts:
  - name: kcp
    context:
      cluster: kcp
      user: kcp-admin
current-context: kcp
users:
  - name: kcp-admin
    user:
      client-certificate-data: ${CLIENT_CERT}
      client-key-data: ${CLIENT_KEY}
EOF

info "KCP admin kubeconfig written to ${SCRIPT_DIR}/kcp-admin.kubeconfig"

# -------------------------------------------------------------------
# Step 7: Build and deploy KCP exporter
# -------------------------------------------------------------------
info "Step 7/8: Building and deploying KCP resource exporter..."

# Build the exporter image
docker build -t kcp-exporter:latest "${SCRIPT_DIR}/kcp-exporter/"

# Load into Kind
kind load docker-image kcp-exporter:latest --name "${CLUSTER_NAME}"

# Create kubeconfig secret for the exporter (uses in-cluster service address)
kubectl create secret generic kcp-exporter-kubeconfig \
  --from-file=kubeconfig="${SCRIPT_DIR}/kcp-exporter.kubeconfig" \
  -n "${KCP_NAMESPACE}" \
  --dry-run=client -o yaml | kubectl apply -f -

# Deploy exporter
kubectl apply -f "${SCRIPT_DIR}/kcp-exporter/manifests/"

# Wait for exporter to be ready
kubectl wait --for=condition=Available deployment/kcp-exporter \
  -n "${KCP_NAMESPACE}" --timeout=120s || true

# -------------------------------------------------------------------
# Step 8: Provision Grafana dashboards
# -------------------------------------------------------------------
info "Step 8/8: Provisioning Grafana dashboards..."

for dashboard_file in "${SCRIPT_DIR}"/observability/grafana/dashboards/*.json; do
  dashboard_name=$(basename "${dashboard_file}" .json)
  kubectl create configmap "grafana-dashboard-${dashboard_name}" \
    --from-file="${dashboard_name}.json=${dashboard_file}" \
    -n "${OBS_NAMESPACE}" \
    --dry-run=client -o yaml | \
    kubectl label --local -f - grafana_dashboard=1 -o yaml | \
    kubectl apply -f -
done

# -------------------------------------------------------------------
# Summary
# -------------------------------------------------------------------
echo ""
echo "=============================================="
echo "  KCP + OTel Observability Stack is ready!"
echo "=============================================="
echo ""
echo "  Grafana:    http://localhost:3000  (admin/admin)"
echo "  Prometheus: http://localhost:9090"
echo "  KCP:        https://kcp.localhost:8443"
echo ""
echo "  KCP admin kubeconfig: ${SCRIPT_DIR}/kcp-admin.kubeconfig"
echo ""
echo "  Usage:"
echo "    export KUBECONFIG=${SCRIPT_DIR}/kcp-admin.kubeconfig"
echo "    kubectl ws tree"
echo "    kubectl get workspaces"
echo ""
echo "  Run demo:   ./demo.sh"
echo "  Teardown:   ./teardown.sh"
echo "=============================================="
