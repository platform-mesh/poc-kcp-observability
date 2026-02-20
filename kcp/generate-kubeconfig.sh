#!/usr/bin/env bash
# Generate a KCP admin kubeconfig from cert-manager-issued client certificates.
# Usage: ./generate-kubeconfig.sh [output-file]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTPUT="${1:-${SCRIPT_DIR}/../kcp-admin.kubeconfig}"
NAMESPACE="kcp"
SECRET_NAME="kcp-admin-client-cert"
CA_SECRET="kcp-pki-ca"
KCP_HOST="https://kcp.localhost:8443"

echo "==> Waiting for admin client certificate to be ready..."
kubectl wait --for=condition=Ready certificate/${SECRET_NAME} \
  -n "${NAMESPACE}" --timeout=120s

echo "==> Extracting certificates from secret ${SECRET_NAME}..."
CLIENT_CERT=$(kubectl get secret "${SECRET_NAME}" -n "${NAMESPACE}" -o jsonpath='{.data.tls\.crt}')
CLIENT_KEY=$(kubectl get secret "${SECRET_NAME}" -n "${NAMESPACE}" -o jsonpath='{.data.tls\.key}')
CA_CERT=$(kubectl get secret "${SECRET_NAME}" -n "${NAMESPACE}" -o jsonpath='{.data.ca\.crt}')

# Fallback: try getting CA from the CA issuer secret
if [ -z "${CA_CERT}" ]; then
  echo "==> CA not found in client cert secret, trying CA secret..."
  CA_CERT=$(kubectl get secret "${CA_SECRET}" -n "${NAMESPACE}" -o jsonpath='{.data.ca\.crt}' 2>/dev/null || \
            kubectl get secret "${CA_SECRET}" -n "${NAMESPACE}" -o jsonpath='{.data.tls\.crt}' 2>/dev/null || true)
fi

if [ -z "${CLIENT_CERT}" ] || [ -z "${CLIENT_KEY}" ] || [ -z "${CA_CERT}" ]; then
  echo "ERROR: Could not extract all required certificate data."
  echo "  CLIENT_CERT present: $([ -n "${CLIENT_CERT}" ] && echo yes || echo no)"
  echo "  CLIENT_KEY present:  $([ -n "${CLIENT_KEY}" ] && echo yes || echo no)"
  echo "  CA_CERT present:     $([ -n "${CA_CERT}" ] && echo yes || echo no)"
  exit 1
fi

echo "==> Writing kubeconfig to ${OUTPUT}..."
cat > "${OUTPUT}" <<EOF
apiVersion: v1
kind: Config
clusters:
  - cluster:
      certificate-authority-data: ${CA_CERT}
      server: ${KCP_HOST}
    name: kcp
contexts:
  - context:
      cluster: kcp
      user: kcp-admin
    name: kcp
current-context: kcp
users:
  - name: kcp-admin
    user:
      client-certificate-data: ${CLIENT_CERT}
      client-key-data: ${CLIENT_KEY}
EOF

chmod 600 "${OUTPUT}"
echo "==> KCP admin kubeconfig written to ${OUTPUT}"
echo "    Test with: KUBECONFIG=${OUTPUT} kubectl api-resources"
