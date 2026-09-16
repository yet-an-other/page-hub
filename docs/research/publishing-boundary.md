# Research: Direct S3 publishing versus Page Hub-mediated publishing

## Question

For this system, what are the concrete costs, benefits, failure modes, and migration implications of keeping direct S3 writes in the `web-share` skill versus routing publication writes through a Page Hub API?

This report informs, but does not make, the decision in **“Choose the publishing control boundary.”**

## Summary

Page Hub should be the **authoritative acceptance and validation boundary** for publication changes if its private catalog, descriptions, collision rules, and operation history are intended to be trustworthy. Leaving an unrestricted direct-S3 path in steady state makes S3 authoritative for bytes while Page Hub can only observe and reconcile after the fact; that is simpler and keeps publishing independent of Page Hub, but it permits uncatalogued or policy-invalid changes and requires storage credentials in every publisher environment.

For the currently observed workload—ten isolated, single-file HTML publications, each under about 57 KiB—a fully API-mediated upload is operationally simple and its extra data hop is immaterial. The API contract should nevertheless allow a later **API-authorized, storage-direct data plane** (short-lived, key-scoped upload grants followed by verification/finalization) if larger or multi-file publications emerge. A staged hybrid is also the safest migration mechanism, but it should not leave two equal authorities indefinitely.

## Scope and agreed constraints

The analysis assumes:

- a private, single-user manager at `https://share.bdgn.me/`;
- public Publications at top-level paths;
- an authenticated backend is acceptable;
- existing URLs survive adoption;
- descriptions remain private;
- first-release deletion is permanent after confirmation;
- moves may break old URLs; and
- browser uploads, multi-user permissions, redirects, recoverable deletion, and soft delete are out of scope.

“API-mediated” below means Page Hub authenticates, validates, and accepts the operation. The upload bytes may either pass through the API (**proxy data plane**) or, in a later hybrid, travel directly to storage using a narrowly scoped grant issued by Page Hub (**direct data plane**).

## Evidence from the prerequisite reports

### Current inventory

The [completed inventory report](https://github.com/yet-an-other/page-hub/blob/research/inventory-publications/docs/research/current-publications.md) establishes:

- There are **10 objects under 10 distinct top-level prefixes**, with no root objects.
- Every current Publication is a **single HTML object**. Six use `index.html`; four use an exact named HTML entry point.
- All ten prefixes can be adopted without copying, renaming, or republishing objects.
- The six index-backed Publications work at their prefix roots and have observed publication-scoped SPA fallback. The four named-entry Publications work only at their exact object URLs; their prefix roots return 404.
- One named entry point contains an uppercase character. Exact key casing must therefore survive adoption.
- Current objects range from **14,361 to 56,586 bytes**, totalling about **304 KiB**.
- Existing object HTTP metadata varies slightly and should not be rewritten merely to register a Publication.
- There is no evidence yet for multi-file sites, asset trees, or nested layouts.

These facts make non-destructive registration practical and make a proxied first upload path inexpensive, but they do not justify designing out future larger or multi-object publications.

### Current infrastructure

The [completed infrastructure report](https://github.com/yet-an-other/page-hub/blob/research/infrastructure-constraints/docs/research/current-infrastructure.md) establishes:

- Publication objects are publicly readable; anonymous listing is denied, but knowledge of an object path is sufficient to read it.
- The root currently returns 404 and no private manager or authenticated management API was observed.
- The existing contract publishes below top-level prefixes, forbids root writes, and states a 30 GiB bucket quota.
- No publication registry currently records private descriptions, ownership, status, provenance, or an application identifier.
- Versioning is unset; no backup, rollback, or recovery behavior was demonstrated. Lifecycle configuration is unknown.
- Object-level updates are possible, but no cross-object transaction, atomic release procedure, stale-file cleanup behavior, or concurrent-publisher rule was demonstrated.
- Existing arbitrary-origin browser CORS tests failed. This does not constrain the first release because browser uploads are out of scope.
- The actual S3-compatible service has not been proven to implement all Amazon S3 semantics cited below, especially conditional writes, presigning, versioning, or lifecycle behavior.

The public read path can remain independent of the manager and API. An API outage therefore need not take existing Publications offline; it need only stop management and new publication writes.

## What is authoritative in each option?

### 1. Keep direct S3 writes

- **Content authority:** the bucket contents and keys.
- **Acceptance authority:** effectively the `web-share` skill version and whatever validation it performs locally.
- **Page Hub catalog:** derived/observational. It cannot truthfully claim that every stored change passed Page Hub validation.
- **Private descriptions:** must be maintained separately from public objects, creating a two-system consistency problem.

This is coherent only if the product accepts “anything validly present under a prefix is a Publication” or treats the catalog as an eventually reconciled view.

### 2. Route writes through Page Hub

- **Acceptance authority:** the authenticated Page Hub API and its operation contract.
- **Content authority:** S3 remains the source of served bytes, while the Page Hub catalog records which state is accepted and managed.
- **Private descriptions:** remain in Page Hub’s private data store and can be updated with the publication record.

This is the clearest model, but API mediation does **not** create an automatic transaction across the catalog and object store. The implementation still needs idempotency, ordering, verification, and compensation.

### 3. Staged hybrid

A safe hybrid has one logical authority and two data-plane steps:

1. Page Hub authenticates the caller, validates a manifest, checks/reserves the destination, and creates an operation ID.
2. Page Hub either accepts bytes itself or issues short-lived, operation-specific upload grants.
3. `web-share` uploads to a staging namespace or exact reserved keys.
4. Page Hub verifies key, size, checksum, content metadata, and expected object set, then finalizes the catalog state.

Unfinalized objects are not accepted Publications. This preserves a central policy boundary while avoiding a permanent API byte proxy. It is more complex than either simple direct upload or simple proxying and needs cleanup for abandoned operations.

A **migration hybrid** may temporarily allow legacy direct writes while new writes use Page Hub. During that period, Page Hub must label directly discovered changes as external/unmanaged or reconcile them explicitly; it must not silently present both paths as equally validated.

## Comparison

| Dimension | Direct S3 from `web-share` | Page Hub API with proxied bytes | API control plane with storage-direct upload |
|---|---|---|---|
| **Authority** | Split: the tool accepts locally; storage state wins; Page Hub observes later. | Clear: API accepts or rejects every managed operation. | Clear if grants require API reservation and successful finalization; split if callers can still write arbitrary live keys. |
| **Metadata consistency** | Private catalog and public bytes can diverge. Reconciliation is mandatory. | API can coordinate content and private metadata, though cross-store failures still require compensation. | Manifest/finalize protocol can bind metadata to verified objects; abandoned uploads create extra states. |
| **Validation** | Fast and local, but policy changes require updating every skill installation and old clients can bypass them. | Central, versioned validation and uniform collision rules. | Central manifest validation plus post-upload verification; storage cannot enforce all semantic rules by itself. |
| **Credential handling** | Each publisher environment needs storage capability, commonly broader and longer-lived than one operation. | Storage credentials stay at the trusted backend; clients hold only Page Hub credentials. | Backend retains storage credentials; clients receive short-lived, key-scoped bearer grants. AWS recommends temporary credentials and least privilege for workloads. [IAM best practices](https://docs.aws.amazon.com/IAM/latest/UserGuide/best-practices.html) |
| **Upload efficiency** | One network hop; native SDK/CLI retry and multipart support. | Client → API → storage adds latency, bandwidth, request-body handling, and possible runtime limits. For the observed 14–57 KiB files this cost is negligible. | Bytes go directly to storage and can use multipart upload; best for large artifacts, but protocol and cleanup are more complex. [Multipart upload](https://docs.aws.amazon.com/AmazonS3/latest/userguide/mpuoverview.html) |
| **Availability** | Publishing works while Page Hub is down, provided storage and credentials work. | Page Hub/API outage blocks new writes; existing public reads can remain unaffected. | New reservations/finalization require Page Hub; already issued grants may continue until expiry. |
| **Partial failures** | Local retries and mixed multi-object releases; Page Hub may never learn the intended operation. | API can expose one operation status, but S3 success/catalog failure and ambiguous timeouts still exist. | Adds reserved, uploading, uploaded, verified, committed, expired, and abandoned states. |
| **Concurrency/collisions** | Unconditional writes can overwrite. Every client must implement the same preconditions. | API can serialize by Publication and require expected revisions/ETags. | API reserves a destination and signs preconditions where supported; finalization detects drift. Amazon S3 documents `If-None-Match` and `If-Match`, but compatibility must be tested here. [Conditional writes](https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes.html) |
| **Backward compatibility** | Existing behavior remains unchanged. | Can preserve existing keys and reader URLs if the API writes in place and imports exact entry points. | Same, provided staging is internal and final keys are unchanged. |
| **Operations** | Few new services, but distributed credentials, client-version drift, audit gaps, and recurring reconciliation. | Requires an authenticated service, private catalog, monitoring, idempotency, storage integration, and its own deployment/rollback. | All API operating costs plus grant issuance, finalize/reaper jobs, and staging visibility rules. |
| **Auditability** | Storage access logs may show low-level calls; intent, description changes, and one logical publication operation are not inherently grouped. | API can record actor, operation ID, manifest, validation result, and final state in one domain audit trail. | Same if finalization is mandatory; raw storage logs remain useful evidence. |
| **Rollback of boundary** | No migration needed, but later centralization is harder as drift accumulates. | Can temporarily restore a direct emergency path, but must reconcile every bypass before trusting the catalog again. | Can switch the upload data plane without changing the API contract; safest evolutionary path if designed deliberately. |

## Concrete failure modes and required responses

| Failure mode | Direct S3 consequence | API/hybrid consequence | Required handling |
|---|---|---|---|
| **Two writers create the same key** | Last completion may overwrite the first unless both clients use conditions. | API can reserve or serialize, but a bypass writer can still race it. | Define create-versus-replace semantics; use expected revision/ETag or `If-None-Match` where verified; return a stable collision result. |
| **Network timeout after a successful PUT** | Client cannot know whether retry will overwrite. | API caller may retry after storage succeeded but before the API response. | Require an idempotency/operation ID; verify with `HEAD` plus expected size/checksum; make repeat requests return the prior result. S3-compatible behavior must be tested. |
| **One object in a set fails** | Public readers may see a mixed release; cleanup intent is local to the skill. | Proxying alone has the same problem. | Do not claim atomic multi-file publication. Use staging plus verified commit, immutable release paths plus an indirection, or an explicit upload order such as entry point last. No current multi-file requirement has been demonstrated. |
| **Object write succeeds, catalog write fails** | Normal steady state if Page Hub is only observing. | Bytes may be public but operation appears failed/unmanaged. | Record intent before writing, retry finalization idempotently, reconcile by operation ID/manifest, and delete/quarantine abandoned staging objects. Do not automatically delete an in-place object if it may have replaced valid prior content. |
| **Catalog says accepted, object write fails** | Not applicable to a storage-derived catalog, though external metadata can still be stale. | Page Hub can expose a broken Publication. | Never mark committed before object verification; keep failed/pending states private; retry or compensate. |
| **Client publishes without Page Hub** | Expected and therefore not distinguishable from managed publication. | Catalog authority is violated. | Once cut over, remove broad direct credentials and enforce the boundary at storage policy level; retain a documented break-glass path with mandatory reconciliation. |
| **Event-driven reconciliation** | Can reduce lag but cannot prove prior validation. | Useful for drift detection, not primary commit. | Treat notifications as hints. Amazon S3 notifications are at-least-once, may be duplicated, and are not ordered. [Event ordering and duplicates](https://docs.aws.amazon.com/AmazonS3/latest/userguide/notification-how-to-event-types-and-destinations.html#event-ordering-and-duplicate-events) |
| **Expired or leaked presigned URL** | Not relevant to ordinary credentialed direct writes. | Hybrid upload fails after expiry, or the bearer can be replayed before expiry. A presigned PUT to an existing key may replace it. | Use short expiry, exact key and method, signed checksum/headers where supported, one operation per grant, and conditional creation. Presigned URLs are bearer tokens and may be reusable until expiry. [Presigned URLs](https://docs.aws.amazon.com/AmazonS3/latest/userguide/using-presigned-url.html) |
| **Abandoned multipart upload** | Parts may remain billable. | Same for API or hybrid multipart. | Abort on cancellation and run bounded cleanup. Amazon S3 retains parts and charges for them until completion or abort. [Multipart upload](https://docs.aws.amazon.com/AmazonS3/latest/userguide/mpuoverview.html) |
| **Delete/move partially completes** | Some source or destination keys may remain. | Central orchestration makes state visible but does not make copy/delete atomic. | Precompute a manifest, verify copies before deleting sources, report partial state, and make retry safe. Permanent deletion and link-breaking moves still need explicit confirmation. |
| **Page Hub unavailable** | Direct publishing continues; catalog drifts. | New API-controlled publication stops; public reads need not stop. | Keep the public reader independent, expose a clear retryable failure, and define whether break-glass direct writes are allowed. |
| **Validation rollout is defective** | Requires skill rollback/update on each publisher environment. | One API rollback can restore service, but all writers are affected at once. | Version the contract, support dry-run validation, retain backward-compatible request versions during migration, and separate validation deployment from destructive storage changes. |

Amazon S3 documents strong read-after-write consistency and atomicity per key, not an atomic transaction spanning multiple keys or an application catalog. The current service is only known to be S3-compatible, so these guarantees cannot be assumed until tested. [Amazon S3 consistency model](https://docs.aws.amazon.com/AmazonS3/latest/userguide/Welcome.html#ConsistencyModel)

## Migration implications

### Non-destructive adoption

No option requires republishing the current ten Publications. Page Hub can import a record containing the exact top-level prefix, exact entry-point key and casing, observed metadata, size, and ETag while leaving bytes untouched.

Import must preserve two existing routing classes:

1. six index-backed Publications with working prefix roots and observed fallback; and
2. four exact named-entry Publications whose prefix roots currently return 404.

Registration must not normalize the uppercase leaf, rename named entries to `index.html`, rewrite content metadata, or create root objects. Existing public URLs and reader behavior remain the compatibility baseline.

### Recommended staged cutover

1. **Define the contract first.** Specify manifest shape, allowed top-level names, exact entry point, create versus replace, metadata rules, expected revision, idempotency key, validation errors, and operation states.
2. **Import read-only.** Register all current Publications without object mutations. Keep private descriptions only in the authenticated catalog.
3. **Reconcile and baseline.** Compare catalog manifests with storage listings and heads. Record drift without automatically rewriting existing objects.
4. **Add Page Hub dry-run/reservation.** Let `web-share` submit the intended manifest and receive deterministic validation/collision results before any write.
5. **Cut new writes to the API.** For the observed single small HTML objects, proxy bytes through Page Hub initially. Verify the resulting object before marking the Publication committed.
6. **Add a direct data plane only when justified.** If real payloads or runtime limits warrant it, preserve the same operation API but return short-lived key-scoped upload grants, then require finalization and verification. Stage objects outside public Publication paths unless premature public visibility is explicitly accepted.
7. **Close the bypass.** After a monitored compatibility window, revoke the skill’s general storage write/delete credentials or constrain policy so accepted production writes require the chosen Page Hub path.
8. **Keep drift detection.** Periodic full reconciliation remains necessary even if events are added, because events can duplicate or arrive out of order.

### Rollback

Rollback must distinguish the **control-plane rollback** from content recovery:

- The public read path and current object keys should remain unchanged throughout cutover, so rolling back Page Hub must not remove existing Publications.
- During early rollout, the prior direct writer can be retained but disabled. Re-enabling it is an emergency compatibility rollback, not a return to catalog consistency.
- Every direct write made during rollback must be inventoried and explicitly adopted/rejected before Page Hub again claims authoritative status.
- Do not rely on storage rollback for content: versioning is currently unset and first-release deletion is intentionally permanent.
- Database/API schema changes should be backward compatible for the duration of the rollback window; destructive catalog migrations should follow, not precede, successful write-path cutover.

## Recommendation, with conditions

Recommend that **“Choose the publishing control boundary” select Page Hub as the sole authority for accepting and validating managed publication changes**, while keeping the existing object reader as the public serving data plane.

For the first observed workload, use a **proxied API upload** because it has the fewest protocol states and the payload cost is negligible. Define the operation contract so the byte path can later change to Page Hub-issued, narrowly scoped, storage-direct uploads without changing who authorizes and finalizes publication.

Use a staged hybrid for migration, not as a permanent dual-authority design. Keeping unrestricted direct S3 writes permanently is reasonable only if all of the following are consciously accepted:

- Page Hub’s catalog is eventually consistent and derived rather than authoritative;
- a Publication can appear or change without Page Hub validation;
- private descriptions may temporarily reference missing or changed content;
- storage credentials remain distributed to publisher environments; and
- reconciliation, conflict reporting, and client-version drift are ongoing operating responsibilities.

The API-authoritative recommendation is conditional on:

1. an authenticated backend being deployed with protected private state and least-privilege storage access;
2. existing exact keys, entry points, casing, metadata, public URLs, and routing behavior being preserved on adoption;
3. operation IDs, idempotent retry, create/replace preconditions, and explicit pending/failed/committed states being implemented;
4. storage success being verified before catalog commit;
5. public reads remaining independent of Page Hub write availability;
6. the compatible endpoint’s conditional-write, checksum, presigning, and multipart behavior being tested before those features are relied upon; and
7. a reconciliation and break-glass procedure existing before direct credentials are revoked.

If those conditions cannot be met in the first slice, retain direct S3 temporarily and describe Page Hub honestly as an inventory/catalog view. Do not claim central validation until the bypass is technically closed.

## Decision points for “Choose the publishing control boundary”

The decision ticket should explicitly choose:

1. **Authority:** Is a Publication accepted because its objects exist, or only after Page Hub commits an operation?
2. **Initial data plane:** Should the small current payloads be proxied, or is storage-direct upload required immediately?
3. **Visibility before commit:** May reserved/uploaded objects be publicly readable before finalization, or must staging be non-public?
4. **Create and replace concurrency:** Which expected revision/ETag and conditional-write rules apply?
5. **Catalog failure policy:** On storage success plus catalog failure, should Page Hub retry, quarantine, or compensate?
6. **Emergency bypass:** Is direct S3 a supported break-glass mechanism, who may use it, and how is mandatory reconciliation triggered?
7. **Cutover gate:** What evidence permits revoking the legacy writer’s broad storage capability?
8. **Future scale trigger:** What observed file count, payload size, or API runtime constraint justifies moving from proxying to a signed direct data plane?

## Unknowns and validation work

- The Page Hub runtime, deployment platform, request-size/time limits, and authentication design are not yet chosen.
- The S3-compatible endpoint’s support for presigned PUTs, signed checksums, conditional `If-Match`/`If-None-Match`, multipart completion conditions, and strong consistency has not been verified.
- The mechanism permitting anonymous reads is unknown; staging must not be assumed private merely because it uses an unusual prefix.
- Write/delete permissions of the current principal were not tested.
- Bucket lifecycle behavior, backups, and out-of-band recovery remain unknown.
- No real multi-file Publication exists, so atomic-release complexity is prospective rather than a demonstrated first-release need.
- Expected maximum publication size, publish frequency, concurrency, and acceptable management write downtime are not specified.
- The private catalog technology and its backup/rollback properties are not chosen.
- Move semantics need a separately defined copy/verify/delete contract; object storage has no rename primitive.
- Whether unchanged object metadata should be preserved on replacement, or normalized under a new policy, remains undecided.

## Sources

### Kept

- [Completed current Publications inventory report](https://github.com/yet-an-other/page-hub/blob/research/inventory-publications/docs/research/current-publications.md) — primary project evidence for object count, redacted layout categories, sizes, naming edge cases, existing routing, and non-destructive adoption. The linked public report omits exact keys, prefix names, URLs, credentials, addresses, and private infrastructure details.
- [Completed infrastructure constraints report](https://github.com/yet-an-other/page-hub/blob/research/infrastructure-constraints/docs/research/current-infrastructure.md) — primary project evidence for public access, root behavior, current contract, CORS observations, absent registry, unset versioning, and unverified compatible-endpoint capabilities. The linked public report omits credentials, addresses, object keys, prefix names, DNS targets, and unrelated configuration.
- [GitHub issue #4: Compare direct S3 publishing with Page Hub-mediated publishing](https://github.com/yet-an-other/page-hub/issues/4) — the research question.
- [GitHub issue #6: Choose the publishing control boundary](https://github.com/yet-an-other/page-hub/issues/6) — the decision this report informs.
- [Amazon S3 presigned URLs](https://docs.aws.amazon.com/AmazonS3/latest/userguide/using-presigned-url.html) — primary documentation for upload delegation, expiry, overwrite behavior, checksums, and bearer-token risk.
- [Amazon S3 conditional writes](https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes.html) — primary documentation for create/replace preconditions and concurrent-write responses.
- [Amazon S3 multipart upload](https://docs.aws.amazon.com/AmazonS3/latest/userguide/mpuoverview.html) — primary documentation for direct-upload efficiency, resumability, checksums, and incomplete-upload cleanup/cost.
- [Amazon S3 event ordering and duplicate events](https://docs.aws.amazon.com/AmazonS3/latest/userguide/notification-how-to-event-types-and-destinations.html#event-ordering-and-duplicate-events) — primary documentation showing why notifications support reconciliation but cannot be the sole ordered commit log.
- [Amazon S3 consistency model](https://docs.aws.amazon.com/AmazonS3/latest/userguide/Welcome.html#ConsistencyModel) — primary documentation for per-key atomicity and read-after-write consistency in Amazon S3, subject to compatibility testing here.
- [AWS IAM security best practices](https://docs.aws.amazon.com/IAM/latest/UserGuide/best-practices.html) — primary documentation supporting temporary credentials and least privilege.
- [AWS CLI `s3 sync`](https://docs.aws.amazon.com/cli/latest/reference/s3/sync.html) — primary documentation for MIME inference, optional deletion, and metadata behavior relevant to preserving or replacing the current direct workflow.
- [Amazon S3 Versioning](https://docs.aws.amazon.com/AmazonS3/latest/userguide/versioning-workflows.html) — primary documentation for recovery semantics that are not currently enabled or demonstrated.

### Dropped

- Third-party “S3 versus API” architecture articles were excluded because they are generic and add no project-specific evidence.
- Vendor-specific API gateway limits and pricing were excluded because no Page Hub runtime or hosting platform has been selected.
- Native Amazon S3 guarantees were not treated as observed properties of the current S3-compatible endpoint; they remain design references pending compatibility tests.
