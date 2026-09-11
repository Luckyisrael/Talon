package api

import (
	"fmt"
	"net/http"

	"github.com/luckyisrael/talon/backend/internal/metrics"
	"github.com/luckyisrael/talon/backend/internal/redroid"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// MetricsHandler serves operational metrics in Prometheus text format:
// request volume, error rate, latency, agent turns, token and dollar spend,
// plus live gauges read from the store and the container manager at scrape
// time — never cached, so a scrape reflects what is actually happening.
type MetricsHandler struct {
	store   *store.Store
	manager *redroid.Manager
}

func NewMetricsHandler(s *store.Store, mgr *redroid.Manager) *MetricsHandler {
	return &MetricsHandler{store: s, manager: mgr}
}

func (h *MetricsHandler) Get(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	metrics.Default.WritePrometheus(w)

	sessions := 0
	if list, err := h.store.GetActiveSessions(); err == nil {
		sessions = len(list)
	}
	fmt.Fprintf(w, "# HELP talon_sessions_active Sessions not destroyed.\n")
	fmt.Fprintf(w, "# TYPE talon_sessions_active gauge\n")
	fmt.Fprintf(w, "talon_sessions_active %d\n", sessions)

	containers := -1
	if h.manager != nil {
		if list, err := h.manager.ListContainers(); err == nil {
			containers = len(list)
		}
	}
	if containers >= 0 {
		fmt.Fprintf(w, "# HELP talon_containers_running Docker containers under management.\n")
		fmt.Fprintf(w, "# TYPE talon_containers_running gauge\n")
		fmt.Fprintf(w, "talon_containers_running %d\n", containers)
	}

	spend, err := h.store.UsageCostTotal()
	if err == nil {
		fmt.Fprintf(w, "# HELP talon_spend_usd_total Model spend across all usage rows.\n")
		fmt.Fprintf(w, "# TYPE talon_spend_usd_total counter\n")
		fmt.Fprintf(w, "talon_spend_usd_total %.4f\n", spend)
	}
}
