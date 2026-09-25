package server

import (
	"net/http"
	"path"
	"strings"
	"text/template"

	"github.com/yet-an-other/page-hub/internal/adoption"
	"github.com/yet-an-other/page-hub/internal/catalog"
)

// previewPrefix is the reserved manager prefix of the authenticated preview
// route. One Publication path follows it, e.g. /_page-hub/preview/notes/report.
const previewPrefix = managerPrefix + "preview/"

// canonicalURL joins the configured public base URL with a Publication's
// public URL. Exact-file Publications are addressed by their entry point so
// the URL names the page readers actually request.
func (s *Server) canonicalURL(publication catalog.InventoryPublication) string {
	if s.config.PublicBaseURL == "" {
		return ""
	}
	return adoption.CanonicalURL(s.config.PublicBaseURL, publication.Path, publication.EntryPoint, adoption.RoutingMode(publication.RoutingMode))
}

// servePreview answers an authenticated Publication-path preview. An in-sync
// Publication redirects to its canonical public URL; every other state
// receives a warning page and never redirects. The redirect target is the
// independent public reader, so no manager credentials travel with it.
func (s *Server) servePreview(response http.ResponseWriter, request *http.Request) {
	if !allowMethods(response, request, http.MethodGet, http.MethodHead) {
		return
	}
	publicationPath := strings.TrimPrefix(request.URL.Path, previewPrefix)
	if !validPublicationPath(publicationPath) {
		s.writePreviewWarning(response, request, previewWarning{
			Status: http.StatusNotFound,
			Reason: "No managed Publication exists at this path.",
		})
		return
	}

	inventory, err := s.inventory.Inventory(request.Context())
	if err != nil {
		// Catalog internals stay out of the browser; the failure still
		// fails closed instead of redirecting.
		s.writePreviewWarning(response, request, previewWarning{
			Status: http.StatusServiceUnavailable,
			Path:   publicationPath,
			Reason: "The catalog is unavailable, so the observed state of this Publication is unknown.",
		})
		return
	}
	publication, found := findPublication(inventory, publicationPath)
	if !found {
		s.writePreviewWarning(response, request, previewWarning{
			Status: http.StatusNotFound,
			Path:   publicationPath,
			Reason: "No managed Publication exists at this path.",
		})
		return
	}

	publicURL := s.canonicalURL(publication)
	if publication.Observation == nil || publication.Observation.State != catalog.StateInSync {
		s.writePreviewWarning(response, request, s.previewWarningFor(publication, inventory, publicURL))
		return
	}
	if publicURL == "" {
		s.writePreviewWarning(response, request, previewWarning{
			Status:      http.StatusServiceUnavailable,
			DisplayName: publication.DisplayName,
			Path:        publication.Path,
			Reason:      "Page Hub is not configured with a public base URL, so it cannot redirect to the canonical public URL.",
		})
		return
	}
	http.Redirect(response, request, publicURL, http.StatusFound)
}

// previewWarningFor explains why a known, non-in-sync Publication cannot be
// previewed right now.
func (s *Server) previewWarningFor(publication catalog.InventoryPublication, inventory catalog.Inventory, publicURL string) previewWarning {
	warning := previewWarning{
		Status:      http.StatusConflict,
		DisplayName: publication.DisplayName,
		Path:        publication.Path,
		PublicURL:   publicURL,
	}
	switch {
	case inventory.Observation == nil:
		warning.Reason = "Page Hub has not completed a storage observation yet, so the state of this Publication is unknown."
		return warning
	case publication.Observation == nil:
		warning.Reason = "The latest observation did not classify this Publication, so its state is unknown."
	default:
		switch publication.Observation.State {
		case catalog.StateDrifted:
			warning.Reason = "This Publication drifted from its accepted manifest. Preview opens only an in-sync Publication."
		case catalog.StateMissing:
			warning.Reason = "This Publication is missing from storage. Preview opens only an in-sync Publication."
		default:
			warning.Reason = "This Publication is not in sync, so Page Hub refuses to preview it."
		}
		warning.StatusDetail = publication.Observation.StatusDetail
	}
	warning.ObservedAt = inventory.Observation.ObservedAt.UTC().Format("2006-01-02 15:04") + " UTC"
	warning.Stale = observationStale(inventory.Observation.ObservedAt)
	return warning
}

// previewWarning renders as a standalone warning page with no manager assets.
type previewWarning struct {
	Status       int
	DisplayName  string
	Path         string
	Reason       string
	StatusDetail string
	ObservedAt   string
	Stale        bool
	PublicURL    string
}

func (s *Server) writePreviewWarning(response http.ResponseWriter, request *http.Request, warning previewWarning) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(warning.Status)
	if request.Method == http.MethodHead {
		return
	}
	_ = previewWarningTemplate.Execute(response, warning)
}

// validPublicationPath enforces the exact relative path shape used by the
// catalog: no empty, current, or parent segments. The value is only ever
// compared against cataloged paths, never used to address storage.
func validPublicationPath(publicationPath string) bool {
	if publicationPath == "" {
		return false
	}
	for segment := range strings.SplitSeq(publicationPath, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return publicationPath == path.Clean(publicationPath)
}

func findPublication(inventory catalog.Inventory, publicationPath string) (catalog.InventoryPublication, bool) {
	for _, project := range inventory.Projects {
		for _, publication := range project.Publications {
			if publication.Path == publicationPath {
				return publication, true
			}
		}
	}
	return catalog.InventoryPublication{}, false
}

var previewWarningTemplate = template.Must(template.New("preview-warning").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Preview unavailable — Page Hub</title>
<style>
  body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: #f7f8f5; color: #17201d; font-family: system-ui, -apple-system, "Segoe UI", sans-serif; }
  main { max-width: 34rem; margin: 2rem 1rem; padding: 2rem; border: 1px solid #dce1dd; border-radius: 14px; background: #fff; }
  h1 { margin: 0 0 .5rem; font-size: 1.4rem; }
  p { margin: .5rem 0; line-height: 1.5; }
  code { font-family: ui-monospace, Consolas, monospace; font-size: .9em; }
  .detail { color: #915b14; }
  .actions { margin-top: 1.5rem; display: flex; gap: .75rem; flex-wrap: wrap; }
  a { color: #126448; }
</style>
</head>
<body>
<main>
  <h1>Preview unavailable</h1>
  {{- if .DisplayName}}
  <p><strong>{{.DisplayName}}</strong>{{if .Path}} — <code>/{{.Path}}</code>{{end}}</p>
  {{- else if .Path}}
  <p><code>/{{.Path}}</code></p>
  {{- end}}
  <p>{{.Reason}}</p>
  {{- if .StatusDetail}}
  <p class="detail">{{.StatusDetail}}</p>
  {{- end}}
  {{- if .ObservedAt}}
  <p>Last observed {{.ObservedAt}}.{{if .Stale}} This observation is stale.{{end}}</p>
  {{- end}}
  <div class="actions">
    <a href="/">Back to the manager</a>
    {{- if .PublicURL}}
    <a href="{{.PublicURL}}" target="_blank" rel="noopener noreferrer">Open the public page anyway</a>
    {{- end}}
  </div>
</main>
</body>
</html>
`))
