# Publication operation contracts

This document defines the first-release behavior for listing, previewing, describing, moving, and permanently deleting Publications. The catalog remains the authority for managed state. Storage observations can reveal drift, but they do not alter accepted state by themselves. The [legacy adoption and publishing cutover](legacy-adoption-and-publishing-cutover.md) contract defines reconciliation choices and write locks.

## Shared rules

- Page Hub serializes management writes.
- Every mutation carries the entity revision shown to the operator and a generated operation ID.
- A stale entity revision fails without changing the catalog or storage.
- Retrying an operation ID returns that operation's existing status or result. It does not start the mutation again.
- Page Hub creates a durable operation record before a move or deletion touches storage.
- The interface disables repeated submission while an operation is running. Closing its dialog does not cancel it.
- Client-side validation is advisory. Page Hub repeats every check before writing.
- Catalog or manager unavailability fails writes closed.
- A storage mutation that cannot finish or clean up ends as a failed operation. Page Hub observes storage again and reports the Publication through the ordinary `drifted` or `missing` state. It does not create separate incomplete-operation states.
- While a move or deletion is running, its Publication has no other row actions. Once a failed operation has become ordinary drift, the normal state rules apply again.
- A successful move or deletion releases the old route for reuse. Manifest history and deletion tombstones do not reserve routes. An old link may therefore serve another Publication if the route is reused later.

## Inventory

The inventory reads Projects and Publications from the catalog. It does not infer membership from a bucket listing.

- Show every Project, including empty Projects, and every managed Publication, including drifted and missing Publications.
- Group Publications under collapsible Project headers, as in the accepted Variant A prototype.
- Sort Projects and Publications by display name without case sensitivity. Use the exact path as the tie-breaker.
- Search Project and Publication display names, Project prefixes, Publication paths, entry points, and private descriptions. Expand a collapsed Project when it contains a match.
- Do not paginate the first release.
- Show each Publication's accepted manifest size. When it is drifted, show the observed size or size difference in the status detail rather than replacing the accepted value.
- Label the date column `Content changed`. It records the latest accepted content creation or replacement. Adoption uses the original object's modification time. Description edits and moves do not change it.
- Show `in sync`, `drifted`, or `missing` when Page Hub has a current observation. Also show when storage was last checked.

The inventory returns cataloged observations immediately and refreshes storage in the background when it opens. It refreshes again after each storage mutation and while an operation is running. A manual Refresh action starts the same check. An observation older than five minutes is stale. If refresh fails, keep the last values visible with a stale warning rather than displaying zero or hiding Publications.

### Storage usage

The quota meter uses the latest observed size of the entire bucket, not only accepted manifests. It shows:

- exact used and quota values;
- accepted Publication usage;
- unclaimed usage, including unexpected objects; and
- the observation time or an unavailable state.

Project and Publication sizes still come from their accepted manifests. A failed observation displays usage as unavailable if Page Hub has no prior value.

## Preview

The Publication path invokes an authenticated Page Hub preview endpoint in a new tab.

- For an in-sync Publication, the endpoint redirects to its public URL.
- For a drifted or missing Publication, it shows a warning instead of redirecting.
- Page Hub does not embed public Publication content in the private manager.
- The separate public-page icon links directly to the public URL without the Page Hub status check. It uses a new tab and does not give the destination access to the manager tab.

This distinction lets the path reflect managed state while the direct link shows what a public reader currently receives.

## Private descriptions

Projects and Publications each have an optional private description.

- Accept plain text only, with at most 240 Unicode characters after trimming outer whitespace.
- Preserve internal line breaks.
- Reject control characters.
- Render descriptions as text. Do not interpret HTML or Markdown.
- Treat an empty trimmed value as clearing the description.
- Permit description changes while a Publication is in sync, drifted, or missing. A running move or deletion still blocks its Publication row until that operation finishes.

A failed save keeps the entered text in the form. A revision conflict reloads the affected record without discarding that text. After success, Page Hub refreshes the record and reports whether it updated or removed the description.

## Move

### Eligibility and path validation

Only an in-sync Publication with no running storage operation can move.

A move chooses an existing Project and a Publication path. An empty path means the Project root and appears as `Project root` in the interface. A non-root path must:

- contain only lowercase ASCII letters, digits, internal hyphens, and `/` between segments;
- have no leading or trailing slash, repeated slash, dot segment, or encoded separator;
- avoid every reserved manager path; and
- be no longer than 240 characters.

Page Hub preserves the entry-point filename exactly, including case, and preserves the routing mode. It maps every source object to the destination by replacing the old Publication root while retaining the relative object path and serving metadata. A destination identical to the current location is invalid.

### Collision handling

Page Hub rejects a move when:

- the destination route overlaps another active Publication route, including overlap caused by a fallback routing mode;
- any destination object key already exists in storage, even if it is unclaimed or has identical bytes; or
- the destination overlaps the source object set in a way that prevents both layouts from existing during the move.

Page Hub never overwrites, merges, or automatically renames a destination. The error identifies whether a managed route or stored object caused the conflict without exposing object content. Former routes and tombstones are not collisions unless another active Publication or stored object now occupies them.

### Confirmation

Before submission, show:

- the exact old and new public URLs;
- the object count and total accepted size;
- the exact entry point that will be preserved; and
- a statement that the old URL will stop working and no redirect will remain.

The operator must select `I understand that links to the old URL will break.`

### Quota check

A safe move temporarily stores the source and destination copies. Refresh bucket usage before starting and require enough free space for the complete destination manifest. If space is insufficient, write nothing and show the required and available amounts. Never delete source objects first to work around the quota.

### Storage sequence

1. Create the durable operation record and verify the submitted Publication revision.
2. Revalidate every source object against the accepted manifest. Recheck the destination routes, keys, and current quota usage.
3. Stream each source object through Page Hub to its destination key using conditional create. Do not use server-side copy because the current storage endpoint ignores destination copy preconditions.
4. Preserve serving and user metadata. Compute a digest while streaming, download the destination, and compare its bytes and metadata with the source. The storage endpoint's checksum response is not trusted.
5. Revalidate the source objects, delete only the exact accepted source keys, and verify that those keys are absent. Never use recursive prefix deletion.
6. In one catalog transaction, accept the destination manifest revision, update the Publication's Project and path, and mark the operation successful.

The old and new public URLs may both work during the copy phase. A move succeeds only after every destination object passes verification, every old key is absent, and the catalog transaction commits.

If failure occurs before source deletion starts, the source remains authoritative. Page Hub removes only destination objects created by this operation and only after verifying that they still match what the operation wrote. Once source deletion starts, Page Hub continues forward within the operation rather than trying to reconstruct the source. If forward completion or cleanup fails, the operation fails and a new storage observation produces the normal drift or missing state. Unexpected destination objects become unclaimed storage and participate in future collision checks.

## Permanent deletion

### Eligibility

- An in-sync Publication can be permanently deleted.
- A drifted Publication cannot be deleted until reconciliation resolves the mismatch.
- A missing Publication can be removed from the catalog after one fresh storage check confirms that its accepted objects remain absent.
- Deleting the last Publication does not delete its Project.

### Confirmation

Show the public URL, accepted object count, accepted size, and a statement that Page Hub has no trash or recovery. For a missing Publication, state that Page Hub found no stored objects to remove.

Require an exact, case-sensitive Publication path without a leading slash. Do not trim the confirmation input. For example:

- `page-hub/research` for a nested Publication;
- `page-hub/` for a Project-root Publication.

Enable `Delete forever` only when the input matches exactly.

### Storage sequence

For an in-sync Publication:

1. Create the durable operation record and verify the submitted Publication revision.
2. Revalidate every accepted object before deleting any of them. Any mismatch aborts without deletion and becomes an ordinary storage observation.
3. Mark the operation as deleting and remove only the exact accepted keys. Never recursively delete a prefix.
4. Once the first object is removed, continue toward deletion rather than attempting rollback.
5. Verify that every accepted key is absent.
6. In one catalog transaction, create the deletion tombstone and mark the operation successful.

For a missing Publication, repeat the storage check and then create the tombstone without issuing storage deletes.

If deletion stops after removing some objects, the operation fails. Page Hub observes storage and reports the Publication as drifted or missing according to the ordinary reconciliation model. It never reports successful deletion while the Publication remains in the active catalog.

## Restart and recovery

A manager stop or crash never cancels an accepted storage operation. At startup, Page Hub resumes every interrupted move, deletion, or Project move forward until it reaches its durable success or failure result.

- Recovery resumes from the operation record and the accepted manifests. Each remaining step revalidates the exact keys, digests, routes, and quota before acting, exactly as the ordinary storage sequences require.
- Objects attributable to a running operation are not unclassified findings. A scan attributes their keys to the operation record, so they never place a global storage-mutation lock, in strict mode or in coexistence mode. The affected Publication keeps its ordinary observed state.
- Recovery does not wait for the startup observation, because every step revalidates storage itself. Page Hub accepts no new storage mutation until recovery has finished; serialized management writes already provide this.
- On shutdown, Page Hub stops accepting new mutations and exits. Whether the current step reaches a clean boundary within the deployment's stop window is an implementation detail, not a contract.
- The browser keeps following the operation ID across a restart. The affected row keeps showing the running operation until the durable result lands.
- A catalog-only operation, such as a description edit or a rename, is one catalog transaction. An interruption rolls it back with nothing to resume, and the client retries under the ordinary operation-ID rule.

## Errors and feedback

- Invalid fields show field-level messages and preserve every submitted value.
- Revision conflicts, route collisions, quota failures, and storage failures keep the dialog available with the submitted values intact.
- A disconnected client follows the durable operation ID instead of blindly resubmitting.
- A running operation remains visible on the affected row even if its dialog closes.
- After success, Page Hub reloads affected records from the server before changing the inventory.
- Success messages name the result, for example `Moved API review to /briefs/research/` or `Permanently deleted API review`.
- Moves update Project grouping and storage usage. Deletions remove the Publication row and leave an empty Project visible.

## Acceptance scenarios

An implementation of this contract must demonstrate at least these cases:

1. A failed background storage refresh leaves the catalog inventory visible with stale observations.
2. Search finds a private description inside a collapsed Project and expands that Project.
3. A repeated description request with the same operation ID does not apply twice.
4. A preview redirects only for an in-sync Publication, while the direct public link remains available in every state.
5. A move rejects an overlapping route, an unclaimed destination object, and insufficient temporary quota without changing storage.
6. A successful multi-object move preserves relative keys, bytes, metadata, entry-point case, and routing mode, then removes every old key.
7. A move failure before source deletion leaves the source accepted. A cleanup failure appears as ordinary drift with unclaimed destination storage.
8. A deletion revalidation mismatch removes nothing.
9. A partial deletion failure appears through the ordinary drift or missing state and does not create a tombstone.
10. Deleting a missing Publication performs a fresh check, creates a tombstone, and issues no storage delete.
11. A completed move or deletion releases its old route so a later Publication can use it.
12. A manager restart during the copy or deletion phase resumes the operation forward to its durable result, and the objects it wrote never trigger the global lock.
