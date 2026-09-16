# Current web-share infrastructure constraints

## Purpose and scope

This report maps the behavior around `s3.bdgn.me` and `share.bdgn.me` that constrains placing a private manager at `https://share.bdgn.me/` while keeping publication paths public. It is based on the supplied read-only evidence artifact and primary documentation only. Sensitive values—including credentials, addresses, object keys, publication-prefix names, DNS targets, and unrelated configuration—are intentionally omitted.

The storage endpoint is described here only as **S3-compatible**. No implementation vendor is inferred. Amazon S3 documentation is cited as the authoritative definition of the API semantics being used, but an S3-compatible service is not guaranteed to implement every Amazon S3 behavior unless it is directly observed or separately documented.

## Executive answer

The existing system is a public, prefix-oriented static publication path, not a private application platform. The root of `share.bdgn.me` currently returns a plain 404, the publication contract forbids root objects, known stored objects are anonymously readable through both the reader and the S3 endpoint, arbitrary-origin browser requests are not currently enabled, and the bucket has no demonstrated versioning, recovery, lifecycle, registry, or manager authentication facility.

Keeping `/` private while publication paths remain public therefore requires a routing/authentication boundary **in front of or separate from** the public object-serving behavior. That boundary must protect the manager route, APIs, and private data without accidentally applying publication fallback to `/`; it must also avoid relying on `share.bdgn.me` alone to protect any object that remains anonymously readable from `s3.bdgn.me`. This report does not select a runtime or authentication model.

## Observed behavior

The following statements are direct observations recorded in the supplied evidence artifact.

### Storage and public access

- Authenticated listing found a small, complete inventory consisting only of objects below top-level publication prefixes; there were no root objects.
- A known object was anonymously readable from `s3.bdgn.me` and from its `share.bdgn.me` publication URL.
- Anonymous bucket enumeration was denied. This prevents casual listing but does not make known or discovered object paths private.
- A bucket-policy-status read reported the policy as not public, while direct anonymous object reads succeeded. No bucket-level public-access-block configuration was present. The mechanism granting reads was not identified, so policy status must not be treated as proof that objects are private.
- The local publication contract states that everything in the bucket is world-readable and that publishers must not write at the bucket root.

### Static serving and routing

- `https://share.bdgn.me/` returned `404 text/html`, with no redirect, authentication challenge, or directory listing.
- A random missing root path returned the same shaped 404.
- An existing HTML object returned `200` at its exact object path.
- Both slash and no-slash forms of a sampled publication directory returned `200` with a body identical to that publication's stored index object, without redirecting.
- A missing route below that sampled publication also returned the publication index body with `200`, demonstrating a publication-scoped single-page-application fallback in the sampled case.
- Nested-directory index behavior could not be tested because no suitable nested object existed.
- Successful publication responses supported byte ranges and exposed `ETag`, `Last-Modified`, `Content-Type`, and `Accept-Ranges`. They also carried `X-Robots-Tag: noindex, nofollow`.
- No explicit `Cache-Control` was present on the sampled publication response.

### Authentication and browser boundary

- The sampled root response set no cookie and supplied no `WWW-Authenticate` header. This describes the current 404 only; it is not evidence that no authentication component exists elsewhere.
- No private manager route or authenticated management API was observed.
- The configured command-line principal could perform read-only inventory and configuration calls. Its write, delete, policy, and deployment permissions were not tested.

### CORS

- A publication GET carrying an arbitrary synthetic cross-origin `Origin` succeeded as a normal GET but returned no `Access-Control-Allow-*` headers.
- A reader preflight returned `405` with no CORS headers.
- S3 endpoint preflights for both GET and PUT returned `403` with no CORS headers.
- These tests show that the tested arbitrary origin cannot currently use either endpoint through browser CORS. They do not prove that no other origin is allowed.

### Object model and metadata

- The inventory behaves as a flat object store organized by key prefixes.
- Every object had `Content-Type`; the inventory contained HTML types only. Most objects also had content-encoding metadata.
- All objects exposed size, modification time, ETag, and range support.
- No object had custom user metadata, explicit cache control, content disposition, website redirect metadata, or a version ID.
- The sampled object's tag set was empty. Tag reads worked for the configured principal; tag writes were not tested.
- There is no observed publication registry that records owner, title, visibility, status, or an application-level identifier.

### Versioning, lifecycle, and recovery

- The versioning status was unset, and sampled object heads had no version IDs.
- Lifecycle configuration could not be determined because the command-line client failed while parsing the endpoint response. Lifecycle is therefore **unknown**, not absent.
- No backup, rollback, soft-delete, retention, or object-lock behavior was demonstrated.

### Deployment contract and current publication behavior

- The current publishing contract deploys beneath a publication prefix and forbids root writes.
- The contract states a 30 GiB bucket quota.
- The current tooling can assign standard object metadata, and the command-line sync path infers content type unless overridden.
- No deployment mutation was performed, so atomic multi-object publication, cleanup behavior, cache invalidation, rollback, and concurrent-publisher behavior were not observed.

## Documented guarantees and reference semantics

### Local publication contract

The repository's current web-share contract documents these intended behaviors:

1. bucket contents are world-readable;
2. publications live below project prefixes, never at bucket root;
3. directory requests resolve to an index object;
4. a missing path beneath a publication falls back to that publication's index;
5. the root returns 404;
6. public responses carry `X-Robots-Tag: noindex, nofollow`; and
7. the bucket quota is 30 GiB.

The probes confirmed items 1, 3–5, and 6 for the sampled paths. The prefix-only rule and quota are contract statements, not independently measured guarantees.

### S3 API reference semantics

These are relevant Amazon S3 guarantees and tool behaviors. They are reliable design references, but must not be assumed for this compatible endpoint beyond what testing or endpoint documentation confirms.

- **Flat namespace:** S3 stores objects in a flat namespace; apparent folders are inferred from key prefixes and delimiters. [Naming Amazon S3 objects](https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-keys.html)
- **Metadata:** `Content-Type`, `Content-Encoding`, `Cache-Control`, and similar HTTP fields are object metadata. User-defined metadata can be supplied at upload, but changing metadata generally requires replacing/copying the object rather than patching one metadata field in place. ETags do not always equal an MD5 digest. [Working with object metadata](https://docs.aws.amazon.com/AmazonS3/latest/userguide/UsingMetadata.html)
- **Website serving is public-only in the reference service:** the documented S3 website endpoint supports only publicly readable content, whereas the REST endpoint supports public or private content. The observed HTTPS reader has additional routing behavior and therefore must not be assumed to be a native website endpoint. [Website endpoints](https://docs.aws.amazon.com/AmazonS3/latest/userguide/WebsiteEndpoints.html)
- **Authentication:** S3 API requests normally carry Signature Version 4 authentication in an `Authorization` header or query string; presigned URLs delegate bounded use of the signing principal's permission. [Authenticating requests](https://docs.aws.amazon.com/AmazonS3/latest/developerguide/sig-v4-authenticating-requests.html) and [Presigned URLs](https://docs.aws.amazon.com/AmazonS3/latest/userguide/using-presigned-url.html)
- **CORS is not authorization:** S3 CORS rules control whether a browser may make/read a cross-origin request. Bucket policies and ACLs still apply, and enabling CORS does not grant an S3 action. [Using CORS](https://docs.aws.amazon.com/AmazonS3/latest/userguide/cors.html) and [AWS SDK CORS guidance](https://docs.aws.amazon.com/sdk-for-javascript/v3/developer-guide/cors.html)
- **Versioning:** when enabled, S3 retains prior versions on overwrite and creates a delete marker for an ordinary delete; without versioning, overwrite/deletion recovery must not be assumed. [How S3 Versioning works](https://docs.aws.amazon.com/AmazonS3/latest/userguide/versioning-workflows.html)
- **Lifecycle:** lifecycle rules can transition or expire objects. In a nonversioned bucket, expiration permanently removes the object; processing is asynchronous. [Lifecycle expiration](https://docs.aws.amazon.com/AmazonS3/latest/userguide/lifecycle-expire-general-considerations.html)
- **Sync behavior:** the AWS CLI guesses MIME type by default; `--delete` removes destination objects absent from the source; metadata options apply only to transferred files, so unchanged files do not receive newly specified metadata. [AWS CLI `s3 sync`](https://docs.aws.amazon.com/cli/latest/reference/s3/sync.html)
- **Concurrency controls:** Amazon S3 documents atomic updates per key and conditional writes using ETags, but no cross-object transaction is provided by the cited behavior. Compatibility must be verified before relying on these semantics. [S3 consistency model](https://docs.aws.amazon.com/AmazonS3/latest/userguide/Welcome.html) and [Conditional writes](https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes.html)
- **HTTP caching:** a response without explicit freshness can in some circumstances receive heuristic freshness; `private` prevents shared-cache storage and `no-store` prevents storage by private and shared caches. Authenticated manager responses therefore need an intentional cache policy rather than inheriting the publications' current metadata. [RFC 9111](https://www.rfc-editor.org/rfc/rfc9111.html)

## Inferences and design constraints

The following are reasoned implications, not direct observations.

### 1. The current publication workflow cannot deploy the private root

The root currently has no object, returns 404, and is reserved by contract. Uploading a manager as another publication would place it below a public prefix, not at `/`. Writing a root index would violate the present contract and could also change the root/fallback behavior for all publications. Root placement therefore requires a routing or service change outside the existing prefix-only publishing workflow.

### 2. Privacy must be enforced before public publication fallback

A routing layer must distinguish the exact manager surface from public publication paths before applying publication index or SPA fallback. At minimum, unauthenticated `/` and every manager-only subroute/API must not fall through to a public object or publication index. Conversely, the authentication rule must not unintentionally make existing publication paths private.

This defines a routing requirement, not a choice of proxy, application runtime, identity provider, or session mechanism.

### 3. Protecting only `share.bdgn.me` is insufficient for stored private data

A known stored object is directly readable from `s3.bdgn.me`. Authentication applied only at the reader hostname would be bypassable for the same object at the storage hostname. Private manager data, secrets, session material, and private-only assets therefore cannot be placed in the currently world-readable object set unless direct storage access is also made private. Public publication objects may remain where they are.

A manager's static shell could be non-sensitive, but making only the shell appear behind a login would not protect management operations or data.

### 4. Manager authorization must cover operations, not just page delivery

CORS controls browser access; it does not authorize S3 operations. Direct browser management would additionally require an allowed CORS rule and request-specific authorization. Long-lived storage credentials must not be embedded in browser code. Any eventual design must establish least-privilege, auditable authorization for listing, creating, replacing, and deleting publications, regardless of whether operations are mediated server-side or use bounded delegated requests.

### 5. Current metadata is enough for inventory, not governance

Key, size, modification time, type, and ETag can populate a basic object inventory. They do not identify a publication's owner, display title, visibility, provenance, deployment state, or authorization policy. A manager will need an explicit source of truth or metadata convention if those concepts are required. Because existing objects lack custom metadata and tags, migration/backfill behavior would also need definition.

### 6. Writes need explicit conflict and publication semantics

With versioning unset and no demonstrated transaction across objects, an overwrite or delete may be irreversible, and a multi-file deployment can expose a mixed release if readers observe files while deployment is in progress. ETag-based preconditions, immutable release paths plus a pointer, or another concurrency strategy are possible classes of solution, but compatibility and desired behavior remain undecided.

### 7. Cleanup and recovery cannot depend on lifecycle

Lifecycle could not be read, and versioning is unset. The manager must not promise retention, automatic expiry, rollback, trash, or deletion recovery until those behaviors are explicitly implemented and verified. Enabling lifecycle later could also affect existing objects if rules match them, so public publication prefixes need deliberate scoping.

### 8. Deployment metadata must be deliberate

Current objects have no explicit cache-control metadata, and the sync tool will not update metadata on unchanged files merely because new metadata options are supplied. A manager deployment path therefore needs explicit rules for content type, content encoding, cache policy, replacement, stale-file deletion, and metadata-only changes. Public publication caching and private manager caching should be treated as separate policies.

## Required properties for keeping `/` private while publications stay public

Without selecting an implementation, the infrastructure boundary must provide all of the following:

- route precedence that handles `/` and all manager-only routes before public object fallback;
- authentication and authorization on manager pages and state-changing APIs;
- no private manager state in objects that remain anonymously readable from `s3.bdgn.me`;
- continued anonymous reads for intended publication paths;
- protection against fallback accidentally converting an unauthorized or missing manager route into `200` public content;
- intentional CORS behavior if the browser and management API are cross-origin;
- credentials confined to an appropriate trusted boundary or replaced with narrowly scoped, short-lived delegation;
- explicit cache controls for authenticated/private responses;
- a publication registry or metadata contract if the manager needs concepts not represented by current object metadata;
- defined overwrite, delete, conflict, rollback, and lifecycle semantics; and
- a deployment mechanism authorized to alter root routing, because the current publication skill cannot do so.

These properties are compatible with multiple runtime and authentication designs; this research intentionally does not choose among them.

## Unknowns

The following remain unresolved and should be answered before implementation planning:

1. How DNS, TLS termination, and request routing for `share.bdgn.me` are deployed and changed.
2. Whether the current reader supports an exact-root authenticated route without changing publication fallback behavior.
3. Whether an identity provider, session service, authenticated gateway, or reverse proxy already exists.
4. The mechanism that grants anonymous object reads at `s3.bdgn.me`.
5. The actual bucket CORS and website configuration; client parsing failures prevented reliable reads.
6. The lifecycle configuration and any out-of-band backup or replication.
7. The configured principal's least-privilege write, delete, tag, policy, and deployment permissions.
8. Whether the compatible endpoint guarantees Amazon S3's documented consistency, conditional-write, versioning, and lifecycle semantics.
9. Nested-directory routing, because no suitable object existed for a safe live test.
10. Cache behavior in intermediaries when publication objects omit explicit `Cache-Control`.
11. The deployment process responsible for the reader itself, including rollback and cache invalidation.
12. The required manager ownership model, audit history, publication visibility model, and deletion/retention policy.

## Evidence and source disposition

### Kept

- **Supplied direct evidence artifact** — authoritative for the recorded read-only observations and local publication contract; sensitive raw values were already omitted.
- [Amazon S3: Naming objects](https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-keys.html) — primary reference for the flat key/prefix model.
- [Amazon S3: Object metadata](https://docs.aws.amazon.com/AmazonS3/latest/userguide/UsingMetadata.html) — primary reference for system and user metadata semantics.
- [Amazon S3: Website endpoints](https://docs.aws.amazon.com/AmazonS3/latest/userguide/WebsiteEndpoints.html) — primary reference for static website access and its public-only limitation.
- [Amazon S3: CORS](https://docs.aws.amazon.com/AmazonS3/latest/userguide/cors.html) — primary reference distinguishing browser cross-origin permission from storage authorization.
- [Amazon S3: Signature Version 4](https://docs.aws.amazon.com/AmazonS3/latest/developerguide/sig-v4-authenticating-requests.html) — primary reference for API request authentication.
- [Amazon S3: Versioning](https://docs.aws.amazon.com/AmazonS3/latest/userguide/versioning-workflows.html) — primary reference for overwrite/delete recovery behavior.
- [Amazon S3: Lifecycle expiration](https://docs.aws.amazon.com/AmazonS3/latest/userguide/lifecycle-expire-general-considerations.html) — primary reference for expiry behavior by versioning state.
- [AWS CLI: `s3 sync`](https://docs.aws.amazon.com/cli/latest/reference/s3/sync.html) — primary reference for deployed metadata, MIME inference, and optional deletion behavior.
- [RFC 9111: HTTP Caching](https://www.rfc-editor.org/rfc/rfc9111.html) — standards source for private response caching implications.

### Dropped

- Vendor-identification results and DNS-infrastructure speculation — excluded because the direct evidence does not prove an implementation vendor.
- Third-party tutorials, blog posts, and product comparisons — excluded in favor of official API documentation and standards.
- Search results that merely restated S3 documentation — excluded as redundant.
- Unrelated cloud configuration and sensitive raw probe details — excluded because they do not constrain the root manager and are unsuitable for a public report.
