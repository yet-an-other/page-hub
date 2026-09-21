# First implementation slice

This specification defines the first implementation slice for Page Hub. It turns the existing product and architecture decisions into a production-shaped application that can adopt existing Publications into a private catalog and display their observed state without changing Publication content.

Issue [#11](https://github.com/yet-an-other/page-hub/issues/11) records the design discussion. The catalog, deployment, operation, publishing, and cutover contracts remain authoritative where this document does not narrow the first slice.

## Outcome

The slice delivers one Go runtime with an embedded React manager. It can:

- create and migrate a private SQLite catalog;
- plan and atomically commit an explicit adoption batch without mutating storage;
- observe an S3-compatible bucket and compare it with accepted manifests;
- display the catalog-backed inventory, storage state, and usage;
- guard manager routes with the production browser-authentication contract;
- publish a deployable binary and document a generic nginx and S3-compatible deployment; and
- build and pass acceptance tests without production credentials or network access.

This is a read-only Publication-management slice. Adoption writes accepted state to the catalog, but Page Hub does not create, replace, move, or delete Publication objects.

## Implementation checkpoints

Implement the slice through these ordered checkpoints. The ticket breakdown may combine work within a checkpoint when needed to preserve a complete, testable path.

1. Publish and deploy the authenticated manager shell as a Go binary with embedded React assets and read-only storage connectivity.
2. Plan and commit a declared adoption batch, then show the accepted Publications in the manager.
3. Observe storage and report Publication state, bucket usage, and refresh progress.
4. Complete the accepted Variant A inventory interactions and preview behavior.
5. Verify degraded operation and qualify the complete release.

The final gate applies to the integrated slice rather than to any isolated layer.

## Runtime and frontend

The manager uses React with TypeScript, Vite, Tailwind CSS, and shadcn/ui. Use pnpm and pin the Node and pnpm versions in the repository. Build the frontend before compiling the Go runtime, then embed the built files in the binary.

Use TanStack Query for inventory data, refresh state, and background polling. Keep search text and collapsed Project state in local React state. Do not add React Router, a global state library, or TanStack Table in this slice.

Keep an OpenAPI 3.1 document for the browser JSON interface. Generate TypeScript request and response types from it. Implement the small Go HTTP interface directly and verify it against the OpenAPI contract rather than generating a Go server framework.

The UI follows Variant A at commit [`4361f43`](https://github.com/yet-an-other/page-hub/commit/4361f43). The prototype defines the layout and interaction, not pixel-exact styling. Deferred actions do not appear as disabled controls.

## Routing and browser authentication

Reserve `/` for the manager and the complete `/_page-hub/` prefix for assets, browser interfaces, previews, health checks, authentication support, and future publishing interfaces. Reject `_page-hub` as a Project prefix. A missing or failed reserved route must never fall through to public Publication routing.

Production browser requests carry one high-entropy assertion value shared by the authentication gateway and Page Hub. The gateway removes any client-supplied assertion header and writes its configured value. Page Hub compares that value in constant time on every manager request. The deployment connects to Page Hub through its protected Unix socket.

An explicit development bypass may omit the assertion only while the runtime listens on a loopback address. Page Hub must reject a configuration that combines the bypass with a non-loopback listener.

Health checks use the protected upstream path and must not expose catalog data. The private deployment decides whether and how the reverse proxy exposes them.

## First-checkpoint deployment

The first checkpoint is deployable before catalog and adoption work begins. Its manager shell reports whether Page Hub can reach the configured S3-compatible bucket through a read-only connectivity check. It does not expose object names or bucket contents.

CI publishes a versioned Linux amd64 binary and SHA-256 checksum from this checkpoint onward. A generic deployment guide covers:

- runtime configuration and secret injection;
- connecting Page Hub to an S3-compatible endpoint with a bucket-scoped credential;
- running Page Hub on a protected Unix socket;
- placing exact `/` and `/_page-hub/` manager routes before the public Publication catch-all in nginx;
- supplying and stripping the trusted browser assertion header;
- keeping public Publication reads independent of Page Hub; and
- checking health and storage connectivity after deployment.

The guide must use placeholders and contain no private homelab values. Concrete nginx, systemd, authentication-provider, secret, backup, and host changes remain in the private deployment repository.

## Catalog

Use numbered SQLite migrations and an explicit migration command. The runtime checks the schema version and refuses to start against an incompatible or partially applied schema. It does not apply migrations automatically. Configure foreign keys and verify the catalog with SQLite integrity checks where required by the existing deployment contract.

The first schema stores only state used by this slice:

- Projects, including stable identity, exact prefix, display name, optional private description, and entity revision;
- Publications, including stable identity, owning Project, path, display name, optional private description, entry point, routing mode, entity revision, and content-change time;
- immutable accepted manifest revisions and their exact object records;
- Publication and bucket observations kept separate from accepted state;
- adoption plans and durable adoption operation results; and
- migration metadata.

Use random UUIDv4 values for Project, Publication, manifest revision, observation, and operation identities. Use SHA-256 over a canonical plan representation for adoption plan digests.

Do not add publishing tokens, deletion tombstones, or move and replacement operation details yet.

## Adoption

Adoption accepts an explicit batch declaration. The declaration names every proposed Project prefix, Publication path, object set, entry point, and routing mode. Page Hub does not infer Publication boundaries from storage and does not hard-code the ten current Publications.

The service binary provides administrative `plan` and `commit` subcommands that use the same application modules as the runtime. The exact command spelling may follow the binary's final name, but the behavior below is fixed.

### Plan

Planning:

- validates the complete declaration and path rules;
- performs the complete storage listing and ownership checks required by the cutover contract;
- reads exact keys, character case, sizes, modification times, opaque ETags, serving metadata, and user metadata;
- downloads bodies and computes SHA-256 digests;
- checks entry points, routing modes, route overlap, and reserved paths;
- runs the declared public-route probes; and
- emits a reviewable JSON plan with a canonical digest.

A plan does not reserve a route, change storage, or change the catalog.

The public repository contains the declaration schema and representative fixtures. The real ten-Publication declaration belongs to the private deployment repository.

### Commit

Committing requires the approved plan digest and a caller-provided operation ID. The command obtains exclusive catalog access and refuses to run while the service holds the catalog. It repeats the complete storage and catalog checks immediately before one SQLite transaction accepts the batch.

The transaction creates every Project, Root Publication, initial manifest, and adoption operation result, or creates none of them. Reusing an operation ID with the same request returns the existing result. Reusing it with different content fails.

Adoption never issues a storage write, copy, or delete request. A changed object, metadata value, key set, public probe, declaration, or catalog state invalidates the plan.

## Storage observation and reconciliation

Put S3 behavior behind one storage interface. The production adapter uses the configured S3-compatible endpoint. Integration tests run that adapter against an in-process S3-compatible HTTP server. A narrow fake adapter may cover failures that the server cannot produce deterministically.

A full observation lists the entire bucket and compares exact observed state with accepted manifests. It records unexpected objects as unclaimed storage. Follow the existing reconciliation contract for metadata comparison and when body downloads and hashes are required.

Run or request an observation:

- at startup;
- daily;
- when the inventory opens;
- after a manual refresh; and
- through an opt-in read-only compatibility command.

Only one scan runs at a time. Concurrent triggers join the running scan instead of queuing more scans. The manager returns the latest cataloged observation immediately and reports whether a refresh is running.

An observation records:

- `in sync`, `drifted`, or `missing` for each Publication;
- accepted and observed sizes where applicable;
- the last successful check time and stale state;
- complete bucket usage;
- accepted Publication usage;
- unclaimed usage; and
- whether later storage mutations would be locked.

An observation older than five minutes is stale. Configure the storage quota explicitly. Calculate usage from the bucket observation rather than a vendor-specific quota interface. Invalid or missing quota configuration prevents startup. A failed usage scan reports unavailable or stale data, never zero.

A storage failure at startup does not hide a healthy catalog. Start the manager, return its latest inventory with stale or unavailable observations, report degraded readiness, and retry reconciliation. A catalog failure prevents readiness. Adoption planning and committing fail closed when required storage or catalog state is unavailable.

This slice records storage-mutation lock state but does not implement Publication content mutations or reconciliation resolution.

## Inventory and preview

The browser manager implements the read-only parts of the accepted inventory contract:

- show every Project, including empty Projects, and every managed Publication;
- group Publication rows under collapsible Project headers;
- sort Projects and Publications by display name without case sensitivity, using exact path as the tie-breaker;
- search display names, prefixes, paths, entry points, and private descriptions;
- expand a collapsed Project when it contains a search result;
- display Project and Publication private descriptions as plain text;
- show accepted size and `Content changed` for each Publication;
- show observed state, status detail, observation time, and stale warnings;
- show exact quota, bucket usage, accepted usage, and unclaimed usage;
- show refresh progress without replacing cataloged data; and
- work at desktop and narrow viewport widths.

The Publication path opens an authenticated Page Hub preview in a new tab. An in-sync Publication redirects to its canonical public URL. A drifted or missing Publication shows a warning and does not redirect. The separate public-page icon always opens the canonical public URL in a new tab without giving the destination access to the manager tab.

The manager does not embed public Publication content.

## Verification

One repository command must:

- format and lint Go and TypeScript;
- run Go unit and integration tests;
- verify that generated OpenAPI TypeScript types are current;
- build the React manager;
- embed it into the Go binary; and
- run Playwright acceptance tests against that binary.

Playwright covers desktop and narrow viewports. Automated accessibility checks cover the inventory and interactive controls. Serious accessibility violations fail the suite.

The hermetic fixtures include:

- a multi-object directory Publication;
- an exact-file Publication with an uppercase entry point;
- fallback and exact-file routing;
- drifted and missing Publications;
- unclaimed objects;
- route and object ownership conflicts; and
- storage and catalog failures.

An opt-in read-only compatibility command may plan the known production adoption batch against RadosGW. CI and the normal verification command never require production credentials or network access.

## Release files

CI builds a versioned Linux amd64 binary with embedded manager assets and publishes its SHA-256 checksum from the first checkpoint onward. The binary reports its version and, once the catalog exists, its compatible catalog schema range.

The final checkpoint runs the integrated acceptance gate through the same release pipeline. Concrete installation, systemd, nginx, oauth2-proxy, backups, production release promotion, and production adoption remain in the private deployment work.

## Final acceptance scenarios

The integrated slice is complete when automated checks demonstrate all of these cases:

1. A clean checkout passes the repository verification command without production access.
2. CI publishes the compiled Linux amd64 binary and matching checksum, and the binary serves the React manager and its OpenAPI-described browser interface.
3. The generic deployment guide can place that binary behind nginx, connect it read-only to S3-compatible storage, and preserve the independent public Publication reader.
4. Missing or invalid browser assertions are rejected, and development bypass cannot listen beyond loopback.
5. A reserved-route failure never returns public Publication content.
6. The runtime refuses an incompatible or partially applied catalog schema.
7. Adoption plans exact keys, bytes, metadata, entry points, routing behavior, and public probes without changing storage.
8. A changed byte, metadata value, key, probe, declaration, or catalog state invalidates the complete adoption plan.
9. Adoption commits all declared Publications or none, and an operation ID has one durable result.
10. Adoption preserves multi-object relative paths, uppercase entry-point case, metadata, routing mode, and original content-change times.
11. Storage scans classify in-sync, drifted, and missing Publications and report unexpected objects as unclaimed usage.
12. Concurrent refresh triggers share one scan, while inventory reads return the latest cataloged observation immediately.
13. A storage outage leaves catalog inventory visible with stale or unavailable status and degraded readiness.
14. A catalog outage prevents readiness and all adoption work fails closed.
15. Search finds private descriptions inside collapsed Projects and expands the matching Project.
16. The inventory shows accepted size, observed differences, content-change time, exact quota, accepted usage, unclaimed usage, and observation time.
17. Preview redirects only for an in-sync Publication, while the direct public link remains available in every state.
18. The manager passes its desktop, narrow viewport, keyboard, and automated accessibility checks.

## Out of scope

This slice does not include:

- Publication content creation or replacement;
- Project or Publication description editing;
- Publication moves or permanent deletion;
- accepting observed state, restoration, or unclaimed-object removal;
- publishing tokens or the publishing CLI;
- production adoption or legacy credential revocation;
- homelab deployment changes; or
- disabled placeholders for deferred actions.
