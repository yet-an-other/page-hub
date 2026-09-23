package server

import (
	"context"
	"net/http"

	"github.com/yet-an-other/page-hub/internal/catalog"
)

// InventoryReader is the server's narrow view of the catalog.
type InventoryReader interface {
	Inventory(ctx context.Context) ([]catalog.InventoryProject, error)
}

func (s *Server) serveInventory(response http.ResponseWriter, request *http.Request) {
	if !allowMethods(response, request, http.MethodGet, http.MethodHead) {
		return
	}
	if s.inventory == nil {
		writeJSON(response, request, http.StatusServiceUnavailable, map[string]string{"error": "inventory unavailable"})
		return
	}
	projects, err := s.inventory.Inventory(request.Context())
	if err != nil {
		// The failure detail may contain catalog internals; the browser only
		// learns that the inventory is unavailable.
		writeJSON(response, request, http.StatusInternalServerError, map[string]string{"error": "inventory unavailable"})
		return
	}
	if projects == nil {
		projects = []catalog.InventoryProject{}
	}
	writeJSON(response, request, http.StatusOK, map[string]any{"projects": projects})
}
