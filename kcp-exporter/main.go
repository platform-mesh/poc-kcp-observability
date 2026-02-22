package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	workspacesTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kcp_workspaces_total",
			Help: "Total number of KCP workspaces by phase",
		},
		[]string{"phase"},
	)
	apiExportsTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "kcp_apiexports_total",
			Help: "Total number of KCP APIExports",
		},
	)
	apiBindingsTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kcp_apibindings_total",
			Help: "Total number of KCP APIBindings by phase",
		},
		[]string{"phase"},
	)
	apiResourceSchemasTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "kcp_apiresourceschemas_total",
			Help: "Total number of KCP APIResourceSchemas",
		},
	)
	logicalClustersTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "kcp_logical_clusters_total",
			Help: "Total number of KCP logical clusters",
		},
	)
	scrapeErrors = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "kcp_exporter_scrape_errors_total",
			Help: "Total number of scrape errors",
		},
	)
	shardsTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "kcp_shards_total",
			Help: "Total number of KCP shards",
		},
	)
	shardInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kcp_shard_info",
			Help: "Information about KCP shards",
		},
		[]string{"name", "url", "ready"},
	)
	shardCondition = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kcp_shard_condition",
			Help: "KCP shard conditions",
		},
		[]string{"name", "type", "status"},
	)
	workspaceTypesTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "kcp_workspace_types_total",
			Help: "Total number of KCP workspace types",
		},
	)
	workspaceTypeInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kcp_workspace_type_info",
			Help: "Information about KCP workspace types",
		},
		[]string{"name", "has_initializer", "has_terminator"},
	)
	apiExportEndpointSlicesTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "kcp_apiexport_endpoint_slices_total",
			Help: "Total number of KCP APIExportEndpointSlices",
		},
	)
	apiExportEndpointsTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kcp_apiexport_endpoints_total",
			Help: "Number of endpoints per APIExport endpoint slice",
		},
		[]string{"export", "workspace"},
	)
	workspacePhaseDuration = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kcp_workspace_phase_duration_seconds",
			Help: "Duration in seconds a workspace has been in its current phase",
		},
		[]string{"path", "phase"},
	)
	workspaceStuckCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kcp_workspace_stuck_count",
			Help: "Number of workspaces stuck in a phase beyond threshold",
		},
		[]string{"phase"},
	)
	workspaceInfoGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kcp_workspace_info",
			Help: "Info metric for each discovered workspace (value always 1)",
		},
		[]string{"path", "name", "type", "phase"},
	)
	wsAPIExportsTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kcp_workspace_apiexports_total",
			Help: "Number of APIExports in each workspace",
		},
		[]string{"workspace_path"},
	)
	wsAPIBindingsTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kcp_workspace_apibindings_total",
			Help: "Number of APIBindings in each workspace by phase",
		},
		[]string{"workspace_path", "phase"},
	)
	wsAPIResourceSchemasTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kcp_workspace_apiresourceschemas_total",
			Help: "Number of APIResourceSchemas in each workspace",
		},
		[]string{"workspace_path"},
	)
	workspaceTreeDepth = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "kcp_workspace_tree_depth",
			Help: "Maximum depth of the workspace tree",
		},
	)
	workspaceChildrenTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kcp_workspace_children_total",
			Help: "Number of direct child workspaces",
		},
		[]string{"path"},
	)
	apiBindingInfoGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kcp_apibinding_info",
			Help: "Info metric for each APIBinding showing provider-consumer relationship (value always 1)",
		},
		[]string{"consumer_workspace", "provider_workspace", "export_name", "phase"},
	)
	apiExportConsumersTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kcp_apiexport_consumers_total",
			Help: "Number of consumer workspaces bound to each APIExport",
		},
		[]string{"provider_workspace", "export_name"},
	)
	apiExportInfoGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kcp_apiexport_info",
			Help: "Info metric for each APIExport with identity and schema count (value always 1)",
		},
		[]string{"workspace", "name", "identity_hash", "schema_count"},
	)
)

func init() {
	prometheus.MustRegister(workspacesTotal)
	prometheus.MustRegister(apiExportsTotal)
	prometheus.MustRegister(apiBindingsTotal)
	prometheus.MustRegister(apiResourceSchemasTotal)
	prometheus.MustRegister(logicalClustersTotal)
	prometheus.MustRegister(scrapeErrors)
	prometheus.MustRegister(shardsTotal)
	prometheus.MustRegister(shardInfo)
	prometheus.MustRegister(shardCondition)
	prometheus.MustRegister(workspaceTypesTotal)
	prometheus.MustRegister(workspaceTypeInfo)
	prometheus.MustRegister(apiExportEndpointSlicesTotal)
	prometheus.MustRegister(apiExportEndpointsTotal)
	prometheus.MustRegister(workspacePhaseDuration)
	prometheus.MustRegister(workspaceStuckCount)
	prometheus.MustRegister(workspaceInfoGauge)
	prometheus.MustRegister(wsAPIExportsTotal)
	prometheus.MustRegister(wsAPIBindingsTotal)
	prometheus.MustRegister(wsAPIResourceSchemasTotal)
	prometheus.MustRegister(workspaceTreeDepth)
	prometheus.MustRegister(workspaceChildrenTotal)
	prometheus.MustRegister(apiBindingInfoGauge)
	prometheus.MustRegister(apiExportConsumersTotal)
	prometheus.MustRegister(apiExportInfoGauge)
}

// genericList represents a Kubernetes-style list response.
type genericList struct {
	Items []json.RawMessage `json:"items"`
}

// workspaceLifecycleItem represents workspace fields needed for phase counts, lifecycle duration, and stuck detection.
type workspaceLifecycleItem struct {
	Metadata struct {
		Name              string `json:"name"`
		CreationTimestamp string `json:"creationTimestamp"`
	} `json:"metadata"`
	Status struct {
		Phase      string      `json:"phase"`
		Conditions []condition `json:"conditions"`
	} `json:"status"`
}

// stuckThresholds defines the duration after which a workspace in each phase is considered stuck.
var stuckThresholds = map[string]time.Duration{
	"Scheduling":  5 * time.Minute,
	"Initializing": 10 * time.Minute,
	"Terminating":  15 * time.Minute,
}

// apiBindingItem represents the minimal fields needed from an APIBinding.
type apiBindingItem struct {
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

// condition represents a Kubernetes-style status condition (reused by shards, workspaces, etc.).
type condition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	LastTransitionTime string `json:"lastTransitionTime"`
	Reason             string `json:"reason"`
	Message            string `json:"message"`
}

// shardItem represents the minimal fields needed from a KCP Shard.
type shardItem struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		BaseURL string `json:"baseURL"`
	} `json:"spec"`
	Status struct {
		Conditions []condition `json:"conditions"`
	} `json:"status"`
}

// endpointSliceItem represents the minimal fields needed from a KCP APIExportEndpointSlice.
type endpointSliceItem struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Export struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"export"`
	} `json:"spec"`
	Status struct {
		Endpoints []struct {
			URL string `json:"url"`
		} `json:"endpoints"`
	} `json:"status"`
}

// WorkspaceNode represents a node in the KCP workspace tree.
type WorkspaceNode struct {
	Name     string
	Path     string
	Type     string
	Phase    string
	Children []*WorkspaceNode
}

// workspaceFullItem represents workspace fields needed for tree discovery.
type workspaceFullItem struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Type struct {
			Name string `json:"name"`
		} `json:"type"`
	} `json:"spec"`
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

// workspaceTypeItem represents the minimal fields needed from a KCP WorkspaceType.
type workspaceTypeItem struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		DefaultChildWorkspaceType json.RawMessage `json:"defaultChildWorkspaceType"`
		Initializer               bool   `json:"initializer"`
		Terminator                bool   `json:"terminator"`
	} `json:"spec"`
}

// apiBindingFullItem parses the full APIBinding with reference details for topology.
type apiBindingFullItem struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Reference struct {
			Export struct {
				Path string `json:"path"`
				Name string `json:"name"`
			} `json:"export"`
		} `json:"reference"`
	} `json:"spec"`
	Status struct {
		Phase          string `json:"phase"`
		BoundResources []struct {
			Group    string `json:"group"`
			Resource string `json:"resource"`
		} `json:"boundResources"`
		Conditions []condition `json:"conditions"`
	} `json:"status"`
}

// apiExportFullItem parses the full APIExport with identity and schema details.
type apiExportFullItem struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		LatestResourceSchemas []string `json:"latestResourceSchemas"`
	} `json:"spec"`
	Status struct {
		IdentityHash string      `json:"identityHash"`
		Conditions   []condition `json:"conditions"`
	} `json:"status"`
}

// exportKey uniquely identifies an APIExport by workspace and name.
type exportKey struct {
	Workspace string
	Name      string
}

// exporterState holds the latest metric values for OTel async gauge callbacks.
type exporterState struct {
	mu                     sync.RWMutex
	workspaceCount         int64
	apiExportCount         int64
	apiBindingCount        int64
	apiResourceSchemaCount int64
	logicalClusterCount    int64
}

// otelInstruments holds synchronous OTel instruments for scrape observability.
type otelInstruments struct {
	scrapeErrors   otelmetric.Int64Counter
	scrapeDuration otelmetric.Float64Histogram
}

// initOTelMeterProvider creates an OTel meter provider that exports metrics via OTLP gRPC.
// The endpoint is read from OTEL_EXPORTER_OTLP_ENDPOINT env var, defaulting to
// opentelemetry-collector.observability.svc.cluster.local:4317.
func initOTelMeterProvider(ctx context.Context) (*metric.MeterProvider, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "otel-collector-opentelemetry-collector.observability.svc.cluster.local:4317"
	}

	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("creating gRPC connection to %s: %w", endpoint, err)
	}

	exporter, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, fmt.Errorf("creating OTLP metric exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("kcp-exporter"),
			semconv.ServiceVersion("0.1.0"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("creating OTel resource: %w", err)
	}

	mp := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(metric.NewPeriodicReader(exporter, metric.WithInterval(30*time.Second))),
	)

	return mp, nil
}

// registerAsyncGauges creates OTel async gauge instruments that report the same
// values as the Prometheus gauges, reading from exporterState under RLock.
func registerAsyncGauges(meter otelmetric.Meter, state *exporterState) error {
	if _, err := meter.Int64ObservableGauge("kcp.workspaces.total",
		otelmetric.WithDescription("Total number of KCP workspaces"),
		otelmetric.WithInt64Callback(func(_ context.Context, o otelmetric.Int64Observer) error {
			state.mu.RLock()
			defer state.mu.RUnlock()
			o.Observe(state.workspaceCount)
			return nil
		}),
	); err != nil {
		return fmt.Errorf("registering kcp.workspaces.total: %w", err)
	}

	if _, err := meter.Int64ObservableGauge("kcp.apiexports.total",
		otelmetric.WithDescription("Total number of KCP APIExports"),
		otelmetric.WithInt64Callback(func(_ context.Context, o otelmetric.Int64Observer) error {
			state.mu.RLock()
			defer state.mu.RUnlock()
			o.Observe(state.apiExportCount)
			return nil
		}),
	); err != nil {
		return fmt.Errorf("registering kcp.apiexports.total: %w", err)
	}

	if _, err := meter.Int64ObservableGauge("kcp.apibindings.total",
		otelmetric.WithDescription("Total number of KCP APIBindings"),
		otelmetric.WithInt64Callback(func(_ context.Context, o otelmetric.Int64Observer) error {
			state.mu.RLock()
			defer state.mu.RUnlock()
			o.Observe(state.apiBindingCount)
			return nil
		}),
	); err != nil {
		return fmt.Errorf("registering kcp.apibindings.total: %w", err)
	}

	if _, err := meter.Int64ObservableGauge("kcp.apiresourceschemas.total",
		otelmetric.WithDescription("Total number of KCP APIResourceSchemas"),
		otelmetric.WithInt64Callback(func(_ context.Context, o otelmetric.Int64Observer) error {
			state.mu.RLock()
			defer state.mu.RUnlock()
			o.Observe(state.apiResourceSchemaCount)
			return nil
		}),
	); err != nil {
		return fmt.Errorf("registering kcp.apiresourceschemas.total: %w", err)
	}

	if _, err := meter.Int64ObservableGauge("kcp.logical_clusters.total",
		otelmetric.WithDescription("Total number of KCP logical clusters"),
		otelmetric.WithInt64Callback(func(_ context.Context, o otelmetric.Int64Observer) error {
			state.mu.RLock()
			defer state.mu.RUnlock()
			o.Observe(state.logicalClusterCount)
			return nil
		}),
	); err != nil {
		return fmt.Errorf("registering kcp.logical_clusters.total: %w", err)
	}

	return nil
}

// newOTelInstruments creates synchronous OTel instruments for scrape observability.
func newOTelInstruments(meter otelmetric.Meter) (*otelInstruments, error) {
	scrapeErrs, err := meter.Int64Counter("kcp.exporter.scrape_errors.total",
		otelmetric.WithDescription("Total number of scrape errors"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating scrape errors counter: %w", err)
	}

	scrapeDur, err := meter.Float64Histogram("kcp.exporter.scrape_duration_seconds",
		otelmetric.WithDescription("Duration of scrape collection in seconds"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating scrape duration histogram: %w", err)
	}

	return &otelInstruments{scrapeErrors: scrapeErrs, scrapeDuration: scrapeDur}, nil
}

func buildRESTConfig() (*rest.Config, error) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = os.Getenv("KCP_KUBECONFIG")
	}
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}

func doRequest(ctx context.Context, client *http.Client, cfg *rest.Config, path string) ([]byte, error) {
	url := cfg.Host + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request for %s: %w", path, err)
	}
	if cfg.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.BearerToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		errBody := make([]byte, 512)
		n, _ := resp.Body.Read(errBody)
		return nil, fmt.Errorf("unexpected status %d for %s: %s", resp.StatusCode, path, string(errBody[:n]))
	}
	var body []byte
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
		}
		if readErr != nil {
			break
		}
	}
	return body, nil
}

func collectShards(ctx context.Context, client *http.Client, cfg *rest.Config) {
	data, err := doRequest(ctx, client, cfg, "/clusters/root/apis/core.kcp.io/v1alpha1/shards")
	if err != nil {
		log.Printf("Error fetching shards: %v", err)
		scrapeErrors.Inc()
		return
	}
	var list genericList
	if err := json.Unmarshal(data, &list); err != nil {
		log.Printf("Error parsing shards: %v", err)
		scrapeErrors.Inc()
		return
	}

	shardInfo.Reset()
	shardCondition.Reset()
	shardsTotal.Set(float64(len(list.Items)))

	for _, raw := range list.Items {
		var s shardItem
		if err := json.Unmarshal(raw, &s); err != nil {
			log.Printf("Error parsing shard item: %v", err)
			continue
		}
		ready := "false"
		for _, c := range s.Status.Conditions {
			shardCondition.WithLabelValues(s.Metadata.Name, c.Type, c.Status).Set(1)
			if c.Type == "Ready" && c.Status == "True" {
				ready = "true"
			}
		}
		// KCP v0.30.0 doesn't populate shard status.conditions.
		// Synthesize Ready=True for shards with a valid baseURL.
		if len(s.Status.Conditions) == 0 && s.Spec.BaseURL != "" {
			shardCondition.WithLabelValues(s.Metadata.Name, "Ready", "True").Set(1)
			ready = "true"
		}
		shardInfo.WithLabelValues(s.Metadata.Name, s.Spec.BaseURL, ready).Set(1)
	}
}

func collectWorkspaceTypes(ctx context.Context, client *http.Client, cfg *rest.Config) {
	data, err := doRequest(ctx, client, cfg, "/clusters/root/apis/tenancy.kcp.io/v1alpha1/workspacetypes")
	if err != nil {
		log.Printf("Error fetching workspace types: %v", err)
		scrapeErrors.Inc()
		return
	}
	var list genericList
	if err := json.Unmarshal(data, &list); err != nil {
		log.Printf("Error parsing workspace types: %v", err)
		scrapeErrors.Inc()
		return
	}

	workspaceTypeInfo.Reset()
	workspaceTypesTotal.Set(float64(len(list.Items)))

	for _, raw := range list.Items {
		var wt workspaceTypeItem
		if err := json.Unmarshal(raw, &wt); err != nil {
			log.Printf("Error parsing workspace type item: %v", err)
			continue
		}
		hasInitializer := "false"
		if wt.Spec.Initializer {
			hasInitializer = "true"
		}
		hasTerminator := "false"
		if wt.Spec.Terminator {
			hasTerminator = "true"
		}
		workspaceTypeInfo.WithLabelValues(wt.Metadata.Name, hasInitializer, hasTerminator).Set(1)
	}
}

func collectEndpointSlices(ctx context.Context, client *http.Client, cfg *rest.Config) {
	data, err := doRequest(ctx, client, cfg, "/clusters/root/apis/apis.kcp.io/v1alpha1/apiexportendpointslices")
	if err != nil {
		log.Printf("Error fetching apiexport endpoint slices: %v", err)
		scrapeErrors.Inc()
		return
	}
	var list genericList
	if err := json.Unmarshal(data, &list); err != nil {
		log.Printf("Error parsing apiexport endpoint slices: %v", err)
		scrapeErrors.Inc()
		return
	}

	apiExportEndpointsTotal.Reset()
	apiExportEndpointSlicesTotal.Set(float64(len(list.Items)))

	for _, raw := range list.Items {
		var es endpointSliceItem
		if err := json.Unmarshal(raw, &es); err != nil {
			log.Printf("Error parsing endpoint slice item: %v", err)
			continue
		}
		apiExportEndpointsTotal.WithLabelValues(es.Spec.Export.Name, es.Spec.Export.Path).Set(float64(len(es.Status.Endpoints)))
	}
}

// collectBindingsForWorkspace queries APIBindings for a single workspace and accumulates consumer counts.
func collectBindingsForWorkspace(ctx context.Context, client *http.Client, cfg *rest.Config, wsPath string, consumerCounts map[exportKey]int) {
	apiPath := fmt.Sprintf("/clusters/%s/apis/apis.kcp.io/v1alpha1/apibindings", wsPath)
	data, err := doRequest(ctx, client, cfg, apiPath)
	if err != nil {
		log.Printf("ERROR: collecting bindings for %s: %v", wsPath, err)
		scrapeErrors.Inc()
		return
	}

	var list struct {
		Items []apiBindingFullItem `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		log.Printf("ERROR: parsing bindings for %s: %v", wsPath, err)
		scrapeErrors.Inc()
		return
	}

	for _, b := range list.Items {
		providerWS := b.Spec.Reference.Export.Path
		exportName := b.Spec.Reference.Export.Name
		phase := b.Status.Phase
		if phase == "" {
			phase = "Unknown"
		}

		apiBindingInfoGauge.WithLabelValues(wsPath, providerWS, exportName, phase).Set(1)

		key := exportKey{Workspace: providerWS, Name: exportName}
		consumerCounts[key]++
	}
}

// collectExportsForWorkspace queries APIExports for a single workspace and sets export info metrics.
func collectExportsForWorkspace(ctx context.Context, client *http.Client, cfg *rest.Config, wsPath string) {
	apiPath := fmt.Sprintf("/clusters/%s/apis/apis.kcp.io/v1alpha1/apiexports", wsPath)
	data, err := doRequest(ctx, client, cfg, apiPath)
	if err != nil {
		log.Printf("ERROR: collecting exports for %s: %v", wsPath, err)
		scrapeErrors.Inc()
		return
	}

	var list struct {
		Items []apiExportFullItem `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		log.Printf("ERROR: parsing exports for %s: %v", wsPath, err)
		scrapeErrors.Inc()
		return
	}

	for _, e := range list.Items {
		schemaCount := fmt.Sprintf("%d", len(e.Spec.LatestResourceSchemas))
		identityHash := e.Status.IdentityHash
		if identityHash == "" {
			identityHash = "unknown"
		}
		apiExportInfoGauge.WithLabelValues(wsPath, e.Metadata.Name, identityHash, schemaCount).Set(1)
	}
}

// collectTopology walks the workspace tree collecting APIBinding and APIExport topology metrics.
func collectTopology(ctx context.Context, client *http.Client, cfg *rest.Config, tree []*WorkspaceNode) {
	apiBindingInfoGauge.Reset()
	apiExportConsumersTotal.Reset()
	apiExportInfoGauge.Reset()

	consumerCounts := make(map[exportKey]int)

	var walk func(nodes []*WorkspaceNode)
	walk = func(nodes []*WorkspaceNode) {
		for _, node := range nodes {
			if node.Phase != "Ready" {
				continue
			}
			collectBindingsForWorkspace(ctx, client, cfg, node.Path, consumerCounts)
			collectExportsForWorkspace(ctx, client, cfg, node.Path)
			walk(node.Children)
		}
	}
	walk(tree)

	for key, count := range consumerCounts {
		apiExportConsumersTotal.WithLabelValues(key.Workspace, key.Name).Set(float64(count))
	}
}

// calculatePhaseDuration returns the duration in seconds a workspace has been in its current phase.
// For Ready phase, it uses the lastTransitionTime of the Ready condition.
// For non-Ready phases, it falls back to creationTimestamp.
// Negative durations (clock skew) are clamped to 0.
func calculatePhaseDuration(ws workspaceLifecycleItem, now time.Time) float64 {
	if ws.Status.Phase == "Ready" {
		for _, c := range ws.Status.Conditions {
			if c.Type == "Ready" {
				if t, err := time.Parse(time.RFC3339, c.LastTransitionTime); err == nil {
					return math.Max(0, now.Sub(t).Seconds())
				}
			}
		}
	}
	// Fallback: use creationTimestamp
	if t, err := time.Parse(time.RFC3339, ws.Metadata.CreationTimestamp); err == nil {
		return math.Max(0, now.Sub(t).Seconds())
	}
	return 0
}

// discoverWorkspaces recursively discovers all workspaces starting from parentPath.
// Only Ready workspaces are recursed into. If recursion into a child fails, the
// subtree is skipped with a warning log rather than failing the whole tree.
func discoverWorkspaces(ctx context.Context, client *http.Client, cfg *rest.Config, parentPath string) ([]*WorkspaceNode, error) {
	path := fmt.Sprintf("/clusters/%s/apis/tenancy.kcp.io/v1alpha1/workspaces", parentPath)
	data, err := doRequest(ctx, client, cfg, path)
	if err != nil {
		return nil, fmt.Errorf("listing workspaces at %s: %w", parentPath, err)
	}
	var list genericList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parsing workspaces at %s: %w", parentPath, err)
	}

	var nodes []*WorkspaceNode
	for _, raw := range list.Items {
		var ws workspaceFullItem
		if err := json.Unmarshal(raw, &ws); err != nil {
			log.Printf("Warning: skipping unparseable workspace in %s: %v", parentPath, err)
			continue
		}
		childPath := parentPath + ":" + ws.Metadata.Name
		node := &WorkspaceNode{
			Name:  ws.Metadata.Name,
			Path:  childPath,
			Type:  ws.Spec.Type.Name,
			Phase: ws.Status.Phase,
		}
		if ws.Status.Phase == "Ready" {
			children, err := discoverWorkspaces(ctx, client, cfg, childPath)
			if err != nil {
				log.Printf("Warning: failed to discover children of %s, skipping subtree: %v", childPath, err)
			} else {
				node.Children = children
			}
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// collectForWorkspace queries apiexports, apibindings, and apiresourceschemas for a single workspace.
func collectForWorkspace(ctx context.Context, client *http.Client, cfg *rest.Config, wsPath string) {
	// APIExports
	exportsPath := fmt.Sprintf("/clusters/%s/apis/apis.kcp.io/v1alpha1/apiexports", wsPath)
	if data, err := doRequest(ctx, client, cfg, exportsPath); err != nil {
		log.Printf("Error fetching apiexports for %s: %v", wsPath, err)
		scrapeErrors.Inc()
	} else {
		var list genericList
		if err := json.Unmarshal(data, &list); err != nil {
			log.Printf("Error parsing apiexports for %s: %v", wsPath, err)
			scrapeErrors.Inc()
		} else {
			wsAPIExportsTotal.WithLabelValues(wsPath).Set(float64(len(list.Items)))
		}
	}

	// APIBindings
	bindingsPath := fmt.Sprintf("/clusters/%s/apis/apis.kcp.io/v1alpha1/apibindings", wsPath)
	if data, err := doRequest(ctx, client, cfg, bindingsPath); err != nil {
		log.Printf("Error fetching apibindings for %s: %v", wsPath, err)
		scrapeErrors.Inc()
	} else {
		var list genericList
		if err := json.Unmarshal(data, &list); err != nil {
			log.Printf("Error parsing apibindings for %s: %v", wsPath, err)
			scrapeErrors.Inc()
		} else {
			phaseCounts := make(map[string]float64)
			for _, raw := range list.Items {
				var ab apiBindingItem
				if err := json.Unmarshal(raw, &ab); err == nil {
					phase := ab.Status.Phase
					if phase == "" {
						phase = "Unknown"
					}
					phaseCounts[phase]++
				}
			}
			for phase, count := range phaseCounts {
				wsAPIBindingsTotal.WithLabelValues(wsPath, phase).Set(count)
			}
		}
	}

	// APIResourceSchemas
	schemasPath := fmt.Sprintf("/clusters/%s/apis/apis.kcp.io/v1alpha1/apiresourceschemas", wsPath)
	if data, err := doRequest(ctx, client, cfg, schemasPath); err != nil {
		log.Printf("Error fetching apiresourceschemas for %s: %v", wsPath, err)
		scrapeErrors.Inc()
	} else {
		var list genericList
		if err := json.Unmarshal(data, &list); err != nil {
			log.Printf("Error parsing apiresourceschemas for %s: %v", wsPath, err)
			scrapeErrors.Inc()
		} else {
			wsAPIResourceSchemasTotal.WithLabelValues(wsPath).Set(float64(len(list.Items)))
		}
	}
}

// calculateTreeDepth returns the maximum depth of the workspace tree.
func calculateTreeDepth(nodes []*WorkspaceNode) int {
	if len(nodes) == 0 {
		return 0
	}
	maxChild := 0
	for _, n := range nodes {
		childDepth := calculateTreeDepth(n.Children)
		if childDepth > maxChild {
			maxChild = childDepth
		}
	}
	return 1 + maxChild
}

// collectWorkspaceMetrics walks the workspace tree, sets info/tree metrics, and
// concurrently collects per-workspace resource counts for all Ready workspaces.
func collectWorkspaceMetrics(ctx context.Context, client *http.Client, cfg *rest.Config, tree []*WorkspaceNode, sem chan struct{}) {
	// Reset all per-workspace gauges to clear stale entries from deleted workspaces
	workspaceInfoGauge.Reset()
	wsAPIExportsTotal.Reset()
	wsAPIBindingsTotal.Reset()
	wsAPIResourceSchemasTotal.Reset()
	workspaceChildrenTotal.Reset()

	// Set tree depth (root level counts as depth 1 if there are children)
	depth := calculateTreeDepth(tree)
	if depth > 0 {
		depth++ // add 1 for root itself
	}
	workspaceTreeDepth.Set(float64(depth))

	// Set root children count
	workspaceChildrenTotal.WithLabelValues("root").Set(float64(len(tree)))

	var wg sync.WaitGroup
	var walk func(nodes []*WorkspaceNode)
	walk = func(nodes []*WorkspaceNode) {
		for _, node := range nodes {
			// Set info metric for every discovered workspace
			workspaceInfoGauge.WithLabelValues(node.Path, node.Name, node.Type, node.Phase).Set(1)
			// Set children count
			workspaceChildrenTotal.WithLabelValues(node.Path).Set(float64(len(node.Children)))

			if node.Phase != "Ready" {
				continue
			}
			wg.Add(1)
			go func(n *WorkspaceNode) {
				defer wg.Done()
				sem <- struct{}{}        // acquire
				defer func() { <-sem }() // release
				collectForWorkspace(ctx, client, cfg, n.Path)
			}(node)
			walk(node.Children)
		}
	}
	walk(tree)
	wg.Wait()
}

func collectMetrics(ctx context.Context, client *http.Client, cfg *rest.Config, state *exporterState, otelInst *otelInstruments) {
	start := time.Now()
	var wsCount, exportCount, bindCount, schemaCount, lcCount int64

	// Collect Workspaces (phase counts + lifecycle duration + stuck detection)
	if data, err := doRequest(ctx, client, cfg, "/clusters/root/apis/tenancy.kcp.io/v1alpha1/workspaces"); err != nil {
		log.Printf("Error fetching workspaces: %v", err)
		scrapeErrors.Inc()
		if otelInst != nil {
			otelInst.scrapeErrors.Add(ctx, 1)
		}
	} else {
		var list genericList
		if err := json.Unmarshal(data, &list); err != nil {
			log.Printf("Error parsing workspaces: %v", err)
			scrapeErrors.Inc()
			if otelInst != nil {
				otelInst.scrapeErrors.Add(ctx, 1)
			}
		} else {
			wsCount = int64(len(list.Items))
			phaseCounts := make(map[string]float64)
			workspacePhaseDuration.Reset()
			workspaceStuckCount.Reset()
			stuckCounts := make(map[string]float64)
			now := time.Now()

			for _, raw := range list.Items {
				var ws workspaceLifecycleItem
				if err := json.Unmarshal(raw, &ws); err != nil {
					continue
				}
				phase := ws.Status.Phase
				if phase == "" {
					phase = "Unknown"
				}
				phaseCounts[phase]++

				// Lifecycle duration
				duration := calculatePhaseDuration(ws, now)
				workspacePhaseDuration.WithLabelValues(ws.Metadata.Name, phase).Set(duration)

				// Stuck detection
				if threshold, ok := stuckThresholds[phase]; ok {
					if duration > threshold.Seconds() {
						stuckCounts[phase]++
					}
				}
			}

			workspacesTotal.Reset()
			for phase, count := range phaseCounts {
				workspacesTotal.WithLabelValues(phase).Set(count)
			}
			for phase, count := range stuckCounts {
				workspaceStuckCount.WithLabelValues(phase).Set(count)
			}
		}
	}

	// Collect APIExports
	if data, err := doRequest(ctx, client, cfg, "/clusters/root/apis/apis.kcp.io/v1alpha1/apiexports"); err != nil {
		log.Printf("Error fetching apiexports: %v", err)
		scrapeErrors.Inc()
		if otelInst != nil {
			otelInst.scrapeErrors.Add(ctx, 1)
		}
	} else {
		var list genericList
		if err := json.Unmarshal(data, &list); err != nil {
			log.Printf("Error parsing apiexports: %v", err)
			scrapeErrors.Inc()
			if otelInst != nil {
				otelInst.scrapeErrors.Add(ctx, 1)
			}
		} else {
			exportCount = int64(len(list.Items))
			apiExportsTotal.Set(float64(len(list.Items)))
		}
	}

	// Collect APIBindings
	if data, err := doRequest(ctx, client, cfg, "/clusters/root/apis/apis.kcp.io/v1alpha1/apibindings"); err != nil {
		log.Printf("Error fetching apibindings: %v", err)
		scrapeErrors.Inc()
		if otelInst != nil {
			otelInst.scrapeErrors.Add(ctx, 1)
		}
	} else {
		var list genericList
		if err := json.Unmarshal(data, &list); err != nil {
			log.Printf("Error parsing apibindings: %v", err)
			scrapeErrors.Inc()
			if otelInst != nil {
				otelInst.scrapeErrors.Add(ctx, 1)
			}
		} else {
			bindCount = int64(len(list.Items))
			phaseCounts := make(map[string]float64)
			for _, raw := range list.Items {
				var ab apiBindingItem
				if err := json.Unmarshal(raw, &ab); err == nil {
					phase := ab.Status.Phase
					if phase == "" {
						phase = "Unknown"
					}
					phaseCounts[phase]++
				}
			}
			apiBindingsTotal.Reset()
			for phase, count := range phaseCounts {
				apiBindingsTotal.WithLabelValues(phase).Set(count)
			}
		}
	}

	// Collect APIResourceSchemas
	if data, err := doRequest(ctx, client, cfg, "/clusters/root/apis/apis.kcp.io/v1alpha1/apiresourceschemas"); err != nil {
		log.Printf("Error fetching apiresourceschemas: %v", err)
		scrapeErrors.Inc()
		if otelInst != nil {
			otelInst.scrapeErrors.Add(ctx, 1)
		}
	} else {
		var list genericList
		if err := json.Unmarshal(data, &list); err != nil {
			log.Printf("Error parsing apiresourceschemas: %v", err)
			scrapeErrors.Inc()
			if otelInst != nil {
				otelInst.scrapeErrors.Add(ctx, 1)
			}
		} else {
			schemaCount = int64(len(list.Items))
			apiResourceSchemasTotal.Set(float64(len(list.Items)))
		}
	}

	// Collect LogicalClusters
	if data, err := doRequest(ctx, client, cfg, "/clusters/root/apis/core.kcp.io/v1alpha1/logicalclusters"); err != nil {
		log.Printf("Error fetching logicalclusters: %v", err)
		scrapeErrors.Inc()
		if otelInst != nil {
			otelInst.scrapeErrors.Add(ctx, 1)
		}
	} else {
		var list genericList
		if err := json.Unmarshal(data, &list); err != nil {
			log.Printf("Error parsing logicalclusters: %v", err)
			scrapeErrors.Inc()
			if otelInst != nil {
				otelInst.scrapeErrors.Add(ctx, 1)
			}
		} else {
			lcCount = int64(len(list.Items))
			logicalClustersTotal.Set(float64(len(list.Items)))
		}
	}

	// Collect Shards
	collectShards(ctx, client, cfg)

	// Collect Workspace Types
	collectWorkspaceTypes(ctx, client, cfg)

	// Collect APIExport Endpoint Slices
	collectEndpointSlices(ctx, client, cfg)

	// Dual-write: store current values in exporterState for OTel async gauges
	if state != nil {
		state.mu.Lock()
		state.workspaceCount = wsCount
		state.apiExportCount = exportCount
		state.apiBindingCount = bindCount
		state.apiResourceSchemaCount = schemaCount
		state.logicalClusterCount = lcCount
		state.mu.Unlock()
	}

	// Record scrape duration via OTel histogram
	if otelInst != nil {
		otelInst.scrapeDuration.Record(ctx, time.Since(start).Seconds())
	}
}

func main() {
	log.Println("Starting KCP resource exporter...")

	cfg, err := buildRESTConfig()
	if err != nil {
		log.Fatalf("Failed to build REST config: %v", err)
	}

	transport, err := rest.TransportFor(cfg)
	if err != nil {
		log.Fatalf("Failed to create transport: %v", err)
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		log.Println("Shutting down...")
		cancel()
	}()

	// State for OTel async gauges (dual-write from collectMetrics)
	state := &exporterState{}
	var otelInst *otelInstruments

	// Initialize OTel meter provider if OTEL_EXPORTER_OTLP_ENDPOINT is set
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
		mp, err := initOTelMeterProvider(ctx)
		if err != nil {
			log.Printf("WARNING: failed to initialize OTel meter provider, continuing with Prometheus-only: %v", err)
		} else {
			log.Printf("OTel meter provider initialized, exporting to %s", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
			defer func() {
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer shutdownCancel()
				if err := mp.Shutdown(shutdownCtx); err != nil {
					log.Printf("OTel meter provider shutdown error: %v", err)
				}
			}()

			meter := mp.Meter("kcp-exporter")
			if err := registerAsyncGauges(meter, state); err != nil {
				log.Printf("WARNING: failed to register OTel async gauges: %v", err)
			}
			inst, err := newOTelInstruments(meter)
			if err != nil {
				log.Printf("WARNING: failed to create OTel instruments: %v", err)
			} else {
				otelInst = inst
			}
		}
	} else {
		log.Println("OTEL_EXPORTER_OTLP_ENDPOINT not set, running in Prometheus-only mode")
	}

	// Initial collection (root-level metrics)
	collectMetrics(ctx, client, cfg, state, otelInst)

	// Periodic root-level collection every 30s
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	go func() {
		for {
			select {
			case <-ticker.C:
				collectMetrics(ctx, client, cfg, state, otelInst)
			case <-ctx.Done():
				return
			}
		}
	}()

	// Workspace tree discovery goroutine (5-minute interval)
	var (
		workspaceTree   []*WorkspaceNode
		workspaceTreeMu sync.RWMutex
	)

	go func() {
		// Run discovery once immediately on startup
		tree, err := discoverWorkspaces(ctx, client, cfg, "root")
		if err != nil {
			log.Printf("ERROR: initial workspace discovery: %v", err)
		} else {
			workspaceTreeMu.Lock()
			workspaceTree = tree
			workspaceTreeMu.Unlock()
			log.Printf("Initial workspace discovery complete: %d top-level workspaces", len(tree))
		}

		discoveryTicker := time.NewTicker(5 * time.Minute)
		defer discoveryTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-discoveryTicker.C:
				tree, err := discoverWorkspaces(ctx, client, cfg, "root")
				if err != nil {
					log.Printf("ERROR: workspace discovery: %v", err)
					continue
				}
				workspaceTreeMu.Lock()
				workspaceTree = tree
				workspaceTreeMu.Unlock()
				log.Printf("Workspace discovery refreshed: %d top-level workspaces", len(tree))
			}
		}
	}()

	// Per-workspace collection goroutine (60-second interval)
	sem := make(chan struct{}, 10)
	go func() {
		collectionTicker := time.NewTicker(60 * time.Second)
		defer collectionTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-collectionTicker.C:
				workspaceTreeMu.RLock()
				tree := workspaceTree
				workspaceTreeMu.RUnlock()
				if tree != nil {
					collectWorkspaceMetrics(ctx, client, cfg, tree, sem)
					collectTopology(ctx, client, cfg, tree)
				}
			}
		}
	}()

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	server := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP server shutdown error: %v", err)
		}
	}()

	log.Println("Listening on :8080")
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}
	log.Println("Exporter stopped.")
}
