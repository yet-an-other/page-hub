package observe_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/yet-an-other/page-hub/internal/catalog"
	"github.com/yet-an-other/page-hub/internal/observe"
	"github.com/yet-an-other/page-hub/internal/storage"
)

// fakeObject is one stored object in the fake reader.
type fakeObject struct {
	content      []byte
	etag         string
	lastModified time.Time
	contentType  string
	encoding     string
	cacheControl string
	userMetadata map[string]string
	statErr      error
	readErr      error
}

// storedObject creates a stored object with stable observation fields.
func storedObject(content string) fakeObject {
	return fakeObject{
		content:      []byte(content),
		etag:         `"etag-x"`,
		lastModified: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		contentType:  "text/html",
	}
}

func (o fakeObject) sha256() string {
	digest := sha256.Sum256(o.content)
	return hex.EncodeToString(digest[:])
}

func (o fakeObject) withETag(etag string) fakeObject {
	o.etag = etag
	return o
}

func (o fakeObject) mutate(mutate func(*fakeObject)) fakeObject {
	mutate(&o)
	return o
}

// manifestFrom derives the accepted manifest of one Publication from stored
// objects, so fixtures are consistent by construction. Tests create drift by
// mutating storage afterwards.
func manifestFrom(publicationID, path string, objects map[string]fakeObject, keys ...string) catalog.PublicationManifest {
	manifest := catalog.PublicationManifest{PublicationID: publicationID, Path: path}
	for _, key := range keys {
		object := objects[key]
		manifest.Objects = append(manifest.Objects, catalog.ManifestObject{
			Key:             key,
			RelativePath:    strings.TrimPrefix(key, path+"/"),
			Size:            int64(len(object.content)),
			ETag:            object.etag,
			ModifiedAt:      object.lastModified.UTC().Format(time.RFC3339),
			ContentType:     object.contentType,
			ContentEncoding: object.encoding,
			CacheControl:    object.cacheControl,
			UserMetadata:    object.userMetadata,
			SHA256:          object.sha256(),
		})
	}
	return manifest
}

// fakeReader is a hermetic storage.Reader that records which objects were
// inspected or downloaded, so tests can prove what an ordinary scan costs.
type fakeReader struct {
	objects map[string]fakeObject
	listErr error

	stats []string
	reads []string
}

func (f *fakeReader) ListObjects(context.Context) ([]storage.ObjectListing, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	keys := make([]string, 0, len(f.objects))
	for key := range f.objects {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	listing := make([]storage.ObjectListing, 0, len(keys))
	for _, key := range keys {
		object := f.objects[key]
		listing = append(listing, storage.ObjectListing{
			Key:          key,
			Size:         int64(len(object.content)),
			ETag:         object.etag,
			LastModified: object.lastModified,
		})
	}
	return listing, nil
}

func (f *fakeReader) ReadObject(_ context.Context, key string) (storage.ObjectContent, error) {
	f.reads = append(f.reads, key)
	object, ok := f.objects[key]
	if !ok {
		return storage.ObjectContent{}, storage.ErrObjectNotFound
	}
	if object.readErr != nil {
		return storage.ObjectContent{}, object.readErr
	}
	return storage.ObjectContent{
		Key:             key,
		Size:            int64(len(object.content)),
		ETag:            object.etag,
		LastModified:    object.lastModified,
		ContentType:     object.contentType,
		ContentEncoding: object.encoding,
		CacheControl:    object.cacheControl,
		UserMetadata:    object.userMetadata,
		Body:            object.content,
		SHA256:          object.sha256(),
	}, nil
}

func (f *fakeReader) StatObject(_ context.Context, key string) (storage.ObjectMeta, error) {
	f.stats = append(f.stats, key)
	object, ok := f.objects[key]
	if !ok {
		return storage.ObjectMeta{}, storage.ErrObjectNotFound
	}
	if object.statErr != nil {
		return storage.ObjectMeta{}, object.statErr
	}
	return storage.ObjectMeta{
		Key:             key,
		Size:            int64(len(object.content)),
		ETag:            object.etag,
		LastModified:    object.lastModified,
		ContentType:     object.contentType,
		ContentEncoding: object.encoding,
		CacheControl:    object.cacheControl,
		UserMetadata:    object.userMetadata,
	}, nil
}

var _ storage.Reader = (*fakeReader)(nil)

// syncFixtures returns a manifest and matching storage for a two-object
// Publication. Tests create drift by mutating the stored objects.
func syncFixtures() (catalog.PublicationManifest, map[string]fakeObject) {
	objects := map[string]fakeObject{
		"guides/index.html": storedObject("<html>guides home</html>").withETag(`"etag-index"`),
		"guides/style.css":  storedObject("body { color: black }").withETag(`"etag-style"`),
	}
	return manifestFrom("publication-1", "guides", objects, "guides/index.html", "guides/style.css"), objects
}

func fakeNow() time.Time { return time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC) }

func observeFixtures(t *testing.T, reader storage.Reader, manifests []catalog.PublicationManifest, options observe.Options) observe.Result {
	t.Helper()
	result, err := observe.Scan(context.Background(), reader, manifests, options)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	return result
}

func publicationResult(t *testing.T, result observe.Result, publicationID string) observe.PublicationResult {
	t.Helper()
	for _, publication := range result.Publications {
		if publication.PublicationID == publicationID {
			return publication
		}
	}
	t.Fatalf("no result for publication %q in %+v", publicationID, result.Publications)
	return observe.PublicationResult{}
}

func TestScanClassifiesInSyncWithoutDownloads(t *testing.T) {
	manifest, objects := syncFixtures()
	reader := &fakeReader{objects: objects}

	result := observeFixtures(t, reader, []catalog.PublicationManifest{manifest}, observe.Options{Now: fakeNow})

	publication := publicationResult(t, result, "publication-1")
	if publication.State != catalog.StateInSync || publication.StatusDetail != "" {
		t.Fatalf("publication = %+v", publication)
	}
	if publication.ObservedSize == nil || *publication.ObservedSize != 45 {
		t.Fatalf("observed size = %v, want 45", publication.ObservedSize)
	}
	if len(reader.reads) != 0 {
		t.Fatalf("an unchanged observation downloaded bodies: %v", reader.reads)
	}
	if result.MutationLock != catalog.LockNone {
		t.Fatalf("mutation lock = %q, want none", result.MutationLock)
	}
	if result.Usage.TotalBytes != 45 || result.Usage.AcceptedBytes != 45 || result.Usage.UnclaimedBytes != 0 {
		t.Fatalf("usage = %+v", result.Usage)
	}
	if len(result.UnclaimedKeys) != 0 {
		t.Fatalf("unclaimed = %v", result.UnclaimedKeys)
	}
	if !result.ObservedAt.Equal(fakeNow()) {
		t.Fatalf("observed at = %v, want the scan time", result.ObservedAt)
	}
}

func TestScanDetectsChangedBytesThroughBodyVerification(t *testing.T) {
	manifest, objects := syncFixtures()
	// An out-of-band writer replaced the body: listing fields change too.
	objects["guides/index.html"] = objects["guides/index.html"].mutate(func(object *fakeObject) {
		object.content = []byte("<html>replaced home</html>")
		object.etag = `"etag-new"`
		object.lastModified = time.Date(2026, 3, 3, 3, 4, 5, 0, time.UTC)
	})
	reader := &fakeReader{objects: objects}

	result := observeFixtures(t, reader, []catalog.PublicationManifest{manifest}, observe.Options{Now: fakeNow})

	publication := publicationResult(t, result, "publication-1")
	if publication.State != catalog.StateDrifted {
		t.Fatalf("state = %q, want drifted", publication.State)
	}
	if publication.ObservedSize == nil || *publication.ObservedSize != 47 {
		t.Fatalf("observed size = %v, want 47", publication.ObservedSize)
	}
	for _, fragment := range []string{"guides/index.html", "size changed (24 → 26 B)", "ETag changed", "modification time changed", "body verified different"} {
		if !strings.Contains(publication.StatusDetail, fragment) {
			t.Fatalf("status detail %q misses %q", publication.StatusDetail, fragment)
		}
	}
	if len(reader.reads) != 1 || reader.reads[0] != "guides/index.html" {
		t.Fatalf("reads = %v, want only the differing object", reader.reads)
	}
	if result.MutationLock != catalog.LockGlobal {
		t.Fatalf("mutation lock = %q, want global", result.MutationLock)
	}
}

func TestScanDetectsMetadataOnlyDriftAndVerifiesBytes(t *testing.T) {
	manifest, objects := syncFixtures()
	objects["guides/style.css"] = objects["guides/style.css"].mutate(func(object *fakeObject) {
		object.contentType = "text/plain"
		object.userMetadata = map[string]string{"owner": "someone"}
	})
	reader := &fakeReader{objects: objects}

	result := observeFixtures(t, reader, []catalog.PublicationManifest{manifest}, observe.Options{Now: fakeNow})

	publication := publicationResult(t, result, "publication-1")
	if publication.State != catalog.StateDrifted {
		t.Fatalf("state = %q, want drifted", publication.State)
	}
	for _, fragment := range []string{"guides/style.css", "content type changed (text/html → text/plain)", "user metadata changed", "body verified unchanged"} {
		if !strings.Contains(publication.StatusDetail, fragment) {
			t.Fatalf("status detail %q misses %q", publication.StatusDetail, fragment)
		}
	}
	// The unchanged object was inspected but not downloaded.
	if len(reader.reads) != 1 || reader.reads[0] != "guides/style.css" {
		t.Fatalf("reads = %v, want only the differing object", reader.reads)
	}
}

func TestScanClassifiesMissingPublicationAndPartialDrift(t *testing.T) {
	manifest, objects := syncFixtures()
	delete(objects, "guides/index.html")
	delete(objects, "guides/style.css")
	reader := &fakeReader{objects: objects}

	result := observeFixtures(t, reader, []catalog.PublicationManifest{manifest}, observe.Options{Now: fakeNow})

	publication := publicationResult(t, result, "publication-1")
	if publication.State != catalog.StateMissing {
		t.Fatalf("state = %q, want missing", publication.State)
	}
	if publication.ObservedSize != nil {
		t.Fatalf("missing publication observed size = %v, want none", publication.ObservedSize)
	}
	if publication.StatusDetail != "all 2 accepted object(s) are absent from storage" {
		t.Fatalf("status detail = %q", publication.StatusDetail)
	}

	// One object returning: partial drift, not missing.
	objects["guides/style.css"] = storedObject("body { color: black }").withETag(`"etag-style"`)
	result = observeFixtures(t, reader, []catalog.PublicationManifest{manifest}, observe.Options{Now: fakeNow})
	publication = publicationResult(t, result, "publication-1")
	if publication.State != catalog.StateDrifted {
		t.Fatalf("state = %q, want drifted", publication.State)
	}
	if !strings.Contains(publication.StatusDetail, "guides/index.html: absent from storage") {
		t.Fatalf("status detail = %q", publication.StatusDetail)
	}
	if publication.ObservedSize == nil || *publication.ObservedSize != 21 {
		t.Fatalf("observed size = %v, want the one present object", publication.ObservedSize)
	}
}

func TestScanKeepsUnexpectedObjectsUnclaimed(t *testing.T) {
	manifest, objects := syncFixtures()
	objects["stray/abandoned.html"] = storedObject("<html>abandoned</html>").withETag(`"etag-stray"`)
	objects["guides/index.html.bak"] = storedObject("old backup").withETag(`"etag-bak"`)
	// Near a Publication boundary but still nobody's object.
	objects["guides/extra/notes.txt"] = storedObject("nearby but unclaimed").withETag(`"etag-near"`)
	reader := &fakeReader{objects: objects}

	result := observeFixtures(t, reader, []catalog.PublicationManifest{manifest}, observe.Options{Now: fakeNow})

	publication := publicationResult(t, result, "publication-1")
	if publication.State != catalog.StateInSync {
		t.Fatalf("state = %q, unexpected objects must not drift their neighbours", publication.State)
	}
	want := []string{"guides/extra/notes.txt", "guides/index.html.bak", "stray/abandoned.html"}
	if strings.Join(result.UnclaimedKeys, ",") != strings.Join(want, ",") {
		t.Fatalf("unclaimed = %v, want %v", result.UnclaimedKeys, want)
	}
	unclaimed := int64(len("<html>abandoned</html>") + len("old backup") + len("nearby but unclaimed"))
	if result.Usage.TotalBytes != 45+unclaimed {
		t.Fatalf("total usage = %d, want %d", result.Usage.TotalBytes, 43+unclaimed)
	}
	if result.Usage.AcceptedBytes != 45 || result.Usage.UnclaimedBytes != unclaimed {
		t.Fatalf("usage = %+v", result.Usage)
	}
	if result.MutationLock != catalog.LockGlobal {
		t.Fatalf("mutation lock = %q, want global while findings are unclassified", result.MutationLock)
	}
}

func TestScanCompleteReconciliationVerifiesEveryBody(t *testing.T) {
	manifest, objects := syncFixtures()
	reader := &fakeReader{objects: objects}

	result := observeFixtures(t, reader, []catalog.PublicationManifest{manifest}, observe.Options{Now: fakeNow, Complete: true})

	publication := publicationResult(t, result, "publication-1")
	if publication.State != catalog.StateInSync {
		t.Fatalf("state = %q, want in sync", publication.State)
	}
	if len(reader.reads) != 2 {
		t.Fatalf("reads = %v, want every accepted object verified", reader.reads)
	}
}

func TestScanRepresentativeFixtures(t *testing.T) {
	// A multi-object fallback Publication, an uppercase exact-file entry
	// point, and a single-object exact-file Publication.
	objects := map[string]fakeObject{
		"guides/index.html":    storedObject("<html>guides home</html>").withETag(`"etag-g"`),
		"guides/assets/app.js": storedObject("console.log('ok')").withETag(`"etag-a"`).mutate(func(o *fakeObject) { o.contentType = "text/javascript" }),
		"reports/Index.html":   storedObject("<html>Annual report</html>").withETag(`"etag-r"`),
		"docs":                 storedObject("Annual report body").withETag(`"etag-d"`).mutate(func(o *fakeObject) { o.contentType = "text/plain" }),
	}
	manifests := []catalog.PublicationManifest{
		manifestFrom("guides-id", "guides", objects, "guides/index.html", "guides/assets/app.js"),
		manifestFrom("reports-id", "reports", objects, "reports/Index.html"),
		manifestFrom("docs-id", "docs", objects, "docs"),
	}
	reader := &fakeReader{objects: objects}

	result := observeFixtures(t, reader, manifests, observe.Options{Now: fakeNow})

	for _, id := range []string{"guides-id", "reports-id", "docs-id"} {
		if got := publicationResult(t, result, id).State; got != catalog.StateInSync {
			t.Fatalf("%s state = %q, want in sync", id, got)
		}
	}
	if result.Usage.TotalBytes != 85 {
		t.Fatalf("total usage = %d, want 85", result.Usage.TotalBytes)
	}
}

func TestScanModificationTimeUsesSecondTolerance(t *testing.T) {
	manifest, objects := syncFixtures()
	// Sub-second rounding must not create drift.
	objects["guides/index.html"] = objects["guides/index.html"].mutate(func(object *fakeObject) {
		object.lastModified = time.Date(2026, 1, 2, 3, 4, 5, 600_000_000, time.UTC)
	})
	reader := &fakeReader{objects: objects}
	result := observeFixtures(t, reader, []catalog.PublicationManifest{manifest}, observe.Options{Now: fakeNow})
	if got := publicationResult(t, result, "publication-1").State; got != catalog.StateInSync {
		t.Fatalf("state = %q, want sub-second rounding tolerated", got)
	}

	// A real change does create drift.
	objects["guides/style.css"] = objects["guides/style.css"].mutate(func(object *fakeObject) {
		object.lastModified = time.Date(2026, 1, 2, 3, 4, 7, 0, time.UTC)
	})
	result = observeFixtures(t, reader, []catalog.PublicationManifest{manifest}, observe.Options{Now: fakeNow})
	publication := publicationResult(t, result, "publication-1")
	if publication.State != catalog.StateDrifted || !strings.Contains(publication.StatusDetail, "modification time changed") {
		t.Fatalf("publication = %+v", publication)
	}
}

func TestScanStatusDetailIsDeterministicAndBounded(t *testing.T) {
	objects := map[string]fakeObject{}
	var keys []string
	for index := 0; index < 8; index++ {
		key := fmt.Sprintf("big/file-%02d.html", index)
		objects[key] = storedObject(strings.Repeat("x", 12)).withETag(`"etag-` + key + `"`)
		keys = append(keys, key)
	}
	manifest := manifestFrom("pub", "big", objects, keys...)
	for index := 0; index < 8; index++ {
		key := fmt.Sprintf("big/file-%02d.html", index)
		objects[key] = objects[key].withETag(`"etag-drift"`)
	}
	reader := &fakeReader{objects: objects}

	first := observeFixtures(t, reader, []catalog.PublicationManifest{manifest}, observe.Options{Now: fakeNow})
	second := observeFixtures(t, reader, []catalog.PublicationManifest{manifest}, observe.Options{Now: fakeNow})

	detail := publicationResult(t, first, "pub").StatusDetail
	if detail != publicationResult(t, second, "pub").StatusDetail {
		t.Fatalf("status detail is not deterministic: %q vs %q", detail, publicationResult(t, second, "pub").StatusDetail)
	}
	if strings.Count(detail, "big/file-") != 5 || !strings.Contains(detail, "3 more object(s) differ") {
		t.Fatalf("status detail = %q, want five listed differences plus a bounded remainder", detail)
	}
}

func TestScanFailsClosedWhenStorageIsUnavailable(t *testing.T) {
	manifest, _ := syncFixtures()
	reader := &fakeReader{objects: map[string]fakeObject{}, listErr: storage.ErrStorageUnavailable}
	if _, err := observe.Scan(context.Background(), reader, []catalog.PublicationManifest{manifest}, observe.Options{}); !errors.Is(err, storage.ErrStorageUnavailable) {
		t.Fatalf("listing error = %v, want ErrStorageUnavailable", err)
	}

	_, objects := syncFixtures()
	objects["guides/index.html"] = objects["guides/index.html"].mutate(func(object *fakeObject) {
		object.statErr = storage.ErrStorageUnavailable
	})
	failing := &fakeReader{objects: objects}
	if _, err := observe.Scan(context.Background(), failing, []catalog.PublicationManifest{manifest}, observe.Options{}); !errors.Is(err, storage.ErrStorageUnavailable) {
		t.Fatalf("stat error = %v, want ErrStorageUnavailable", err)
	}
}

func TestScanWithEmptyCatalogObservesOnlyUnclaimedStorage(t *testing.T) {
	objects := map[string]fakeObject{
		"stray/one.html": storedObject("<html>one</html>").withETag(`"etag-one"`),
	}
	reader := &fakeReader{objects: objects}

	result := observeFixtures(t, reader, nil, observe.Options{Now: fakeNow})

	if len(result.Publications) != 0 {
		t.Fatalf("publications = %+v, want none", result.Publications)
	}
	if len(result.UnclaimedKeys) != 1 || result.Usage.TotalBytes != 16 || result.Usage.AcceptedBytes != 0 {
		t.Fatalf("result = %+v", result)
	}
	if result.MutationLock != catalog.LockGlobal {
		t.Fatalf("mutation lock = %q, want global while findings are unclassified", result.MutationLock)
	}
}
