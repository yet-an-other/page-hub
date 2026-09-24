package server

import (
	"context"
	"net/http"

	"github.com/yet-an-other/page-hub/internal/catalog"
)

// InventoryReader is the server's narrow view of the catalog.
type InventoryReader interface {
	Inventory(ctx context.Context) (catalog.Inventory, error)
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
	// Quota is deployment configuration, not cataloged state.
	if inventory.Observation != nil {
		inventory.Observation.Usage.QuotaBytes = s.config.StorageQuotaBytes
	}
	writeJSON(response, request, http.StatusOK, inventory)
}
