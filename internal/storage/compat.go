package storage

import (
	"context"
	"fmt"
	"sort"
)

// compatDownloadedBodyLimit bounds how many object bodies one compatibility
// check downloads, so the check stays cheap on large buckets while still
// proving that complete body reads work through the configured endpoint.
const compatDownloadedBodyLimit = 8

// CompatibilityReport summarizes one read-only storage compatibility pass.
type CompatibilityReport struct {
	// Objects is the exact number of objects observed in the bucket.
	Objects int `json:"objects"`
	// TotalBytes is the exact observed bucket size.
	TotalBytes int64 `json:"totalBytes"`
	// DownloadedKeys lists, in sorted order, the object keys whose bodies
	// were downloaded completely, with their SHA-256 digests computed.
	DownloadedKeys []string `json:"downloadedKeys"`
}

// CheckCompatibility exercises the complete read path the manager depends
// on: a full bucket listing with pagination, followed by complete body
// downloads with SHA-256 digest computation for a bounded, deterministically
// chosen sample of keys. It issues only reads, never requires a catalog, and
// changes no state, so it is safe to run against a production endpoint as an
// opt-in check.
func CheckCompatibility(ctx context.Context, reader Reader) (CompatibilityReport, error) {
	listings, err := reader.ListObjects(ctx)
	if err != nil {
		return CompatibilityReport{}, fmt.Errorf("list bucket: %w", err)
	}

	report := CompatibilityReport{Objects: len(listings)}
	keys := make([]string, 0, len(listings))
	for _, listing := range listings {
		report.TotalBytes += listing.Size
		keys = append(keys, listing.Key)
	}
	sort.Strings(keys)
	if len(keys) > compatDownloadedBodyLimit {
		keys = keys[:compatDownloadedBodyLimit]
	}

	for _, key := range keys {
		content, err := reader.ReadObject(ctx, key)
		if err != nil {
			return CompatibilityReport{}, fmt.Errorf("read object %q: %w", key, err)
		}
		report.DownloadedKeys = append(report.DownloadedKeys, content.Key)
	}
	return report, nil
}
