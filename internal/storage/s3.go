// Package storage contains the server-side, read-only storage boundary used
// by the manager. It deliberately exposes a coarse status rather than storage
// objects, object keys, or provider error bodies.
package storage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// ConnectivityStatus is the only storage information exposed by the manager
// status API.
type ConnectivityStatus string

const (
	StatusReachable     ConnectivityStatus = "reachable"
	StatusUnavailable   ConnectivityStatus = "unavailable"
	StatusMisconfigured ConnectivityStatus = "misconfigured"
)

// CheckResult intentionally contains no provider message. Provider responses
// may contain credentials, object names, or other private information.
type CheckResult struct {
	Status ConnectivityStatus
}

// Checker is the public seam between the HTTP manager and object storage.
type Checker interface {
	Check(context.Context) CheckResult
}

// Config describes the minimum S3-compatible bucket connection.
type Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// S3Checker performs a read-only HeadBucket request with the configured
// bucket-scoped credentials. Requests are signed with AWS Signature Version 4.
type S3Checker struct {
	config Config
	client *http.Client
	now    func() time.Time
}

// NewS3Checker creates a checker with a bounded network timeout.
func NewS3Checker(config Config) *S3Checker {
	if config.Region == "" {
		config.Region = "us-east-1"
	}
	return &S3Checker{
		config: config,
		client: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			},
		},
		now: time.Now,
	}
}

// NewS3CheckerWithHTTPClient is useful for hermetic integration tests and
// keeps the same production request/signing behavior.
func NewS3CheckerWithHTTPClient(config Config, client *http.Client) *S3Checker {
	checker := NewS3Checker(config)
	if client != nil {
		checker.client = client
	}
	return checker
}

// Check returns a coarse connectivity classification. It never returns a
// provider response body or error text to callers.
func (c *S3Checker) Check(ctx context.Context) CheckResult {
	requestURL, err := bucketURL(c.config.Endpoint, c.config.Bucket)
	if err != nil || c.config.AccessKeyID == "" || c.config.SecretAccessKey == "" {
		return CheckResult{Status: StatusMisconfigured}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, requestURL.String(), nil)
	if err != nil {
		return CheckResult{Status: StatusMisconfigured}
	}
	if err := signRequest(req, c.config, c.now()); err != nil {
		return CheckResult{Status: StatusMisconfigured}
	}

	response, err := c.client.Do(req)
	if err != nil {
		return CheckResult{Status: StatusUnavailable}
	}
	defer response.Body.Close()

	switch {
	case response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices:
		return CheckResult{Status: StatusReachable}
	case response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError:
		return CheckResult{Status: StatusUnavailable}
	default:
		return CheckResult{Status: StatusMisconfigured}
	}
}

func bucketURL(endpoint, bucket string) (*url.URL, error) {
	if strings.TrimSpace(endpoint) == "" || strings.TrimSpace(bucket) == "" || strings.ContainsAny(bucket, "/\\\r\n\t") || strings.IndexFunc(bucket, func(char rune) bool { return unicode.IsSpace(char) || unicode.IsControl(char) }) >= 0 {
		return nil, fmt.Errorf("invalid storage settings")
	}
	parsed, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("invalid storage endpoint")
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	parsed.Path = basePath + "/" + bucket
	parsed.RawPath = ""
	return parsed, nil
}

func signRequest(request *http.Request, config Config, now time.Time) error {
	if config.AccessKeyID == "" || config.SecretAccessKey == "" {
		return fmt.Errorf("missing credentials")
	}
	host := request.Host
	if host == "" {
		host = request.URL.Host
	}
	request.Host = host
	request.Header.Set("X-Amz-Content-Sha256", emptyPayloadHash)
	request.Header.Set("X-Amz-Date", now.UTC().Format("20060102T150405Z"))
	if config.SessionToken != "" {
		request.Header.Set("X-Amz-Security-Token", config.SessionToken)
	}

	canonicalHeaders, signedHeaders := canonicalHeaders(request)
	canonicalRequest := strings.Join([]string{
		request.Method,
		request.URL.EscapedPath(),
		canonicalQuery(request.URL.Query()),
		canonicalHeaders,
		signedHeaders,
		emptyPayloadHash,
	}, "\n")

	amzDate := request.Header.Get("X-Amz-Date")
	shortDate := amzDate[:8]
	scope := shortDate + "/" + config.Region + "/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		digestHex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := hmacSHA256(
		hmacSHA256(
			hmacSHA256(
				hmacSHA256([]byte("AWS4"+config.SecretAccessKey), []byte(shortDate)),
				[]byte(config.Region)),
			[]byte("s3")),
		[]byte("aws4_request"),
	)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))
	request.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+config.AccessKeyID+"/"+scope+", SignedHeaders="+signedHeaders+", Signature="+signature)
	return nil
}

const emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func canonicalHeaders(request *http.Request) (string, string) {
	values := map[string]string{
		"host":                 request.Host,
		"x-amz-content-sha256": request.Header.Get("X-Amz-Content-Sha256"),
		"x-amz-date":           request.Header.Get("X-Amz-Date"),
	}
	if token := request.Header.Get("X-Amz-Security-Token"); token != "" {
		values["x-amz-security-token"] = strings.TrimSpace(token)
	}

	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	// There are only four possible names. Sorting manually keeps this package
	// small and deterministic without pulling an SDK into the binary.
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}

	var canonical strings.Builder
	for _, name := range names {
		canonical.WriteString(name)
		canonical.WriteByte(':')
		canonical.WriteString(strings.Join(strings.Fields(values[name]), " "))
		canonical.WriteByte('\n')
	}
	return canonical.String(), strings.Join(names, ";")
}

func canonicalQuery(query url.Values) string {
	if len(query) == 0 {
		return ""
	}
	return query.Encode()
}

func digestHex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func hmacSHA256(key, value []byte) []byte {
	hash := hmac.New(sha256.New, key)
	_, _ = hash.Write(value)
	return hash.Sum(nil)
}
