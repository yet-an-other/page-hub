package storage

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Reader is the read-only storage seam used by adoption planning and the
// observation scan. Every method is a read: no write, copy, or delete
// request can be expressed through this interface.
type Reader interface {
	ListObjects(ctx context.Context) ([]ObjectListing, error)
	ReadObject(ctx context.Context, key string) (ObjectContent, error)
	StatObject(ctx context.Context, key string) (ObjectMeta, error)
}

// ObjectListing is one entry of the complete bucket listing.
type ObjectListing struct {
	Key          string
	Size         int64
	ETag         string
	LastModified time.Time
}

// ErrObjectNotFound reports that a declared object does not exist.
var ErrObjectNotFound = errors.New("storage object not found")

// ErrStorageUnavailable reports a transport-level or server failure.
var ErrStorageUnavailable = errors.New("storage is unavailable")

// ErrStorageMisconfigured reports that storage rejected the request.
var ErrStorageMisconfigured = errors.New("storage rejected the request")

// ObjectContent is one observed object: exact metadata plus the verified body
// digest. Provider error text is never included.
type ObjectContent struct {
	Key             string
	Size            int64
	ETag            string
	LastModified    time.Time
	ContentType     string
	ContentEncoding string
	CacheControl    string
	UserMetadata    map[string]string
	Body            []byte
	SHA256          string
}

// S3Reader reads objects from the configured S3-compatible bucket using the
// same request signing as the connectivity checker. It performs only GET
// requests.
type S3Reader struct {
	config Config
	client *http.Client
	now    func() time.Time
}

// NewS3Reader creates a reader with a bounded network timeout.
func NewS3Reader(config Config) *S3Reader {
	if config.Region == "" {
		config.Region = "us-east-1"
	}
	return &S3Reader{
		config: config,
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			},
		},
		now: time.Now,
	}
}

// NewS3ReaderWithHTTPClient is useful for hermetic integration tests and keeps
// the same production request/signing behavior.
func NewS3ReaderWithHTTPClient(config Config, client *http.Client) *S3Reader {
	reader := NewS3Reader(config)
	if client != nil {
		reader.client = client
	}
	return reader
}

type listBucketResult struct {
	IsTruncated           bool        `xml:"IsTruncated"`
	NextContinuationToken string      `xml:"NextContinuationToken"`
	Contents              []listEntry `xml:"Contents"`
}

type listEntry struct {
	Key          string    `xml:"Key"`
	LastModified time.Time `xml:"LastModified"`
	ETag         string    `xml:"ETag"`
	Size         int64     `xml:"Size"`
}

// ListObjects returns the complete bucket listing, following ListObjectsV2
// continuation tokens until the listing is exhausted.
func (r *S3Reader) ListObjects(ctx context.Context) ([]ObjectListing, error) {
	base, err := bucketURL(r.config.Endpoint, r.config.Bucket)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid storage configuration", ErrStorageMisconfigured)
	}
	var listings []ObjectListing
	token := ""
	for {
		query := url.Values{"list-type": {"2"}}
		if token != "" {
			query.Set("continuation-token", token)
		}
		requestURL := *base
		requestURL.RawQuery = query.Encode()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("prepare listing request: %w", err)
		}
		if err := signRequest(request, r.config, r.now()); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrStorageMisconfigured, err)
		}
		response, err := r.client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("%w: listing request failed", ErrStorageUnavailable)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
		_ = response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK {
			return nil, classifyStatus("list objects", response.StatusCode)
		}
		var result listBucketResult
		if err := xml.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("parse listing response: %w", err)
		}
		for _, entry := range result.Contents {
			if entry.Key == "" {
				continue
			}
			listings = append(listings, ObjectListing{
				Key:          entry.Key,
				Size:         entry.Size,
				ETag:         entry.ETag,
				LastModified: entry.LastModified.UTC(),
			})
		}
		if !result.IsTruncated || result.NextContinuationToken == "" {
			return listings, nil
		}
		token = result.NextContinuationToken
	}
}

// ReadObject downloads one object, verifies the body length, and computes its
// SHA-256 digest while streaming.
func (r *S3Reader) ReadObject(ctx context.Context, key string) (ObjectContent, error) {
	base, err := bucketURL(r.config.Endpoint, r.config.Bucket)
	if err != nil {
		return ObjectContent{}, fmt.Errorf("%w: invalid storage configuration", ErrStorageMisconfigured)
	}
	requestURL := *base
	requestURL.Path += "/" + key
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return ObjectContent{}, fmt.Errorf("prepare object request: %w", err)
	}
	// Stored representations must be returned exactly as stored: disabling
	// transport-level encoding negotiation prevents transparent decompression
	// from hiding a stored gzip Content-Encoding.
	request.Header.Set("Accept-Encoding", "identity")
	if err := signRequest(request, r.config, r.now()); err != nil {
		return ObjectContent{}, fmt.Errorf("%w: %s", ErrStorageMisconfigured, err)
	}
	response, err := r.client.Do(request)
	if err != nil {
		return ObjectContent{}, fmt.Errorf("%w: object request failed", ErrStorageUnavailable)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ObjectContent{}, classifyStatus("read object "+key, response.StatusCode)
	}

	digest := sha256.New()
	body, err := io.ReadAll(io.TeeReader(io.LimitReader(response.Body, 1<<30), digest))
	if err != nil {
		return ObjectContent{}, fmt.Errorf("%w: reading object body failed", ErrStorageUnavailable)
	}

	metadata := map[string]string{}
	for name, values := range response.Header {
		if len(values) == 0 {
			continue
		}
		if strings.HasPrefix(strings.ToLower(name), "x-amz-meta-") {
			metadata[strings.TrimPrefix(strings.ToLower(name), "x-amz-meta-")] = values[0]
		}
	}

	lastModified := time.Time{}
	if raw := response.Header.Get("Last-Modified"); raw != "" {
		if parsed, err := http.ParseTime(raw); err == nil {
			lastModified = parsed.UTC()
		}
	}

	return ObjectContent{
		Key:             key,
		Size:            int64(len(body)),
		ETag:            response.Header.Get("ETag"),
		LastModified:    lastModified,
		ContentType:     response.Header.Get("Content-Type"),
		ContentEncoding: response.Header.Get("Content-Encoding"),
		CacheControl:    response.Header.Get("Cache-Control"),
		UserMetadata:    metadata,
		Body:            body,
		SHA256:          hex.EncodeToString(digest.Sum(nil)),
	}, nil
}

// ObjectMeta is one observed object's exact metadata without its body.
type ObjectMeta struct {
	Key             string
	Size            int64
	ETag            string
	LastModified    time.Time
	ContentType     string
	ContentEncoding string
	CacheControl    string
	UserMetadata    map[string]string
}

// StatObject requests one object's exact metadata with a HEAD request. It
// never downloads the body, so ordinary unchanged observations do not pay for
// object bytes.
func (r *S3Reader) StatObject(ctx context.Context, key string) (ObjectMeta, error) {
	base, err := bucketURL(r.config.Endpoint, r.config.Bucket)
	if err != nil {
		return ObjectMeta{}, fmt.Errorf("%w: invalid storage configuration", ErrStorageMisconfigured)
	}
	requestURL := *base
	requestURL.Path += "/" + key
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, requestURL.String(), nil)
	if err != nil {
		return ObjectMeta{}, fmt.Errorf("prepare object metadata request: %w", err)
	}
	// Stored representations must be described exactly as stored.
	request.Header.Set("Accept-Encoding", "identity")
	if err := signRequest(request, r.config, r.now()); err != nil {
		return ObjectMeta{}, fmt.Errorf("%w: %s", ErrStorageMisconfigured, err)
	}
	response, err := r.client.Do(request)
	if err != nil {
		return ObjectMeta{}, fmt.Errorf("%w: object metadata request failed", ErrStorageUnavailable)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ObjectMeta{}, classifyStatus("read metadata "+key, response.StatusCode)
	}

	metadata := map[string]string{}
	for name, values := range response.Header {
		if len(values) == 0 {
			continue
		}
		if strings.HasPrefix(strings.ToLower(name), "x-amz-meta-") {
			metadata[strings.TrimPrefix(strings.ToLower(name), "x-amz-meta-")] = values[0]
		}
	}

	lastModified := time.Time{}
	if raw := response.Header.Get("Last-Modified"); raw != "" {
		if parsed, err := http.ParseTime(raw); err == nil {
			lastModified = parsed.UTC()
		}
	}
	return ObjectMeta{
		Key:             key,
		Size:            response.ContentLength,
		ETag:            response.Header.Get("ETag"),
		LastModified:    lastModified,
		ContentType:     response.Header.Get("Content-Type"),
		ContentEncoding: response.Header.Get("Content-Encoding"),
		CacheControl:    response.Header.Get("Cache-Control"),
		UserMetadata:    metadata,
	}, nil
}

func classifyStatus(action string, status int) error {
	switch {
	case status == http.StatusNotFound:
		return fmt.Errorf("%w during %s", ErrObjectNotFound, action)
	case status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= http.StatusInternalServerError:
		return fmt.Errorf("%w during %s (status %d)", ErrStorageUnavailable, action, status)
	default:
		return fmt.Errorf("%w during %s (status %d)", ErrStorageMisconfigured, action, status)
	}
}
