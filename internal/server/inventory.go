package server

import (
	"context"
	"net/http"
	"time"

	"github.com/yet-an-other/page-hub/internal/catalog"
	"github.com/yet-an-other/page-hub/internal/observe"
)

// InventoryReader is the server's narrow view of the catalog.
type InventoryReader interface {
	Inventory(ctx context.Context) (catalog.Inventory, error)
}

// RefreshState is the manager's view of observation progress.
type RefreshState struct {
	Running     bool   `json:"running"`
	LastOutcome string `json:"lastOutcome"`
}

// inventoryResponse is the manager API view: cataloged projects and the
// latest observation, plus live refresh progress. Catalog data is returned
// immediately and is never cleared or replaced by a running scan.
type inventoryResponse struct {
	Projects    []catalog.InventoryProject `json:"projects"`
	Observation *catalog.BucketObservation `json:"observation"`
	Refresh     RefreshState               `json:"refresh"`
}

func (s *Server) serveInventory(response http.ResponseWriter, request *http.Request) {
	if !allowMethods(response, request, http.MethodGet, http.MethodHead) {
		return
	}
	if s.inventory == nil {
		writeJSON(response, request, http.StatusServiceUnavailable, map[string]string{"error": "inventory unavailable"})
		return
	}
	inventory, err := s.inventory.Inventory(request.Context())
	if err != nil {
		// The failure detail may contain catalog internals; the browser only
		// learns that the inventory is unavailable.
		writeJSON(response, request, http.StatusServiceUnavailable, map[string]string{"error": "inventory unavailable"})
		return
	}
	if inventory.Projects == nil {
		inventory.Projects = []catalog.InventoryProject{}
	}
	// Quota is deployment configuration, not cataloged state; staleness is a
	// property of the observation age at read time.
	if inventory.Observation != nil {
		inventory.Observation.Usage.QuotaBytes = s.config.StorageQuotaBytes
		inventory.Observation.Stale = time.Since(inventory.Observation.ObservedAt) > observationStaleAfter
	}
	payload := inventoryResponse{
		Projects:    inventory.Projects,
		Observation: inventory.Observation,
		Refresh:     s.refreshState(),
	}
	writeJSON(response, request, http.StatusOK, payload)
}

// serveRefresh requests a storage observation. It joins an already running
// scan instead of queueing another one and answers with the resulting state;
// cataloged data stays visible the whole time. A scan may legitimately take
// longer than the server's WriteTimeout, so the write deadline is lifted for
// this one response: it answers when the joined scan completes.
func (s *Server) serveRefresh(response http.ResponseWriter, request *http.Request) {
	if !allowMethods(response, request, http.MethodPost) {
		return
	}
	if s.refresher == nil {
		writeJSON(response, request, http.StatusServiceUnavailable, map[string]string{"error": "refresh unavailable"})
		return
	}
	_ = http.NewResponseController(response).SetWriteDeadline(time.Time{})
	s.refresher.Refresh(request.Context())
	writeJSON(response, request, http.StatusOK, s.refreshState())
}

func (s *Server) refreshState() RefreshState {
	if s.refresher == nil {
		return RefreshState{Running: false, LastOutcome: string(observe.OutcomeNever)}
	}
	running, last := s.refresher.State()
	return RefreshState{Running: running, LastOutcome: string(last)}
}
