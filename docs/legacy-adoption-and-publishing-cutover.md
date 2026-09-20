# Legacy adoption and publishing cutover

This document defines how Page Hub takes authority over the existing `web-share` bucket without changing current Publication content or URLs. It also defines when the old direct writer can be revoked, how Page Hub detects and resolves drift, and how emergency storage access works afterward.

The current migration covers ten one-object Adoption candidates under ten top-level prefixes. Six use `index.html` with Publication fallback. Four use an exact named HTML entry point, including one entry point with an uppercase character.

## Cutover invariants

- Public Publication reads remain available throughout the cutover.
- Adoption does not write, copy, rename, or delete storage objects.
- Page Hub preserves every accepted key, byte, serving and user metadata value, entry point, routing mode, public URL, and key character case.
- Page Hub becomes the only routine writer before publishing resumes.
- The `web-share` skill never falls back to direct storage.
- Storage changes that bypass Page Hub never become accepted state automatically.
- A catalog backup is not a content backup. Page Hub does not promise recovery of overwritten or deleted object bodies.

## Roles and credentials

The cutover uses three separate capabilities:

1. **Page Hub storage credential.** The service holds a bucket-scoped credential for normal writes and observations. Publishing clients never receive it.
2. **Legacy writer credential.** The current `web-share` storage access remains available only until the cutover gate passes. The operator then revokes it at the storage service.
3. **Emergency writer capability.** The deployment can create a dedicated emergency key when a break-glass procedure starts. No emergency key remains active between uses.

Deleting local copies of an access key does not count as revocation. The storage service must reject the key. The private deployment repository owns credential creation, revocation, storage, and verification commands.

## Preconditions

Before the write freeze starts:

- Page Hub is deployed with its SQLite catalog, ordered manager routes, browser authentication, publishing-token authentication, and bucket-scoped service credential.
- The public reader still reaches storage without passing through Page Hub.
- A current catalog backup has passed `PRAGMA quick_check` and an isolated restore test.
- The first-party `page-hub` CLI and the replacement `web-share` skill are ready to install.
- The operator has tested the procedure for creating and revoking an emergency key without leaving an active key behind.
- All known installations and copies of the legacy writer credential have been identified.
- The operator has chosen a cutover operation ID and a private place for the deployment log. Secrets do not enter that log.

## Adoption plan

### Start the write freeze

Stop all routine direct publishing. This is a coordinated single-operator freeze, not an interruption to public reads. Page Hub may inspect storage, but no writer may change Publication objects while it builds the adoption plan.

If an urgent public-content repair cannot wait, abort the adoption attempt and use the break-glass procedure. Build a new adoption plan after that repair.

### Capture the baseline

Page Hub performs a complete bucket listing. For every object in the proposed batch, it:

1. records the exact key and character case;
2. records size, opaque ETag, modification time, and all serving and user metadata;
3. downloads the body and computes its SHA-256 digest;
4. identifies the entry point and routing mode from the reviewed candidate layout; and
5. probes the existing public routes needed to preserve status, body, metadata, slash handling, and fallback behavior.

The expected batch is the reviewed set of ten candidates. A missing object, additional object, changed layout, ambiguous boundary, route overlap, or object owned by more than one candidate stops planning. Page Hub reports the difference rather than adding or excluding a candidate automatically.

The plan shows every Project prefix, Publication path, object key, entry point, routing mode, size, digest, metadata value, and canonical public URL. It also shows the public probes that will be repeated after adoption. The operator approves the complete batch, not each candidate separately.

The opaque plan digest binds the operation ID to the complete candidate set and captured state. A changed object or changed catalog state invalidates the plan.

### Commit the batch

Immediately before commit, Page Hub repeats the full listing, metadata reads, body downloads, digests, route checks, and ownership checks. Any difference rejects the complete batch. The operator must review a newly generated plan.

A successful adoption uses one catalog transaction to:

- create one Project for each exact top-level prefix;
- create one Root Publication in each Project;
- assign stable opaque Project and Publication IDs;
- use the exact prefix as the Project display name;
- use the exact entry-point filename as the Publication display name;
- start Project and Publication descriptions empty;
- record the original object modification time as `Content changed`;
- record an immutable initial manifest with exact object state and SHA-256 digests;
- retain `fallback` routing for the six index-backed Publications;
- retain `exact-file` routing for the four named-entry Publications; and
- record adoption provenance, the operation ID, actor, plan digest, and completion time.

The catalog transaction either accepts all ten Publications or none of them. A transaction failure leaves no partially adopted batch and changes no storage objects. Retrying the same operation ID returns its existing status or result.

## Adoption verification

After the transaction commits, Page Hub must demonstrate that:

- all ten Projects and Root Publications appear in the catalog;
- every Publication is in sync;
- every accepted object belongs to exactly one Publication;
- the full bucket scan finds no unexpected object from the adoption process;
- accepted and observed usage equal the pre-adoption baseline;
- all pre-adoption public route probes return the same behavior and bytes;
- named entry points retain exact filenames and case;
- stored serving and user metadata is unchanged; and
- stopping Page Hub leaves all existing public URLs readable.

A verification failure keeps the write freeze in place. Because adoption did not mutate storage, rolling back the manager and catalog cannot damage existing public content.

## Publishing cutover gate

Routine publishing may resume only after all of these checks pass:

1. The catalog backup and isolated restore test succeeded.
2. The adoption batch committed and the post-adoption scan found no drift.
3. Existing public route and metadata checks match the captured baseline.
4. Browser manager authentication admits only the configured operator.
5. Publishing-token tests prove that the token can create and replace content but cannot read descriptions, move, delete, adopt, reconcile, or manage tokens.
6. The `page-hub` CLI successfully creates and replaces a disposable Publication through the production routing and storage path.
7. Manager or catalog unavailability makes the CLI fail closed while existing public reads continue.
8. Every `web-share` installation invokes the Page Hub CLI and contains no direct-storage fallback.
9. The emergency-key creation and revocation procedure is ready.
10. The legacy writer access key has been revoked at the storage service and a signed request with that key is rejected.
11. Deployment automation no longer recreates or redistributes the revoked key.
12. A final full reconciliation after revocation completes without unclassified findings.

Revoking the legacy key is the cutover boundary. Before revocation, the operator may abort the rollout, remove the manager routes, discard the untrusted migration catalog, and resume the legacy process. Any later attempt starts with a new baseline and adoption plan.

After revocation, rollback means rolling back Page Hub code, catalog schema where supported, or manager routing. It does not mean restoring the routine direct writer. Publishing remains paused until Page Hub works again. Urgent repairs use the break-glass procedure.

## Reconciliation

### When reconciliation runs

Page Hub performs a full bucket listing and metadata comparison:

- before enabling storage mutations after service startup;
- daily;
- when the manager inventory opens;
- when the operator requests Refresh;
- after every Page Hub storage mutation;
- after catalog restore or recovery; and
- after every break-glass procedure.

Page Hub also revalidates every affected object immediately before an operation mutates storage. It downloads and hashes object bodies when listing or metadata differs from the accepted manifest, or when the operator starts a reconciliation action. Ordinary unchanged observations do not require downloading every body.

A full scan compares accepted manifests with exact observed keys, sizes, opaque ETags, modification times, serving metadata, and user metadata. It also records unexpected objects as unclaimed storage and includes them in quota and collision checks.

### Write locks

A failed startup scan keeps all storage mutations disabled. Catalog-only description changes may continue if the catalog is healthy.

Unexpected out-of-band drift or declared break-glass use places a global storage-mutation lock. Page Hub lifts that lock only after a complete scan succeeds and the operator classifies every finding. Classification may leave a Publication drifted or missing. That Publication remains individually ineligible for move, replacement, and ordinary deletion, while unrelated in-sync Publications can resume normal operations.

Drift caused by a known Page Hub operation follows the operation contracts. It blocks the affected Publication but does not imply that an external credential was used.

### Resolution choices

Page Hub never chooses a resolution automatically. A fresh scan and the current entity revision are required before each action.

#### Accept observed state

The operator may accept observed storage as a new immutable manifest revision when:

- the full proposed object set is shown as a manifest diff;
- every object remains inside the Publication boundary;
- the entry point exists;
- object paths and metadata pass server validation;
- no object ownership or route overlap is introduced; and
- the operator explicitly confirms the new complete state.

Acceptance does not rewrite storage. It records the observed digests, metadata, storage identifiers, operation ID, actor, and out-of-band provenance. A changed observation invalidates the action and requires a new review.

A completely missing Publication cannot be accepted as an empty manifest. The operator must restore it or use the existing permanent-deletion contract. An object outside every Publication remains unclaimed until an explicit adoption or exact-key cleanup action handles it.

#### Restore accepted content

The catalog stores hashes and metadata, not object bodies. To restore accepted content, the operator must provide the complete source files. Every supplied body and metadata value must match the accepted manifest before any write starts.

The repair uses the break-glass controls and exact keys. Page Hub remains storage-write locked while the operator writes and verifies the objects. A successful repair records a reconciliation revision with the new storage ETags and modification times while retaining the accepted bytes, metadata, entry point, routing mode, and `Content changed` value.

If the operator cannot supply matching content, Page Hub cannot reconstruct it. The Publication stays drifted or missing until the operator accepts another valid observed state or permanently deletes it.

#### Leave unresolved

The operator may classify a finding and leave it unresolved. Page Hub retains the accepted manifest, observed state, diff, and observation time. The affected Publication remains drifted or missing and cannot use storage mutation operations that require an in-sync state.

#### Handle unclaimed storage

Page Hub does not merge an unexpected object into a nearby Publication. The operator may leave it unclaimed, adopt it through an explicit reviewed adoption, or remove that exact key after a fresh digest check and explicit confirmation. Page Hub never performs recursive cleanup.

## Break-glass procedure

Break-glass access exists for an urgent correction or removal while Page Hub cannot write safely, and for an operator-directed restoration of accepted content. It is not an alternate publishing path.

The procedure is:

1. Pause normal Page Hub storage mutations if the service is running.
2. Open a private deployment record with the reason, exact keys, intended action, source paths, expected digests, metadata, and operator. Do not record credentials or private source content.
3. Create a dedicated emergency access key through the storage administration path.
4. Inspect each source as public material and calculate its digest.
5. Re-read each target immediately before mutation.
6. Execute only the recorded exact-key PUT or DELETE requests. Do not use sync, recursive deletion, prefix deletion, copy-based move, or wildcard commands.
7. Download each written object and compare bytes and metadata. Confirm each deleted key is absent.
8. Probe the affected public URLs and record the results.
9. Revoke the emergency key immediately and verify that the storage service rejects it.
10. Start Page Hub, run a full reconciliation, and classify every finding.
11. Resume Page Hub storage mutations only after the reconciliation write lock clears.

Break-glass may replace, restore, or remove existing public content. It may not create a new Publication or move one. A change that can wait for Page Hub must wait.

## Legacy capability removal

Cutover is complete only when the old direct-write capability is unusable, not merely undocumented. The private deployment change must:

- remove the old access key from the storage service;
- remove or replace root-owned credential files that contain it;
- remove it from workstations, password managers, CI, and shell profiles;
- update deployment automation so it cannot recreate the key;
- give Page Hub its own non-exported service credential;
- remove direct S3 commands and credential instructions from `web-share`; and
- retain only the on-demand emergency-key procedure.

The operator records the revoked access-key identifier, revocation time, verification result, and final reconciliation operation ID. Secret values remain excluded.

## Acceptance scenarios

An implementation and deployment of this plan must demonstrate at least these cases:

1. The reviewed ten-candidate batch adopts all Publications without issuing a storage mutation.
2. A changed byte, metadata value, key, or unexpected object between planning and commit rejects the complete batch.
3. A failed catalog transaction leaves no adopted subset and changes no public content.
4. Adoption preserves an uppercase named entry point, exact metadata, clean-root fallback, named-file 404 behavior, and every existing public URL.
5. The manager can stop after adoption without interrupting public Publication reads.
6. The publishing token passes its allowed create and replacement cases and fails every forbidden management action.
7. The revoked legacy access key cannot list, write, or delete bucket content, and deployment automation does not recreate it.
8. A failed startup reconciliation blocks storage mutations without hiding the catalog inventory.
9. Unexpected drift globally locks storage writes until every finding is classified, then leaves only affected Publications blocked.
10. Accepting observed state creates a new manifest revision only after a fresh complete diff and validation.
11. Restoration rejects source bytes or metadata that do not match the accepted manifest.
12. A break-glass write uses a newly created key, touches only recorded exact keys, verifies the result, revokes the key, and completes reconciliation before normal writes resume.
13. A post-cutover Page Hub failure pauses publishing and does not re-enable the routine direct writer.
