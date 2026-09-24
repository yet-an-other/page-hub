package adoption

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxProbeBodyBytes bounds how much of a probed public response Page Hub
// reads and hashes. Legacy publications are small static sites.
const maxProbeBodyBytes = 32 << 20

// ProbeObservation is the observed behavior of one public route.
type ProbeObservation struct {
	Status      int
	ContentType string
	BodySHA256  string
}

// RouteProber performs the declared public-route probes: plain GETs against
// the public routing layer that never touch storage or the manager.
type RouteProber interface {
	Probe(ctx context.Context, publicURL string) (ProbeObservation, error)
}

// HTTPRouteProber probes public routes with a bounded HTTP client.
type HTTPRouteProber struct {
	client *http.Client
}

// NewHTTPRouteProber returns a prober with a bounded network timeout.
func NewHTTPRouteProber() *HTTPRouteProber {
	return &HTTPRouteProber{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// NewHTTPRouteProberWithClient keeps production behavior while letting tests
// supply the transport. The client must not be nil.
func NewHTTPRouteProberWithClient(client *http.Client) *HTTPRouteProber {
	return &HTTPRouteProber{client: client}
}

// Probe GETs one public URL and records its status, content type, and body
// digest. Transport failures and oversized bodies are errors, not probe
// observations, so planning fails closed.
func (p *HTTPRouteProber) Probe(ctx context.Context, publicURL string) (ProbeObservation, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, publicURL, nil)
	if err != nil {
		return ProbeObservation{}, fmt.Errorf("build probe request for %s: %w", publicURL, err)
	}
	response, err := p.client.Do(request)
	if err != nil {
		return ProbeObservation{}, fmt.Errorf("probe %s: %w", publicURL, err)
	}
	defer response.Body.Close()

	digest := sha256.New()
	read, err := io.Copy(digest, io.LimitReader(response.Body, maxProbeBodyBytes+1))
	if err != nil {
		return ProbeObservation{}, fmt.Errorf("read probe response from %s: %w", publicURL, err)
	}
	if read > maxProbeBodyBytes {
		return ProbeObservation{}, fmt.Errorf("probe %s: response exceeds %d bytes", publicURL, maxProbeBodyBytes)
	}

	return ProbeObservation{
		Status:      response.StatusCode,
		ContentType: strings.TrimSpace(response.Header.Get("Content-Type")),
		BodySHA256:  "sha256:" + hex.EncodeToString(digest.Sum(nil)),
	}, nil
}
