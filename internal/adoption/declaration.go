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

// PublicationDeclaration declares one adoption candidate: the Project it
// belongs to, its path, entry point, routing behavior, and exact object set.
// Page Hub never infers Publication boundaries from storage.
type PublicationDeclaration struct {
	Project     ProjectDeclaration `json:"project"`
	Path        string             `json:"path"`
	DisplayName string             `json:"displayName,omitempty"`
	EntryPoint  string             `json:"entryPoint"`
	RoutingMode RoutingMode        `json:"routingMode"`
	Objects     []string           `json:"objects"`
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
	}
	return nil
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
	return nil
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
	for _, segment := range strings.Split(path, "/") {
		switch segment {
		case "", ".", "..":
			return fmt.Errorf("publication path %q contains an empty, '.', or '..' segment", path)
		}
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
	for _, segment := range strings.Split(relative, "/") {
		switch segment {
		case "", ".", "..":
			return fmt.Errorf("object key %q contains an empty, '.', or '..' segment", key)
		}
	}
	return validateKeyCharacters("object key", key)
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
