.PHONY: setup teardown demo status logs-kcp logs-exporter logs-prometheus logs-otel port-forward-grafana port-forward-prometheus build-exporter help

CLUSTER_NAME := kcp-otel
KCP_NS := kcp
OBS_NS := observability

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-25s\033[0m %s\n", $$1, $$2}'

setup: ## Create Kind cluster and deploy full stack
	bash setup.sh

teardown: ## Delete Kind cluster and clean up
	bash teardown.sh

demo: ## Create sample KCP resources
	bash demo.sh

status: ## Show status of all components
	@echo "=== Kind Cluster ==="
	@kind get clusters 2>/dev/null | grep -q "^$(CLUSTER_NAME)$$" && echo "Running" || echo "Not found"
	@echo ""
	@echo "=== KCP Pods ==="
	@kubectl get pods -n $(KCP_NS) 2>/dev/null || echo "Not available"
	@echo ""
	@echo "=== Observability Pods ==="
	@kubectl get pods -n $(OBS_NS) 2>/dev/null || echo "Not available"
	@echo ""
	@echo "=== Prometheus Targets ==="
	@curl -s http://localhost:9090/api/v1/targets 2>/dev/null | \
		python3 -c "import sys,json; d=json.load(sys.stdin); [print(f\"  {t['labels'].get('job','?')}: {t['health']}\") for t in d.get('data',{}).get('activeTargets',[])]" 2>/dev/null || \
		echo "  Not available"

logs-kcp: ## Tail KCP server logs
	kubectl logs -f -l app.kubernetes.io/component=kcp -n $(KCP_NS) --tail=100

logs-exporter: ## Tail KCP exporter logs
	kubectl logs -f -l app=kcp-exporter -n $(KCP_NS) --tail=100

logs-prometheus: ## Tail Prometheus logs
	kubectl logs -f -l app.kubernetes.io/name=prometheus -n $(OBS_NS) --tail=100

logs-otel: ## Tail OTel Collector logs
	kubectl logs -f -l app.kubernetes.io/name=opentelemetry-collector -n $(OBS_NS) --tail=100

build-exporter: ## Rebuild and redeploy KCP exporter
	docker build -t kcp-exporter:latest kcp-exporter/
	kind load docker-image kcp-exporter:latest --name $(CLUSTER_NAME)
	kubectl rollout restart deployment/kcp-exporter -n $(KCP_NS)
	kubectl rollout status deployment/kcp-exporter -n $(KCP_NS) --timeout=60s

port-forward-grafana: ## Port-forward Grafana to localhost:3000
	kubectl port-forward svc/kube-prometheus-stack-grafana 3000:80 -n $(OBS_NS)

port-forward-prometheus: ## Port-forward Prometheus to localhost:9090
	kubectl port-forward svc/kube-prometheus-stack-prometheus 9090:9090 -n $(OBS_NS)
