// Package s3test provides an in-process S3-compatible HTTP server for
// hermetic integration tests of the production storage adapter. It records
// every request so tests can prove that Page Hub issues only reads.
package s3test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Object is one stored object with the exact metadata S3 would return.
type Object struct {
	Content         []byte
	ContentType     string
	ContentEncoding string
	CacheControl    string
	UserMetadata    map[string]string
	ETag            string
	LastModified    time.Time
}

// Request is one recorded storage request.
type Request struct {
	Method string
	Path   string
	Query  string
	Signed bool
}

// Server is an in-process S3-compatible bucket. Only GET and HEAD requests are
// served; every request is recorded.
type Server struct {
	server   *httptest.Server
	bucket   string
	pageSize int

	mu       sync.Mutex
	objects  map[string]Object
	requests []Request
}

// NewServer starts a bucket containing the given objects.
func NewServer(bucket string, objects map[string]Object) *Server {
	s := &Server{bucket: bucket, pageSize: 2, objects: objects}
	s.server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Close shuts the server down.
func (s *Server) Close() { s.server.Close() }

// URL returns the bucket endpoint for the storage adapter configuration.
func (s *Server) URL() string { return s.server.URL }

// Requests returns every request made so far, in order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// AssertOnlyReads fails the test if any request other than a GET or HEAD
// reached the bucket: the proof that a Page Hub workflow never mutates
// storage.
func (s *Server) AssertOnlyReads(t testing.TB) {
	t.Helper()
	s.mu.Lock()
	requests := append([]Request(nil), s.requests...)
	s.mu.Unlock()
	for _, request := range requests {
		switch request.Method {
		case http.MethodGet, http.MethodHead:
		default:
			t.Fatalf("workflow issued %s %s against storage; only reads are allowed", request.Method, request.Path)
		}
	}
}

// SetObject stores or replaces one object (test drift later).
func (s *Server) SetObject(key string, object Object) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = object
}

// RemoveObject deletes one object.
func (s *Server) RemoveObject(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Signed: r.Header.Get("Authorization") != ""})
	s.mu.Unlock()

	if !s.requests[len(s.requests)-1].Signed {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	prefix := "/" + s.bucket
	if r.URL.Path == prefix {
		switch r.Method {
		case http.MethodHead:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			if r.URL.Query().Get("list-type") == "2" {
				s.list(w, r)
				return
			}
			w.WriteHeader(http.StatusBadRequest)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	if !strings.HasPrefix(r.URL.Path, prefix+"/") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, prefix+"/")

	s.mu.Lock()
	object, ok := s.objects[key]
	s.mu.Unlock()
	if !ok {
		writeXMLError(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.")
		return
	}
	if r.Method == http.MethodHead {
		setObjectHeaders(w, object)
		w.Header().Set("Content-Length", fmt.Sprint(len(object.Content)))
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	setObjectHeaders(w, object)
	_, _ = w.Write(object.Content)
}

func setObjectHeaders(w http.ResponseWriter, object Object) {
	etag := object.ETag
	if etag == "" {
		sum := sha256.Sum256(object.Content)
		etag = `"` + hex.EncodeToString(sum[:16]) + `"`
	}
	w.Header().Set("ETag", etag)
	if object.ContentType != "" {
		w.Header().Set("Content-Type", object.ContentType)
	}
	if object.ContentEncoding != "" {
		w.Header().Set("Content-Encoding", object.ContentEncoding)
	}
	if object.CacheControl != "" {
		w.Header().Set("Cache-Control", object.CacheControl)
	}
	if !object.LastModified.IsZero() {
		w.Header().Set("Last-Modified", object.LastModified.UTC().Format(http.TimeFormat))
	}
	for name, value := range object.UserMetadata {
		w.Header().Set("X-Amz-Meta-"+name, value)
	}
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	keys := make([]string, 0, len(s.objects))
	for key := range s.objects {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	token := r.URL.Query().Get("continuation-token")
	start := 0
	if token != "" {
		decoded, err := base64.StdEncoding.DecodeString(token)
		if err != nil {
			s.mu.Unlock()
			writeXMLError(w, http.StatusBadRequest, "InvalidArgument", "Bad continuation token.")
			return
		}
		after := string(decoded)
		for start < len(keys) && keys[start] <= after {
			start++
		}
	}
	end := start + s.pageSize
	truncated := end < len(keys)
	var nextToken string
	if truncated {
		nextToken = base64.StdEncoding.EncodeToString([]byte(keys[end-1]))
	}
	page := keys[start:min(end, len(keys))]

	type entry struct {
		Key          string    `xml:"Key"`
		LastModified time.Time `xml:"LastModified"`
		ETag         string    `xml:"ETag"`
		Size         int       `xml:"Size"`
	}
	result := struct {
		XMLName               xml.Name `xml:"ListBucketResult"`
		IsTruncated           bool     `xml:"IsTruncated"`
		NextContinuationToken string   `xml:"NextContinuationToken"`
		Contents              []entry  `xml:"Contents"`
	}{IsTruncated: truncated, NextContinuationToken: nextToken}
	for _, key := range page {
		object := s.objects[key]
		etag := object.ETag
		if etag == "" {
			sum := sha256.Sum256(object.Content)
			etag = `"` + hex.EncodeToString(sum[:16]) + `"`
		}
		result.Contents = append(result.Contents, entry{Key: key, LastModified: object.LastModified, ETag: etag, Size: len(object.Content)})
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(result)
}

func writeXMLError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	type errorResponse struct {
		XMLName xml.Name `xml:"Error"`
		Code    string   `xml:"Code"`
		Message string   `xml:"Message"`
	}
	_ = xml.NewEncoder(w).Encode(errorResponse{Code: code, Message: message})
}

// HTTPClient returns an HTTP client suitable for talking to this server in
// tests.
func (s *Server) HTTPClient() *http.Client { return s.server.Client() }
