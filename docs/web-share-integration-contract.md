# `web-share` integration contract

This document defines how the `web-share` skill creates and replaces Publications through Page Hub. Page Hub is the sole authority for accepting these changes. The skill and client may reject work earlier, but Page Hub repeats every objective check before it changes storage.

The first release supports one-file Publications and complete static-site directories. It proxies upload bytes through Page Hub. It does not issue storage credentials or fall back to direct S3 writes when Page Hub is unavailable.

## Compatibility boundary

The skill invokes the first-party `page-hub` CLI. The CLI owns authentication, source packaging, protocol negotiation, operation IDs, status recovery, and machine-readable results. The HTTP protocol between the CLI and Page Hub is versioned, but it is not a separate public client contract in the first release.

Compatibility covers the current user workflow:

- publish one inspected local file and receive its public URL;
- publish an inspected build directory as one Publication;
- choose a clean directory URL or retain a named-file URL;
- replace an existing Publication deliberately; and
- receive a clear result when validation, storage, or public delivery fails.

Compatibility does not cover the current AWS CLI commands, direct-S3 credentials, or overwrite behavior. Page Hub never provides an upsert operation. Creating and replacing are separate commands.

## Client authentication

A publishing client uses a dedicated Page Hub bearer token rather than the browser's oauth2-proxy session. The authenticated manager creates and revokes tokens and displays each secret only once. Page Hub stores a one-way hash of the token. The first release uses manual rotation and does not impose automatic expiry.

A publishing token may:

- resolve an exact public URL or Project prefix to the public routing fields and stable ID needed for publication;
- preflight a create or replacement;
- create a Project and its first Publication in one operation;
- create a Publication in an existing Project;
- replace the complete content of an in-sync Publication; and
- read operations started with that token.

It may not list private catalog data, read private descriptions, edit descriptions, move Publications, permanently delete Publications, adopt storage, reconcile drift, manage tokens, or inspect operations started by another token. Revocation blocks new requests immediately but does not cancel an accepted operation.

The client reads its token from a protected credential file or environment variable. It never accepts the token as a command argument or writes it to output, logs, an archive, or an operation record.

## Client operations

The CLI exposes these logical operations. Exact flag spelling may evolve within a compatible client version, but the input and result contracts remain stable.

### Resolve

Resolve accepts an exact public URL or Project prefix. It returns only the stable ID, exact public path, routing fields, current revision, and current managed state needed to prepare a create or replacement. It does not return private display names or descriptions.

### Plan create

A create plan identifies either:

- an existing Project by stable ID; or
- a new Project by an explicit prefix.

A missing prefix never creates a Project implicitly. When creating a Project, the caller may supply its private display name. The exact prefix is the default. The caller may also supply the Publication display name. Its Publication path or named entry point is the default.

The plan also supplies the Publication path, entry point, routing mode, source manifest, and optional serving metadata. Page Hub validates the proposed route, checks current storage and quota, and returns an opaque plan digest. A plan does not reserve a route or mutate storage.

### Create

Create submits a current plan digest, a new operation ID, the same manifest, and the complete content payload. It fails if the route overlaps an active Publication, any destination key exists, the Project changed incompatibly, or current quota and validation checks no longer pass.

If the operation targets a new Project, Page Hub creates the Project and Publication together in the catalog only after it verifies storage. The records start with empty private descriptions.

### Plan replace

A replacement plan identifies a Publication by stable ID and supplies its current revision. Only an in-sync Publication with no running storage operation is eligible. The Project, Publication path, entry point, and routing mode cannot change during replacement.

Page Hub compares the proposed complete manifest with the accepted manifest and returns:

- added paths;
- replaced paths;
- unchanged paths;
- removed paths; and
- an opaque plan digest bound to the Publication revision and proposed manifest.

The skill shows removed paths before uploading. A clear request to replace or redeploy the Publication authorizes those removals. An ambiguous request to update requires confirmation. Execution fails with `plan_changed` if the revision or computed plan no longer matches.

A plan containing no content or serving-metadata changes returns the existing accepted revision without creating a storage operation.

### Replace

Replace submits the plan digest, operation ID, manifest, and complete payload. The submitted directory is the entire desired Publication. Page Hub does not merge it with the accepted manifest. Paths missing from the new manifest are removed after the replacement objects have passed verification.

### Status

Status returns the durable state and existing result for an operation ID started by the same token. Retrying an accepted operation ID returns or follows that operation. Submitting the same ID with a different request or payload digest fails with `operation_id_reused`.

## Destination and routing

The client sends Project prefix, Publication path, entry point, and routing mode as separate fields. Storage layout never determines managed state or routing implicitly.

- An empty Publication path denotes a Root Publication.
- A named file defaults to `exact-file` routing and retains its filename and casing.
- A file published at a clean directory URL is stored as the explicitly selected index entry point.
- A static directory defaults to `directory-index` routing and `index.html` as its entry point.
- A single-page application uses `fallback` routing only when the skill selects it after inspecting the site.

Project and Publication paths follow the validation rules in [Publication operation contracts](publication-operation-contracts.md). Relative object paths and the entry point retain their exact casing. Page Hub rejects empty segments, dot segments, repeated separators, backslashes, encoded separators, control characters, absolute paths, and paths that escape the Publication root.

Active route scopes cannot overlap. In particular, a fallback Publication occupies its complete route scope and can prevent nested Publications in that scope.

## Source inspection and packaging

The skill inspects the exact source before invoking the CLI. It refuses credentials, private notes, personal data, source trees, and other material that should not become public. For a site, it selects the built output rather than the project root.

The CLI walks only the named source. It accepts regular files and rejects symlinks, sockets, devices, path traversal, `.env` files, private-key files, cloud credential files, and files that change while the client hashes and packages them.

The payload consists of a JSON manifest and content:

- A one-file Publication sends the file body directly.
- A directory sends an uncompressed tar stream with sorted relative paths and normalized archive headers. The archive is transport packaging and is never stored as Publication content.

The manifest records:

- operation and client version fields;
- target identity and expected revision where applicable;
- entry point and routing mode;
- every relative object path, size, and SHA-256 digest; and
- required `Content-Type` plus optional `Content-Encoding` and `Cache-Control` for each object.

The first release rejects arbitrary S3 user metadata from publishing clients. Adoption may retain a legacy Publication's other accepted metadata, but replacing that Publication creates a new manifest under the publishing metadata rules.

The CLI may infer content types and run advisory checks. Page Hub validates every manifest field, archive entry, digest, metadata value, file-count limit, upload-size limit, route, object key, quota check, token permission, and storage precondition. It also confirms that the archive contains exactly the declared files. Heuristic inspection does not prove that content is safe for public release, so the skill remains responsible for that decision.

Page Hub receives the entire payload into a private, bounded operation workspace and validates it before touching publication storage. A truncated or invalid upload changes neither storage nor the catalog. The server advertises its file-count and upload-size limits during preflight, but the server-side checks remain authoritative.

## Storage and acceptance

Page Hub creates a durable operation record after it has received and validated the complete payload and before it changes storage. Management writes remain serialized. The server rechecks the plan, expected revision, accepted objects, destination keys, active routes, and current quota immediately before mutation.

### Create sequence

1. Conditionally create each exact destination key. The entry point is written last.
2. Compute a digest while writing, download each stored object, and compare its bytes and serving metadata with the submitted manifest.
3. In one catalog transaction, create any requested Project, create the Publication, accept its first immutable manifest revision, and mark the operation successful.

If a write or catalog commit fails, Page Hub removes only objects created by this operation and only after confirming that they still match its payload. Cleanup failure leaves unclaimed storage and a failed operation. It does not create a Publication or accepted manifest. Created objects may be reachable by a guessed direct URL before acceptance because the current public reader serves storage directly.

### Replace sequence

1. Revalidate every accepted object and copy its bytes and serving metadata into the private operation workspace before changing storage.
2. Conditionally create added keys. Write replaced non-entry objects and verify their downloaded bytes and metadata.
3. Write and verify the entry point last.
4. Revalidate and delete only the exact removed keys, then verify the complete proposed object set and the absence of removed keys.
5. In one catalog transaction, accept the new immutable manifest revision and mark the operation successful.

The current storage endpoint cannot conditionally replace or conditionally delete an object. Page Hub therefore serializes mutations, requires the Publication to be in sync, revalidates immediately before each irreversible step, and relies on the publishing cutover to remove ordinary out-of-band writers. It does not treat the endpoint's checksum response as proof of stored bytes.

A multi-file replacement is not atomically visible to public readers. A reader may briefly receive a mixture of old and new objects. Writing the entry point last reduces that interval but does not remove it. Static sites should use content-addressed asset names when practical.

If replacement fails, Page Hub restores the accepted bytes and metadata from its private operation workspace and removes added keys that still match this operation. A complete restoration leaves the prior revision accepted and in sync. If restoration or cleanup fails, the operation fails and Page Hub observes storage again. The Publication then appears through the ordinary `drifted` or `missing` state, with unexpected objects counted as unclaimed storage. Page Hub never accepts a partially verified manifest.

Disconnecting or interrupting the CLI does not cancel an operation after Page Hub accepts the complete payload. The operation continues to a durable success or failure result.

## Retry and failure contract

The CLI generates an operation ID before upload and reports it to stderr before sending the body. With `--json`, its final stdout contains exactly one JSON document. Progress and diagnostics go to stderr.

If a connection fails, the CLI queries the operation ID before sending content again:

- If no accepted operation exists, it may resend the complete request with the same ID.
- If the operation is running, it follows that operation.
- If the operation succeeded or failed, it returns the stored result.
- If status cannot be determined, it returns `status_unknown` with the operation ID and does not start another mutation.

The CLI may retry status reads and connections that failed before Page Hub accepted the payload. It never turns a conflict, validation failure, quota failure, drift result, or failed storage operation into a new operation automatically. Page Hub unavailability fails closed and never invokes direct S3 as a fallback.

Every API error is JSON and includes a stable code, plain message, retryability, and an affected field or object path when applicable. Operation errors also include the operation ID and durable state. Stable first-release error classes include:

- authentication and token-scope failures;
- incompatible client or API versions;
- invalid source, manifest, archive, metadata, path, entry point, or routing mode;
- upload-size, file-count, and quota limits;
- stale revisions, changed plans, route conflicts, and object collisions;
- a Publication that is drifted, missing, or already running an operation;
- manager or catalog unavailability;
- storage write, verification, cleanup, or restoration failure; and
- an operation ID reused with different content.

Success returns the operation ID, Publication ID, accepted revision, public URL, and added, replaced, unchanged, and removed paths. Failure preserves enough information for the skill to explain what happened without exposing private catalog data, credentials, source content, or object bodies.

## Public delivery verification

Storage verification is part of acceptance. After acceptance, the CLI also requests the canonical public URL and checks that the configured routing mode reaches the accepted entry point.

A public-reader failure does not roll back accepted storage or catalog state. The command returns a successful publication result with a prominent delivery warning. The skill reports the accepted public URL, local source path, and warning without submitting another mutation.

## Protocol versioning

Publishing endpoints live under `/_page-hub/api/v1`. The CLI sends its client version on every request. Page Hub rejects an incompatible client before reading an upload body. Neither component silently downgrades to direct storage access.

Changes that preserve the documented request fields, result fields, operation semantics, and error meanings may remain in version 1. Breaking changes require a new API version and an explicit skill and CLI migration.

## Acceptance scenarios

An implementation of this contract must demonstrate at least these cases:

1. A publishing token can create and replace content but cannot read descriptions, move, delete, reconcile, or manage tokens.
2. A missing Project prefix never creates a Project unless the request explicitly proposes a new one.
3. A named file retains its filename and casing, while an explicitly clean URL uses the selected index entry point.
4. A directory containing a symlink, undeclared file, changed file, unsafe path, or mismatched digest is rejected before storage changes.
5. Create rejects an active route overlap, an existing destination object, and insufficient quota without accepting a Publication.
6. A disconnected upload that Page Hub never accepted can resend under the same operation ID. A disconnect after acceptance follows the existing operation.
7. Reusing an operation ID with a different manifest or payload fails.
8. Replacement reports its complete diff and rejects a changed revision or plan.
9. Replacement removes paths absent from the submitted complete directory and preserves Project, Publication path, entry point, and routing mode.
10. Create failure removes only matching objects created by that operation. Cleanup failure leaves unclaimed storage and no accepted Publication.
11. Replacement failure restores the prior accepted bytes and metadata when possible. Failed restoration appears through ordinary drift or missing state and never accepts the proposed manifest.
12. Verification downloads stored bytes and detects a corrupt write even when the storage endpoint reports success.
13. A successful operation returns the same result when queried again.
14. Public-reader verification failure returns an accepted result with a delivery warning and starts no compensating mutation.
15. Manager unavailability, an incompatible client, or an authentication failure never falls back to direct S3.
