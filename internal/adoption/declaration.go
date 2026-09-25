// Package adoption validates explicit adoption declarations, plans them
// against observed storage without changing anything, and commits approved
// plans into the catalog atomically. Adoption never writes, copies, or
// deletes a storage object.
package adoption

import (
	"fmt"
	"strings"
	"unicode"
)

// RoutingMode is the accepted URL behavior of a Publication.
type RoutingMode string

const (
	RoutingExactFile      RoutingMode = "exact_file"
	RoutingDirectoryIndex RoutingMode = "directory_index"
	RoutingFallback       RoutingMode = "fallback"
)

const reservedManagerPrefix = "_page-hub"

// ProjectDeclaration names one proposed Project.
type ProjectDeclaration struct {
	Prefix      string `json:"prefix"`
	DisplayName string `json:"displayName,omitempty"`
}

// RouteProbe declares one public-route check: a plain GET of the canonical
// public URL joined with the relative probe path must return the expected
// status. The probe path "" addresses the Publication path itself.
type RouteProbe struct {
	Path         string `json:"path"`
	ExpectStatus int    `json:"expectStatus"`
}

// PublicationDeclaration declares one adoption candidate: the Project it
// belongs to, its path, entry point, routing behavior, exact object set, and
// the public routes whose behavior adoption must preserve. Page Hub never
// infers Publication boundaries from storage.
type PublicationDeclaration struct {
	Project     ProjectDeclaration `json:"project"`
	Path        string             `json:"path"`
	DisplayName string             `json:"displayName,omitempty"`
	EntryPoint  string             `json:"entryPoint"`
	RoutingMode RoutingMode        `json:"routingMode"`
	Objects     []string           `json:"objects"`
	Probes      []RouteProbe       `json:"probes"`
}

// Declaration is an explicit adoption batch. The first release adopts exactly
// one candidate; the schema already allows the heterogeneous batches that the
// batch-adoption ticket introduces.
type Declaration struct {
	Version      int                      `json:"version"`
	Publications []PublicationDeclaration `json:"publications"`
}

// Validate checks the complete declaration. A rejected declaration never
// touches storage or the catalog.
func (d Declaration) Validate() error {
	if d.Version != 1 {
		return fmt.Errorf("unsupported declaration version %d", d.Version)
	}
	if len(d.Publications) == 0 {
		return fmt.Errorf("declaration must contain at least one publication")
	}
	seenObjects := map[string]int{}
	projectDisplayNames := map[string]string{}
	for index, publication := range d.Publications {
		if err := publication.Validate(); err != nil {
			return fmt.Errorf("publication %d: %w", index, err)
		}
		for _, key := range publication.Objects {
			if owner, claimed := seenObjects[key]; claimed {
				return fmt.Errorf("publication %d: object %q is also declared by publication %d", index, key, owner)
			}
			seenObjects[key] = index
		}
		if declared, seen := projectDisplayNames[publication.Project.Prefix]; seen {
			if declared != publication.Project.DisplayName {
				return fmt.Errorf("publication %d: project %q is declared with conflicting display names %q and %q",
					index, publication.Project.Prefix, declared, publication.Project.DisplayName)
			}
		} else {
			projectDisplayNames[publication.Project.Prefix] = publication.Project.DisplayName
		}
	}
	if err := checkBoundaries(d.Publications); err != nil {
		return err
	}
	return nil
}

// checkBoundaries rejects declared Publication paths that overlap: identical
// paths, and a path nested inside another, make Publication boundaries and
// served routes ambiguous. Adoption must be able to assign every object and
// every public route to exactly one Publication.
func checkBoundaries(publications []PublicationDeclaration) error {
	for i := 0; i < len(publications); i++ {
		for j := i + 1; j < len(publications); j++ {
			outer, inner := publications[i].Path, publications[j].Path
			switch {
			case outer == inner:
				return fmt.Errorf("publication path %q is declared twice; boundaries are ambiguous", outer)
			case isSegmentPrefix(outer, inner):
				return fmt.Errorf("ambiguous Publication boundary: %q is nested inside %q", inner, outer)
			case isSegmentPrefix(inner, outer):
				return fmt.Errorf("ambiguous Publication boundary: %q is nested inside %q", outer, inner)
			}
		}
	}
	return nil
}

// isSegmentPrefix reports whether b lies inside a at a path-segment
// boundary, so the served route spaces of the two paths overlap.
func isSegmentPrefix(a, b string) bool {
	return strings.HasPrefix(b, a+"/")
}

// Validate checks one declared Publication against the path rules.
func (p PublicationDeclaration) Validate() error {
	if err := validatePrefix(p.Project.Prefix); err != nil {
		return err
	}
	if err := validatePath(p.Project.Prefix, p.Path); err != nil {
		return err
	}
	switch p.RoutingMode {
	case RoutingExactFile, RoutingDirectoryIndex, RoutingFallback:
	default:
		return fmt.Errorf("invalid routing mode %q", p.RoutingMode)
	}
	if len(p.Objects) == 0 {
		return fmt.Errorf("the object set must not be empty")
	}
	if p.RoutingMode == RoutingExactFile && len(p.Objects) != 1 {
		return fmt.Errorf("exact-file routing requires exactly one object, got %d", len(p.Objects))
	}
	seen := map[string]bool{}
	for _, key := range p.Objects {
		if seen[key] {
			return fmt.Errorf("duplicate object key %q", key)
		}
		seen[key] = true
		if err := validateObjectKey(p.Path, key); err != nil {
			return err
		}
	}
	entryDeclared := false
	for _, key := range p.Objects {
		if key == p.EntryPoint {
			entryDeclared = true
			break
		}
	}
	if !entryDeclared {
		return fmt.Errorf("entry point %q is not part of the declared object set", p.EntryPoint)
	}
	return validateProbes(p.Probes)
}

// validateProbes requires at least one public-route probe per Publication so
// adoption always verifies the routes it must preserve.
func validateProbes(probes []RouteProbe) error {
	if len(probes) == 0 {
		return fmt.Errorf("at least one public-route probe is required")
	}
	seen := map[string]bool{}
	for _, probe := range probes {
		if probe.ExpectStatus < 100 || probe.ExpectStatus > 599 {
			return fmt.Errorf("probe path %q: expected status %d is not a valid HTTP status", probe.Path, probe.ExpectStatus)
		}
		if seen[probe.Path] {
			return fmt.Errorf("probe path %q is declared twice", probe.Path)
		}
		seen[probe.Path] = true
		if err := validateProbePath(probe.Path); err != nil {
			return err
		}
	}
	return nil
}

// validateProbePath checks a probe path relative to the Publication path.
// The empty path addresses the Publication path itself; anything else is one
// or more clean segments beneath it.
func validateProbePath(path string) error {
	if path == "" {
		return nil
	}
	if strings.HasPrefix(path, "/") {
		return fmt.Errorf("probe path %q must be relative to the publication path", path)
	}
	if strings.HasSuffix(path, "/") {
		return fmt.Errorf("probe path %q must not end with a slash", path)
	}
	if err := validatePathSegments("probe path", path); err != nil {
		return err
	}
	return validateKeyCharacters("probe path", path)
}

// DisplayName returns the declared display name or a sensible default derived
// from the publication path.
func (p PublicationDeclaration) DisplayNameOr(defaultName string) string {
	if strings.TrimSpace(p.DisplayName) != "" {
		return p.DisplayName
	}
	return defaultName
}

func validatePrefix(prefix string) error {
	if prefix == "" {
		return fmt.Errorf("project prefix must not be empty")
	}
	if strings.EqualFold(prefix, reservedManagerPrefix) {
		return fmt.Errorf("project prefix %q is reserved for the manager", reservedManagerPrefix)
	}
	if prefix == "." || prefix == ".." || strings.HasPrefix(prefix, ".") {
		return fmt.Errorf("project prefix %q must not start with a dot", prefix)
	}
	if strings.ContainsRune(prefix, '/') {
		return fmt.Errorf("project prefix %q must be a single top-level path segment", prefix)
	}
	return validateKeyCharacters("project prefix", prefix)
}

func validatePath(prefix, path string) error {
	if path == prefix {
		return nil
	}
	if !strings.HasPrefix(path, prefix+"/") {
		return fmt.Errorf("publication path %q must be the project prefix %q or a path beneath it", path, prefix)
	}
	if strings.HasSuffix(path, "/") {
		return fmt.Errorf("publication path %q must not end with a slash", path)
	}
	if err := validatePathSegments("publication path", path); err != nil {
		return err
	}
	return validateKeyCharacters("publication path", path)
}

func validateObjectKey(path, key string) error {
	if key == path {
		return nil
	}
	relative, ok := strings.CutPrefix(key, path+"/")
	if !ok || relative == "" {
		return fmt.Errorf("object key %q must be the publication path %q or an object beneath it", key, path)
	}
	if err := validatePathSegments("object key", key); err != nil {
		return err
	}
	return validateKeyCharacters("object key", key)
}

// validatePathSegments rejects empty, '.', and '..' segments in a slash-
// separated path. The what argument names the value in error messages.
func validatePathSegments(what, value string) error {
	for _, segment := range strings.Split(value, "/") {
		switch segment {
		case "", ".", "..":
			return fmt.Errorf("%s %q contains an empty, '.', or '..' segment", what, value)
		}
	}
	return nil
}

// validateKeyCharacters rejects whitespace and characters that make exact
// URL behavior ambiguous. Exact case is always preserved, never folded.
func validateKeyCharacters(what, value string) error {
	for _, char := range value {
		if unicode.IsControl(char) || char == '?' || char == '#' {
			return fmt.Errorf("%s %q contains a control, '?', or '#' character", what, value)
		}
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must not be blank", what)
	}
	return nil
}

// CanonicalURL joins the public base URL with a Publication's public URL.
// The base URL must already be normalized (no trailing slash). An exact-file
// Publication is its entry point's URL: web-share publishes one file as
// <project>/<filename> and readers request the filename, which may differ
// from the managed path and keeps its original casing. Directory-index and
// fallback Publications are addressed by their path.
func CanonicalURL(baseURL, publicationPath, entryPoint string, mode RoutingMode) string {
	if mode == RoutingExactFile && entryPoint != "" {
		return baseURL + "/" + entryPoint
	}
	return baseURL + "/" + publicationPath
}

// ProbeURL joins the Publication's canonical public URL with a relative
// probe path. The empty probe path addresses the Publication path itself.
func ProbeURL(canonicalURL, probePath string) string {
	if probePath == "" {
		return canonicalURL
	}
	return canonicalURL + "/" + probePath
}

// RelativePath returns the object's path inside the Publication.
func RelativePath(publicationPath, key string) string {
	if key == publicationPath {
		if index := strings.LastIndex(key, "/"); index >= 0 {
			return key[index+1:]
		}
		return key
	}
	relative, _ := strings.CutPrefix(key, publicationPath+"/")
	return relative
}
