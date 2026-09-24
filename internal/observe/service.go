package observe

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/yet-an-other/page-hub/internal/catalog"
	"github.com/yet-an-other/page-hub/internal/storage"
)

// Outcome classifies a refresh attempt for the manager. Provider error text
// is never included.
type Outcome string

const (
	OutcomeNever         Outcome = "never"
	OutcomeSucceeded     Outcome = "succeeded"
	OutcomeUnavailable   Outcome = "unavailable"
	OutcomeMisconfigured Outcome = "misconfigured"
	OutcomeFailed        Outcome = "failed"
)

// ServiceDefaults for a deployment.
const (
	DefaultDailyInterval = 24 * time.Hour
	DefaultRetryDelay    = 30 * time.Second
)

// OutcomeOf classifies a scan error without exposing provider details.
func OutcomeOf(err error) Outcome {
	switch {
	case err == nil:
		return OutcomeSucceeded
	case errors.Is(err, storage.ErrStorageUnavailable):
		return OutcomeUnavailable
	case errors.Is(err, storage.ErrStorageMisconfigured):
		return OutcomeMisconfigured
	default:
		return OutcomeFailed
	}
}

// ServiceOptions tune the service; tests inject a clock and short intervals.
type ServiceOptions struct {
	Now           func() time.Time
	DailyInterval time.Duration // time between scans after a success
	RetryDelay    time.Duration // time between scans after a failure
}

// Service owns the single in-flight storage observation. The vocabulary maps
// one concept across boundaries: "refresh" is the operator-facing action (API
// and Refresh control), "scan" is one engine run, and the "observation" is the
// result the catalog records. Every trigger — startup, daily schedule,
// inventory open, and manual refresh — goes through Refresh: only one full
// scan runs at a time and concurrent triggers join it instead of queueing
// repeated scans.
type Service struct {
	reader        storage.Reader
	store         *catalog.Store
	now           func() time.Time
	dailyInterval time.Duration
	retryDelay    time.Duration

	mu          sync.Mutex
	serviceCtx  context.Context // set by Start; scans outlive single requests
	running     chan struct{}
	lastOutcome Outcome
}

// NewService creates the observation service.
func NewService(reader storage.Reader, store *catalog.Store, options ServiceOptions) *Service {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.DailyInterval <= 0 {
		options.DailyInterval = DefaultDailyInterval
	}
	if options.RetryDelay <= 0 {
		options.RetryDelay = DefaultRetryDelay
	}
	return &Service{
		reader:        reader,
		store:         store,
		now:           options.Now,
		dailyInterval: options.DailyInterval,
		retryDelay:    options.RetryDelay,
		lastOutcome:   OutcomeNever,
	}
}

// Start runs the observation schedule until the context is cancelled: one
// scan immediately, then again after retryDelay when the previous scan
// failed (reconciling a storage outage) or dailyInterval when it succeeded.
// Scans run on this lifecycle context, so a disconnected browser cannot
// cancel a scan that other triggers asked for.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	s.serviceCtx = ctx
	s.mu.Unlock()
	go func() {
		for {
			outcome := s.Refresh(ctx)
			delay := s.dailyInterval
			if outcome != OutcomeSucceeded {
				delay = s.retryDelay
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}

// Refresh requests a storage observation. When a scan is already running it
// joins that scan and returns its outcome instead of queueing another one.
// The wait is bounded by the running scan, which storage timeouts bound in
// turn; the caller's own context cannot cancel work other triggers asked for.
func (s *Service) Refresh(ctx context.Context) Outcome {
	s.mu.Lock()
	if s.running != nil {
		done := s.running
		s.mu.Unlock()
		<-done
		s.mu.Lock()
		outcome := s.lastOutcome
		s.mu.Unlock()
		return outcome
	}
	done := make(chan struct{})
	s.running = done
	scanCtx := s.serviceCtx
	if scanCtx == nil {
		scanCtx = ctx
	}
	s.mu.Unlock()

	outcome := s.scan(scanCtx)
	if outcome != OutcomeSucceeded {
		slog.Warn("storage observation failed", "outcome", string(outcome))
	} else {
		slog.Info("storage observation complete", "observedAt", s.now().UTC().Format(time.RFC3339))
	}

	s.mu.Lock()
	s.lastOutcome = outcome
	s.running = nil
	close(done)
	s.mu.Unlock()
	return outcome
}

// State reports whether a scan is running and the last attempt's outcome.
func (s *Service) State() (running bool, last Outcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running != nil, s.lastOutcome
}

func (s *Service) scan(ctx context.Context) Outcome {
	// Run is the one-shot observation path; the service adds the schedule and
	// the single-scan guarantee around it.
	_, err := Run(ctx, s.reader, s.store, Options{Now: s.now})
	return OutcomeOf(err)
}
