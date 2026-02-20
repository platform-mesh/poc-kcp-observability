package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
)

func init() {
	prometheus.MustRegister(workspacesTotal)
	prometheus.MustRegister(apiExportsTotal)
	prometheus.MustRegister(apiBindingsTotal)
	prometheus.MustRegister(apiResourceSchemasTotal)
	prometheus.MustRegister(logicalClustersTotal)
	prometheus.MustRegister(scrapeErrors)
}

// genericList represents a Kubernetes-style list response.
type genericList struct {
	Items []json.RawMessage `json:"items"`
}

// workspaceItem represents the minimal fields needed from a Workspace.
type workspaceItem struct {
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

// apiBindingItem represents the minimal fields needed from an APIBinding.
type apiBindingItem struct {
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
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
		return nil, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, path)
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

func collectMetrics(ctx context.Context, client *http.Client, cfg *rest.Config) {
	// Collect Workspaces
	if data, err := doRequest(ctx, client, cfg, "/clusters/root/apis/tenancy.kcp.io/v1alpha1/workspaces"); err != nil {
		log.Printf("Error fetching workspaces: %v", err)
		scrapeErrors.Inc()
	} else {
		var list genericList
		if err := json.Unmarshal(data, &list); err != nil {
			log.Printf("Error parsing workspaces: %v", err)
			scrapeErrors.Inc()
		} else {
			phaseCounts := make(map[string]float64)
			for _, raw := range list.Items {
				var ws workspaceItem
				if err := json.Unmarshal(raw, &ws); err == nil {
					phase := ws.Status.Phase
					if phase == "" {
						phase = "Unknown"
					}
					phaseCounts[phase]++
				}
			}
			workspacesTotal.Reset()
			for phase, count := range phaseCounts {
				workspacesTotal.WithLabelValues(phase).Set(count)
			}
		}
	}

	// Collect APIExports
	if data, err := doRequest(ctx, client, cfg, "/clusters/root/apis/apis.kcp.io/v1alpha1/apiexports"); err != nil {
		log.Printf("Error fetching apiexports: %v", err)
		scrapeErrors.Inc()
	} else {
		var list genericList
		if err := json.Unmarshal(data, &list); err != nil {
			log.Printf("Error parsing apiexports: %v", err)
			scrapeErrors.Inc()
		} else {
			apiExportsTotal.Set(float64(len(list.Items)))
		}
	}

	// Collect APIBindings
	if data, err := doRequest(ctx, client, cfg, "/clusters/root/apis/apis.kcp.io/v1alpha1/apibindings"); err != nil {
		log.Printf("Error fetching apibindings: %v", err)
		scrapeErrors.Inc()
	} else {
		var list genericList
		if err := json.Unmarshal(data, &list); err != nil {
			log.Printf("Error parsing apibindings: %v", err)
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
	} else {
		var list genericList
		if err := json.Unmarshal(data, &list); err != nil {
			log.Printf("Error parsing apiresourceschemas: %v", err)
			scrapeErrors.Inc()
		} else {
			apiResourceSchemasTotal.Set(float64(len(list.Items)))
		}
	}

	// Collect LogicalClusters
	if data, err := doRequest(ctx, client, cfg, "/clusters/root/apis/core.kcp.io/v1alpha1/logicalclusters"); err != nil {
		log.Printf("Error fetching logicalclusters: %v", err)
		scrapeErrors.Inc()
	} else {
		var list genericList
		if err := json.Unmarshal(data, &list); err != nil {
			log.Printf("Error parsing logicalclusters: %v", err)
			scrapeErrors.Inc()
		} else {
			logicalClustersTotal.Set(float64(len(list.Items)))
		}
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

	// Initial collection
	collectMetrics(ctx, client, cfg)

	// Periodic collection every 30s
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	go func() {
		for {
			select {
			case <-ticker.C:
				collectMetrics(ctx, client, cfg)
			case <-ctx.Done():
				return
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
