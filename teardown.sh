#!/usr/bin/env bash
# Full cleanup of KCP + OTel observability stack.
set -euo pipefail

CLUSTER_NAME="kcp-otel"

info()  { echo "===> $*"; }

info "Deleting Kind cluster '${CLUSTER_NAME}'..."
if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  kind delete cluster --name "${CLUSTER_NAME}"
  info "Cluster deleted."
else
  info "Cluster '${CLUSTER_NAME}' does not exist, nothing to delete."
fi

# Clean up generated kubeconfig
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
rm -f "${SCRIPT_DIR}/kcp-admin.kubeconfig" "${SCRIPT_DIR}/kcp-exporter.kubeconfig"
info "Removed generated kubeconfig files"

info "Teardown complete."
