# Release qualification

This document is the qualification record for the complete first
implementation slice ([#21](https://github.com/yet-an-other/page-hub/issues/21)).
It maps each requirement of the [specification](first-implementation-slice.md)
to the automated evidence that demonstrates it, so maintainers and the
operator can verify a release without production access.

## The verification command

```sh
make verify
```

runs, in order:

1. `pnpm --dir web install --frozen-lockfile` — pinned dependencies;
2. `pnpm --dir web verify:openapi` — regenerates the OpenAPI TypeScript types
   and fails if `src/api/generated.ts` is stale;
3. `pnpm --dir web typecheck` and `pnpm --dir web lint` — TypeScript check
   and lint;
4. `gofmt -w cmd internal web/embed.go` — Go formatting;
5. `pnpm --dir web build` — the React manager bundle that `web/embed.go`
   embeds;
6. `go test ./...` — Go unit and integration tests, including the in-process
   S3-compatible server and the compiled-binary CLI end-to-end test;
7. `go vet ./...` — Go static analysis; and
8. `pnpm --dir web exec playwright test` — the acceptance suite against the
   compiled `page-hub` binary started by
   [`web/playwright.config.ts`](../web/playwright.config.ts), covering
   desktop and narrow viewports with keyboard and automated accessibility
   checks.

The command is hermetic: every test runs against loopback servers
(`internal/storage/s3test`, `httptest`, and the Playwright-managed binary on
`127.0.0.1`). No production credentials, private configuration, or network
access is required. The Playwright suite runs in CI on every push and pull
request (`.github/workflows/ci.yml`).

## Hermetic fixtures

The acceptance-criteria fixture list is covered by:

| Fixture | Evidence |
| --- | --- |
| Multi-object directory Publication | `TestScanRepresentativeFixtures` (`guides` with `index.html` + `app.js`), `TestCLIAdoptionEndToEnd`, `TestS3ReaderListsCompleteBucketWithPagination` |
| Uppercase-entry exact-file Publication | `TestScanRepresentativeFixtures` (`reports/Index.html`), `TestDeclarationPreservesExactCase`, `TestAdoptionPreservesLegacyFacts` |
| Fallback (directory index) routing | `TestScanRepresentativeFixtures`, `TestPlanRecordsExactObservedState`, Playwright preview redirect test |
| Exact-file routing | `TestScanRepresentativeFixtures` (`docs`), `TestCLIAdoptionEndToEnd` |
| Drifted Publications | `TestScanDetectsChangedBytesThroughBodyVerification`, `TestScanDetectsMetadataOnlyDriftAndVerifiesBytes`, `TestScanCompleteReconciliationReportsBodyOnlyDrift`, `TestPreviewWarnsInsteadOfRedirectingWhenNotInSync` |
| Missing Publications | `TestScanClassifiesMissingPublicationAndPartialDrift`, `TestS3ReaderClassifiesMissingObject`, `TestPreviewWarnsInsteadOfRedirectingWhenNotInSync` |
| Unclaimed objects | `TestScanKeepsUnexpectedObjectsUnclaimed`, `TestScanWithEmptyCatalogObservesOnlyUnclaimedStorage` |
| Route and ownership conflicts | `TestDeclarationRejectsAmbiguousBoundariesAndOverlappingRoutes`, `TestCommitRejectsAlreadyManagedPaths`, `TestCommitRejectsDriftedStorage` |
| Slow storage | `TestRefreshResponseSurvivesServerWriteTimeout` (a scan slower than the server write deadline still answers), `TestRefreshStartsOneScanAndJoinsConcurrentTriggers` |
| Unavailable storage | `TestScanFailsClosedWhenStorageIsUnavailable`, `TestS3ReaderClassifiesUnavailableStorage`, `TestRunFailsWithoutRecordingWhenStorageIsUnavailable`, `TestReadinessReportsDegradedWhenStorageUnreachable` |

## Security contracts

- Reserved-route failures never fall through to the public reader:
  `TestReservedRouteFailureCannotFallThroughToPublicReader`, plus
  `TestAuthenticationFailuresNeverExposePublicContent`, which proves for
  every reserved route that an authentication failure returns `401`, never
  reaches the public reader, and never returns Publication content.
- Missing, invalid, and client-spoofed assertions are rejected in constant
  time: `TestManagerRejectsMissingInvalidAndSpoofedAssertions`; the
  development bypass is loopback-only:
  `TestDevelopmentBypassRequiresLoopbackListener`.
- Logs and health responses stay clean. `TestRequestHandlingLogsNoSecrets`
  captures every log record emitted while serving authenticated requests,
  authentication failures, refreshes, previews, and public reads, and
  asserts that no assertion value, private description, storage credential,
  or internal failure detail appears. `TestHealthAndReadinessRevealNoCatalogData`
  pins `healthz` and `readyz` to their exact minimal field sets. The server
  emits no log records containing request data at all.

## Binary identity

`page-hub version` (and `page-hub -version`) print the binary version and the
compatible catalog schema range — `TestVersionCommandReportsVersionAndSchemaRange`.
The runtime refuses an incompatible, newer, partially applied, or unmigrated
catalog (`TestOpenRuntimeRefuses*`, `TestMigrateIsIdempotent`).

## Opt-in storage compatibility check

`page-hub check` runs the read-only compatibility path against a configured
S3-compatible endpoint such as RadosGW: complete bucket listing plus complete
body downloads with SHA-256 digest computation over a bounded, deterministic
key sample. It
requires only the `PAGE_HUB_S3_*` variables, never touches the catalog, and
issues only GET and HEAD requests:

- `TestCheckCompatibilityListsDownloadsAndOnlyReads`
- `TestCheckCompatibilityBoundsDownloadedBodies`
- `TestCheckCompatibilityHandlesEmptyBucket`
- `TestCheckCompatibilityFailsClosedWhenStorageUnavailable`
- `TestCheckCompatibilityFailsWhenBodyReadFails`
- `TestCLICheckRunsReadOnlyCompatibilityChecks`
- `TestCLICheckFailsClosedWithoutTouchingTheCatalogOrLeakingSecrets`
- `TestCLICheckRejectsUnexpectedArguments`

The check is opt-in: `make verify` and CI never invoke it, and a failed check
writes nothing anywhere.

## Release pipeline

CI (`.github/workflows/ci.yml`) runs `make verify`, then `make release`
builds the versioned Linux amd64 binary with the embedded manager and emits
its SHA-256 checksum. Tag pushes (`v*`) publish both to a GitHub release
through the pipeline introduced by #15. The generic deployment guide
([first-checkpoint-deployment.md](first-checkpoint-deployment.md)) installs
that exact artifact, connects it read-only to S3-compatible storage, and
keeps the public Publication reader independent.

## Final acceptance scenarios

Mapping of the specification's integrated scenarios to evidence:

| # | Scenario | Evidence |
| --- | --- | --- |
| 1 | Clean checkout passes verification without production access | CI runs `make verify` on every push; all tests are loopback-only |
| 2 | CI publishes binary + checksum; binary serves manager and OpenAPI interface | `ci.yml` release job; Playwright `compiled binary serves the authenticated manager shell`; `verify:openapi` |
| 3 | Generic deployment guide preserves independent public reader | `first-checkpoint-deployment.md`; `TestPublicReaderDoesNotReceiveManagerCredentials` |
| 4 | Missing/invalid assertions rejected; bypass loopback-only | `TestManagerRejectsMissingInvalidAndSpoofedAssertions`, `TestDevelopmentBypassRequiresLoopbackListener`, `TestAuthenticationFailuresNeverExposePublicContent` |
| 5 | Reserved-route failure never returns public content | `TestReservedRouteFailureCannotFallThroughToPublicReader`, `TestAuthenticationFailuresNeverExposePublicContent` |
| 6 | Runtime refuses incompatible or partially applied schema | `TestOpenRuntimeRefusesNewerSchema`, `TestOpenRuntimeRefusesPartiallyAppliedSchema`, `TestOpenRuntimeRefusesUnmigratedCatalog`, `TestOpenRuntimeRefusesTamperedHistory` |
| 7 | Adoption plans exact keys, bytes, metadata, entry points, routing, probes without changing storage | `TestPlanRecordsExactObservedState`, `TestPlanProbesDeclaredPublicRoutes`, `TestAdoptionPreservesLegacyFacts`, `TestCLIAdoptionEndToEnd` (GET/HEAD-only assertion) |
| 8 | Any change invalidates the complete plan | `TestPlanRejectsUnexpectedStorageObjects`, `TestPlanRejectsMissingDeclaredObject`, `TestCommitRejectsDriftedStorage`, `TestCommitRejectsChangedProbeResults`, `TestCommitRejectsPublicBaseURLMismatch`, `TestPlanDigestBindsProbeResults` |
| 9 | All-or-none commit; one durable result per operation ID | `TestCommitAcceptsWholeBatch`, `TestCommitAdoptionBatchIsAtomic`, `TestCommitReplaysIdenticalOperation` |
| 10 | Adoption preserves paths, case, metadata, routing mode, content-change times | `TestAdoptionPreservesLegacyFacts`, `TestDeclarationPreservesExactCase`, `TestCLIAdoptionEndToEnd` |
| 11 | Scans classify in-sync/drifted/missing; unexpected objects unclaimed | `TestScanClassifiesInSyncWithoutDownloads`, `TestScanDetectsChangedBytesThroughBodyVerification`, `TestScanClassifiesMissingPublicationAndPartialDrift`, `TestScanKeepsUnexpectedObjectsUnclaimed` |
| 12 | Concurrent triggers share one scan; inventory reads return latest immediately | `TestRefreshStartsOneScanAndJoinsConcurrentTriggers`, `TestJoinerSharesTheRunningScanOutcome`, `TestInventoryIdentifiesRunningRefreshAndStaleObservation` |
| 13 | Storage outage leaves inventory visible, degraded readiness | `TestReadinessReportsDegradedWhenStorageUnreachable`, `TestRunFailsWithoutRecordingWhenStorageIsUnavailable`, Playwright `stale inventory stays readable through a failed refresh` |
| 14 | Catalog outage prevents readiness; adoption fails closed | `TestReadinessReportsDegradedWhenCatalogUnavailable`, `TestCommitRefusesWhileRuntimeHoldsCatalog`, `TestRefreshReportsFailedWhenCatalogUnavailable` |
| 15 | Search finds private descriptions in collapsed Projects and expands them | Playwright `search covers names, prefixes, paths, entry points, and descriptions`, `a search expands a collapsed Project and preserves other collapse choices` |
| 16 | Inventory shows sizes, differences, content-change time, quota, usage, observation time | Playwright `Publication rows show accepted facts, observed state, detail, time, and staleness`, `the inventory shows exact used, quota, accepted, and unclaimed usage` |
| 17 | Preview redirects only in-sync; public link always available | `TestPreviewRedirectsInSyncPublicationToCanonicalURL`, `TestPreviewWarnsInsteadOfRedirectingWhenNotInSync`, `TestPreviewUnknownPublicationWarnsWithoutRedirect`, Playwright `the public-page icon stays available in every state` |
| 18 | Desktop, narrow viewport, keyboard, accessibility checks | Playwright `chromium` + `chromium-narrow` projects, `keyboard operation toggles groups and drives search`, `the inventory passes accessibility checks…`, `the preview warning page passes accessibility checks` |
