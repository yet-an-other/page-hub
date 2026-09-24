package observe_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yet-an-other/page-hub/internal/catalog"
	"github.com/yet-an-other/page-hub/internal/observe"
	"github.com/yet-an-other/page-hub/internal/storage"
)

// gatingReader counts scan starts and can block ListObjects until a test
// releases it, making slow and overlapping scans deterministic.
type gatingReader struct {
	started atomic.Int32
	release chan struct{}
	fail    atomic.Bool

	wake chan struct{} // closed once a scan has entered ListObjects
	once sync.Once
}

func newGatingReader() *gatingReader {
	return &gatingReader{release: make(chan struct{}, 4), wake: make(chan struct{})}
}

func (g *gatingReader) ListObjects(ctx context.Context) ([]storage.ObjectListing, error) {
	g.started.Add(1)
	g.once.Do(func() { close(g.wake) })
	if g.fail.Load() {
		return nil, storage.ErrStorageUnavailable
	}
	select {
	case <-g.release:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (g *gatingReader) ReadObject(_ context.Context, _ string) (storage.ObjectContent, error) {
	return storage.ObjectContent{}, storage.ErrObjectNotFound
}

func (g *gatingReader) StatObject(_ context.Context, _ string) (storage.ObjectMeta, error) {
	return storage.ObjectMeta{}, storage.ErrObjectNotFound
}

var _ storage.Reader = (*gatingReader)(nil)

func serviceStore(t *testing.T) *catalog.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.db")
	if _, _, err := catalog.Migrate(path); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	store, err := catalog.OpenRuntime(path)
	if err != nil {
		t.Fatalf("OpenRuntime() error = %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func testService(reader storage.Reader, store *catalog.Store) *observe.Service {
	return observe.NewService(reader, store, observe.ServiceOptions{
		Now:           fakeNow,
		DailyInterval: time.Hour,
		RetryDelay:    time.Hour,
	})
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition was not met in time")
}

func TestRefreshStartsOneScanAndJoinsConcurrentTriggers(t *testing.T) {
	reader := newGatingReader()
	store := serviceStore(t)
	service := testService(reader, store)

	firstDone := make(chan struct{})
	secondDone := make(chan struct{})
	go func() {
		if outcome := service.Refresh(context.Background()); outcome != observe.OutcomeSucceeded {
			t.Errorf("first outcome = %q", outcome)
		}
		close(firstDone)
	}()

	<-reader.wake // the first scan is in flight
	if running, _ := service.State(); !running {
		t.Fatal("service does not report a running scan")
	}

	go func() {
		if outcome := service.Refresh(context.Background()); outcome != observe.OutcomeSucceeded {
			t.Errorf("second outcome = %q", outcome)
		}
		close(secondDone)
	}()

	// Give the joiner a moment: it must wait for the running scan instead of
	// starting (or queueing) another one.
	time.Sleep(20 * time.Millisecond)
	if got := reader.started.Load(); got != 1 {
		t.Fatalf("scan starts = %d, want exactly 1", got)
	}

	close(reader.release)
	<-firstDone
	<-secondDone
	if got := reader.started.Load(); got != 1 {
		t.Fatalf("scan starts after release = %d, want exactly 1", got)
	}
	if running, _ := service.State(); running {
		t.Fatal("service still reports a running scan after completion")
	}
}

func TestJoinerSharesTheRunningScanOutcome(t *testing.T) {
	reader := newGatingReader()
	reader.fail.Store(true)
	store := serviceStore(t)
	service := testService(reader, store)

	firstDone := make(chan observe.Outcome)
	go func() { firstDone <- service.Refresh(context.Background()) }()
	<-reader.wake

	secondDone := make(chan observe.Outcome)
	go func() { secondDone <- service.Refresh(context.Background()) }()

	close(reader.release)
	if outcome := <-firstDone; outcome != observe.OutcomeUnavailable {
		t.Fatalf("first outcome = %q, want unavailable", outcome)
	}
	if outcome := <-secondDone; outcome != observe.OutcomeUnavailable {
		t.Fatalf("joined outcome = %q, want the running scan's outcome", outcome)
	}
	if _, last := service.State(); last != observe.OutcomeUnavailable {
		t.Fatalf("last outcome = %q, want unavailable", last)
	}
}

func TestStartRetriesFailedScansThenRecoversOnDailyCadence(t *testing.T) {
	reader := newGatingReader()
	reader.fail.Store(true)
	reader.release <- struct{}{} // scans never block
	store := serviceStore(t)
	service := observe.NewService(reader, store, observe.ServiceOptions{
		Now:           fakeNow,
		DailyInterval: time.Hour,
		RetryDelay:    5 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.Start(ctx)

	// Storage failure: the service keeps retrying reconciliation.
	waitFor(t, time.Second, func() bool { return reader.started.Load() >= 3 })
	if _, last := service.State(); last != observe.OutcomeUnavailable {
		t.Fatalf("outcome = %q, want unavailable", last)
	}

	// Recovery: the next attempt succeeds and the cadence slows to daily.
	reader.fail.Store(false)
	waitFor(t, time.Second, func() bool {
		_, last := service.State()
		return last == observe.OutcomeSucceeded
	})

	// A daily cadence of one hour means no further scans during the test.
	giveUp := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(giveUp) {
		if got := reader.started.Load(); got > 4 {
			t.Fatalf("scan starts = %d, want no more than the recovery scan", got)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Shutdown stops the loop.
	cancel()
	stable := reader.started.Load()
	time.Sleep(30 * time.Millisecond)
	if got := reader.started.Load(); got != stable {
		t.Fatalf("scan starts grew from %d to %d after shutdown", stable, got)
	}
}

func TestSuccessfulServiceRecordsObservationInCatalog(t *testing.T) {
	reader := newGatingReader()
	reader.release <- struct{}{}
	store := serviceStore(t)
	service := testService(reader, store)

	if outcome := service.Refresh(context.Background()); outcome != observe.OutcomeSucceeded {
		t.Fatalf("outcome = %q, want succeeded", outcome)
	}
	if _, ok, err := store.LatestObservation(context.Background()); err != nil || !ok {
		t.Fatalf("LatestObservation() = ok %v err %v, want a recorded observation", ok, err)
	}
}

func TestRefreshReportsFailedWhenCatalogUnavailable(t *testing.T) {
	reader := newGatingReader()
	reader.release <- struct{}{}
	store := serviceStore(t)
	service := testService(reader, store)

	if outcome := service.Refresh(context.Background()); outcome != observe.OutcomeSucceeded {
		t.Fatalf("outcome = %q, want succeeded", outcome)
	}

	// A catalog outage fails the scan closed without substituting values.
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if outcome := service.Refresh(context.Background()); outcome != observe.OutcomeFailed {
		t.Fatalf("outcome = %q, want failed", outcome)
	}
	if _, last := service.State(); last != observe.OutcomeFailed {
		t.Fatalf("last outcome = %q, want failed", last)
	}
}

func TestOutcomeOfClassification(t *testing.T) {
	if got := observe.OutcomeOf(nil); got != observe.OutcomeSucceeded {
		t.Fatalf("OutcomeOf(nil) = %q", got)
	}
	if got := observe.OutcomeOf(storage.ErrStorageUnavailable); got != observe.OutcomeUnavailable {
		t.Fatalf("OutcomeOf(unavailable) = %q", got)
	}
	if got := observe.OutcomeOf(storage.ErrStorageMisconfigured); got != observe.OutcomeMisconfigured {
		t.Fatalf("OutcomeOf(misconfigured) = %q", got)
	}
	if got := observe.OutcomeOf(errors.New("boom")); got != observe.OutcomeFailed {
		t.Fatalf("OutcomeOf(other) = %q", got)
	}
	wrapped := fmtWrap{storage.ErrStorageUnavailable}
	if got := observe.OutcomeOf(wrapped); got != observe.OutcomeUnavailable {
		t.Fatalf("OutcomeOf(wrapped) = %q", got)
	}
}

type fmtWrap struct{ err error }

func (f fmtWrap) Error() string { return "wrapped: " + f.err.Error() }
func (f fmtWrap) Unwrap() error { return f.err }
