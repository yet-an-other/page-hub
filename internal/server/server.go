// Package server implements the authenticated manager boundary and its
// reserved route space. Public Publication reading is an injected handler so
// its availability is independent from the manager.
package server

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/yet-an-other/page-hub/internal/config"
	"github.com/yet-an-other/page-hub/internal/storage"
	"github.com/yet-an-other/page-hub/web"
)

const (
	managerPrefix = "/_page-hub/"
	managerRoot   = "/_page-hub"
)

type Server struct {
	config    config.Config
	checker   storage.Checker
	inventory InventoryReader
	assets    fs.FS
	public    http.Handler
}

// New creates the manager HTTP handler. The public handler is only considered
// for non-reserved paths and may be nil when the public reader lives elsewhere.
// The inventory reader may be nil only when the catalog is absent, in which
// case the inventory API reports itself unavailable.
func New(cfg config.Config, checker storage.Checker, inventory InventoryReader, public http.Handler) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if checker == nil {
		checker = storage.NewS3Checker(cfg.Storage)
	}
	assets, err := fs.Sub(web.Files, "dist")
	if err != nil {
		return nil, err
	}
	return &Server{config: cfg, checker: checker, inventory: inventory, assets: assets, public: public}, nil
}

// Handler returns the complete manager/public routing boundary.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serveHTTP)
}

func (s *Server) serveHTTP(response http.ResponseWriter, request *http.Request) {
	if isReservedPath(request.URL.Path) {
		s.serveManager(response, request)
		return
	}
	s.servePublic(response, request)
}

func (s *Server) serveManager(response http.ResponseWriter, request *http.Request) {
	if !s.authorized(request) {
		s.writeUnauthorized(response, request)
		return
	}
	setPrivateHeaders(response)

	switch {
	case request.URL.Path == "/":
		s.serveShell(response, request)
	case request.URL.Path == managerRoot:
		http.Redirect(response, request, managerPrefix, http.StatusPermanentRedirect)
	case strings.HasPrefix(request.URL.Path, managerPrefix+"assets/"):
		s.serveAsset(response, request)
	case request.URL.Path == managerPrefix+"api/v1/status":
		s.serveStatus(response, request)
	case request.URL.Path == managerPrefix+"api/v1/inventory":
		s.serveInventory(response, request)
	case request.URL.Path == managerPrefix+"healthz":
		s.serveHealth(response, request)
	case request.URL.Path == managerPrefix+"readyz":
		s.serveReady(response, request)
	default:
		writeNotFound(response)
	}
}

func (s *Server) serveShell(response http.ResponseWriter, request *http.Request) {
	if !allowMethods(response, request, http.MethodGet, http.MethodHead) {
		return
	}
	s.serveEmbedded(response, request, "index.html")
}

func (s *Server) serveAsset(response http.ResponseWriter, request *http.Request) {
	if !allowMethods(response, request, http.MethodGet, http.MethodHead) {
		return
	}
	name := strings.TrimPrefix(request.URL.Path, managerPrefix+"assets/")
	if name == "" || name != path.Clean(name) || strings.HasPrefix(name, "../") || name == ".." {
		writeNotFound(response)
		return
	}
	s.serveEmbedded(response, request, path.Join("assets", name))
}

func (s *Server) serveEmbedded(response http.ResponseWriter, request *http.Request, name string) {
	contents, err := fs.ReadFile(s.assets, name)
	if err != nil {
		writeNotFound(response)
		return
	}
	http.ServeContent(response, request, path.Base(name), time.Time{}, bytes.NewReader(contents))
}

func (s *Server) serveStatus(response http.ResponseWriter, request *http.Request) {
	if !allowMethods(response, request, http.MethodGet, http.MethodHead) {
		return
	}
	result := s.checker.Check(request.Context())
	writeJSON(response, request, http.StatusOK, map[string]any{
		"version": s.config.Version,
		"storage": map[string]string{"status": string(result.Status)},
	})
}

func (s *Server) serveHealth(response http.ResponseWriter, request *http.Request) {
	if !allowMethods(response, request, http.MethodGet, http.MethodHead) {
		return
	}
	writeJSON(response, request, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": s.config.Version,
	})
}

func (s *Server) serveReady(response http.ResponseWriter, request *http.Request) {
	if !allowMethods(response, request, http.MethodGet, http.MethodHead) {
		return
	}
	result := s.checker.Check(request.Context())
	status := http.StatusOK
	readiness := "ready"
	if result.Status != storage.StatusReachable {
		status = http.StatusServiceUnavailable
		readiness = "degraded"
	}
	writeJSON(response, request, status, map[string]string{
		"status":  readiness,
		"storage": string(result.Status),
	})
}

func (s *Server) servePublic(response http.ResponseWriter, request *http.Request) {
	if s.public == nil {
		writeNotFound(response)
		return
	}
	// Public Publication reads must not receive manager credentials, cookies, or
	// the trusted assertion even when both readers share a public origin.
	publicRequest := request.Clone(request.Context())
	publicRequest.Header.Del(s.config.AuthAssertionHeader)
	publicRequest.Header.Del("X-Page-Hub-Assertion")
	publicRequest.Header.Del("X-Page-Hub-Identity")
	publicRequest.Header.Del("Authorization")
	publicRequest.Header.Del("Cookie")
	s.public.ServeHTTP(response, publicRequest)
}

func (s *Server) authorized(request *http.Request) bool {
	if s.config.DevAuthBypass {
		return true
	}
	values := request.Header.Values(s.config.AuthAssertionHeader)
	if len(values) != 1 || values[0] == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(values[0]), []byte(s.config.AuthAssertionValue)) == 1
}

func isReservedPath(requestPath string) bool {
	return requestPath == "/" || requestPath == managerRoot || strings.HasPrefix(requestPath, managerPrefix)
}

func setPrivateHeaders(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
}

func (s *Server) writeUnauthorized(response http.ResponseWriter, request *http.Request) {
	setPrivateHeaders(response)
	if request.URL.Path == "/" && s.config.AuthLoginURL != "" && (request.Method == http.MethodGet || request.Method == http.MethodHead) {
		http.Redirect(response, request, s.config.AuthLoginURL, http.StatusFound)
		return
	}
	writeJSON(response, request, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
}

func writeJSON(response http.ResponseWriter, request *http.Request, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	if request.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(response).Encode(value)
}

func writeNotFound(response http.ResponseWriter) {
	response.WriteHeader(http.StatusNotFound)
}

func allowMethods(response http.ResponseWriter, request *http.Request, methods ...string) bool {
	for _, method := range methods {
		if request.Method == method {
			return true
		}
	}
	writeMethodNotAllowed(response, methods...)
	return false
}

func writeMethodNotAllowed(response http.ResponseWriter, methods ...string) {
	response.Header().Set("Allow", strings.Join(methods, ", "))
	response.WriteHeader(http.StatusMethodNotAllowed)
}

// Listen opens either a TCP listener or a protected Unix socket. The cleanup
// function removes only a socket created by this process.
func Listen(address string) (net.Listener, func(), error) {
	if !strings.HasPrefix(address, "unix:") {
		listener, err := net.Listen("tcp", address)
		return listener, func() {}, err
	}

	pathName := strings.TrimPrefix(address, "unix:")
	if pathName == "" {
		return nil, func() {}, os.ErrInvalid
	}
	if info, err := os.Stat(pathName); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, func() {}, os.ErrExist
		}
		if err := os.Remove(pathName); err != nil {
			return nil, func() {}, err
		}
	}
	listener, err := net.Listen("unix", pathName)
	if err != nil {
		return nil, func() {}, err
	}
	if err := os.Chmod(pathName, 0o660); err != nil {
		_ = listener.Close()
		_ = os.Remove(pathName)
		return nil, func() {}, err
	}
	return listener, func() { _ = os.Remove(pathName) }, nil
}
