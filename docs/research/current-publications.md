# Research: Current web-share Publications inventory

## Question

What top-level prefixes, object layouts, sizes, and naming edge cases already exist in the web-share bucket, and which can Page Hub safely adopt as Publications without republishing or changing their URLs?

## Summary

The current bucket contains 10 objects under 10 distinct top-level prefixes, with no root-level objects: six prefixes contain a single `index.html`, and four contain a single differently named HTML file. All 10 prefixes can be registered non-destructively as Publication management boundaries while preserving their existing object keys and direct object URLs. Only the six index-backed prefixes already behave as clean-root Publications; the four named-file cases require Page Hub to preserve a named entry point because their prefix roots currently return 404.

This conclusion is limited to the current visible objects and observed routing. It does not establish a product requirement that every Publication must have a clean-root URL, nor does it establish support for multi-file sites.

## Method and timestamp

Primary observations were captured at **2026-09-16T22:28:33Z** by a read-only inventory run against the existing web-share bucket:

- AWS CLI `s3api list-objects-v2` enumerated current objects.
- AWS CLI `s3api head-object` collected object metadata without downloading bodies.
- A local script reduced temporary listing data to aggregate counts and name-shape summaries; temporary raw data was mode-restricted and deleted.
- Public HTTP `HEAD` probes covered every direct object URL, each prefix with and without a trailing slash, one confirmed-absent child per prefix, the bucket root, and one confirmed-absent top-level route. There were 42 probes and no request errors.
- Exact keys, prefix names, URLs, credentials, addresses, and private infrastructure details are intentionally omitted from this public report.

A control listing reported 10 keys and `IsTruncated: false`. AWS documents that `ListObjectsV2` returns up to 1,000 objects per request and that `IsTruncated: false` means all matching results were returned, so the single response was complete for current visible keys. S3 prefixes are leading key strings rather than real directories; this report therefore uses “top-level prefix” as a logical management boundary, not as a storage-directory claim. [ListObjectsV2 API](https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectsV2.html) · [Organizing objects using prefixes](https://docs.aws.amazon.com/AmazonS3/latest/userguide/using-prefixes.html)

## Findings

### Inventory and layouts

| Measure | Observation |
|---|---:|
| Current visible objects | 10 |
| Distinct top-level prefixes | 10 |
| Objects at bucket root | 0 |
| Prefixes with exactly one object | 10 |
| Prefixes containing `<prefix>/index.html` | 6 |
| Prefixes containing `<prefix>/<named-file>.html` | 4 |
| Objects nested more than one segment below a prefix | 0 |
| Directory-marker objects | 0 |
| Zero-byte objects | 0 |
| Total current object bytes | 311,737 bytes (about 304.4 KiB) |

There is no current example of a multi-file site, asset tree, nested index, or mixed file-type Publication.

### Sizes

Across all 10 objects:

- minimum: **14,361 bytes**
- median: **27,455 bytes**
- p90: **48,780 bytes** (nearest-rank, over this small population)
- maximum: **56,586 bytes**

The six index-backed objects total **137,645 bytes**, range from **14,361 to 42,586 bytes**, and have a median of **19,288.5 bytes**. The four named-file objects total **174,092 bytes**, range from **27,549 to 56,586 bytes**, and have a median of **44,978.5 bytes**.

Current objects had S3 `LastModified` timestamps spanning **2026-09-08T11:58:47.482Z through 2026-09-14T09:23:04.879Z**.

### Prefix and key naming

- All 10 top-level prefixes match the existing contract shape `[a-z0-9-]+`.
- Prefix lengths range from 5 to 44 characters, with a median of 20.5.
- Nine prefixes contain a hyphen; two contain a digit.
- No prefix begins or ends with a hyphen, and none contains a repeated hyphen.
- Every object has an `.html` extension.
- Six leaves are exactly `index.html`; the other four leaf names are distinct.
- Nine leaf names contain only lowercase letters, digits, hyphens, and dots. **One leaf contains an uppercase character.** S3 object keys are case-sensitive, so import and lookup logic must preserve its exact casing. [Naming Amazon S3 objects](https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-keys.html)
- No observed key contains spaces, underscores, non-ASCII characters, percent or plus signs, query- or fragment-like characters, backslashes, repeated slashes, empty path segments, or leading/trailing segment whitespace.

### Metadata and public behavior

- Stored `Content-Type` is `text/html` for nine objects and `text/html; charset=utf-8` for one.
- No object has stored `Cache-Control` or `Content-Disposition` metadata.
- Successful direct-object responses have content lengths equal to their S3 object sizes.
- Every tested response, including 404s, carries `Content-Type: text/html` and `X-Robots-Tag: noindex, nofollow`.
- No tested route redirects.

Routing differs by layout:

1. **Six index-backed prefixes:** the prefix without a slash, with a trailing slash, and the direct index-object URL all return 200. A confirmed-absent child under each prefix also returns 200 with the same content length as that prefix’s stored index object. This is direct evidence of current SPA-style fallback.
2. **Four named-file prefixes:** the direct object URL returns 200, while the prefix root (with or without a trailing slash) and a confirmed-absent child return 404.
3. The bucket root and a confirmed-absent top-level route return 404.

`HeadObject` is suitable for the metadata portion of this inventory because AWS defines it as retrieving object metadata without returning the object body. [HeadObject API](https://docs.aws.amazon.com/AmazonS3/latest/API/API_HeadObject.html)

## Adoption categories

### A. Adoptable as clean-root Publications without URL or routing changes — 6 prefixes

These prefixes already have a one-object `index.html` layout and serve successfully at both forms of the prefix root. Page Hub can register each existing prefix as a Publication boundary without copying, moving, renaming, or re-uploading its object.

Their existing direct index URLs must remain valid. Their observed missing-child fallback is also existing behavior that an adoption path should not accidentally remove.

### B. Adoptable only with a preserved named entry point — 4 prefixes

These prefixes can also be registered without republishing because each is isolated and its existing named-file URL already works. Safe adoption requires the Publication model or importer to retain the exact named HTML key, including case.

They are **not** currently clean-root Publications: both forms of the prefix root return 404. Making the root render would be a behavior change—such as adding an index object, redirect, or routing rule—even if the old direct URL remained available. Whether Page Hub should make that change is product policy for a later ticket.

### C. Rejected due to conflicting current layout — 0 prefixes

The inventory found no root objects, shared-prefix layouts, cross-prefix dependencies visible from key layout, directory markers, or nested objects that would force a move or rename merely to establish a Publication management boundary.

## Naming and routing edge cases

1. **Uppercase leaf character:** one existing named file requires case-preserving storage, import, and URL generation. Normalizing leaf names to lowercase would change its URL.
2. **Two entry-point forms:** the existing population includes both `index.html` and named HTML entry points. Assuming one universal leaf name would exclude or mutate four publications.
3. **Root URL asymmetry:** named-file publications have valid direct URLs but no working prefix-root URL. “Adoptable” must not be conflated with “already clean-root.”
4. **SPA fallback is real but layout-specific:** the six index-backed prefixes serve the index content for tested absent children; the four named-file prefixes do not. A global fallback rule would alter current behavior for the latter group.
5. **No redirect precedent:** none of the tested routes redirects, so migration through redirects would introduce behavior not demonstrated by the current bucket.
6. **Metadata variation:** both `text/html` and `text/html; charset=utf-8` exist. An importer should avoid rewriting metadata merely to register ownership unless a later policy explicitly requires normalization.
7. **Untested key complexity:** current names are relatively conservative. The absence of spaces, Unicode, percent signs, repeated slashes, and similar cases is not evidence that Page Hub supports or should forbid them.

## Decision implications

These are constraints exposed by the inventory, not product-policy decisions:

- **A non-destructive adoption operation is feasible for all 10 prefixes** if adoption means registering an existing logical prefix and preserving every existing key and URL.
- The data model needs to be capable of representing either an index entry point or an exact named-file entry point if all 10 are to be adopted without mutation.
- Any URL resolver introduced during adoption should preserve the observed distinction between index-backed fallback and named-file 404 behavior unless a later decision intentionally changes it.
- Import logic should preserve object-key casing and existing metadata; it should not infer that registration requires a copy or upload.
- A requirement that every Publication’s canonical URL be its prefix root would reduce the immediately compatible set to six. Deciding whether named entry points are acceptable, or whether canonical redirects should be added, belongs to later design work.
- No current evidence validates multi-file-site assumptions. Asset trees, nested indexes, relative links, and mixed MIME types need separate examples or fixtures before they can shape a general Publication contract.

## Unknowns and limits

- `ListObjectsV2` establishes the current visible keys, not historical versions, delete markers, incomplete multipart uploads, or prior layouts. Bucket versioning state was not inventoried.
- The probes used HTTP `HEAD`; they establish observed status, headers, and content length at that time, but not body equivalence, HTML validity, internal links, asset references, or runtime behavior in a browser.
- Content ownership, author intent, desired titles, and whether each object should be managed by Page Hub were not established. Technical adoptability is not authorization or a product decision.
- Cache behavior cannot be inferred solely from the absence of object-level `Cache-Control`; intermediary configuration was not investigated.
- The sample is small and contains only single-file HTML. It cannot establish production limits or compatibility for larger or multi-object Publications.
- The observation timestamp and results are a point-in-time snapshot; concurrent or later bucket changes would require a fresh inventory.

## Sources

### Kept

- **Direct read-only inventory evidence (private run artifact, summarized and redacted above)** — primary evidence for counts, layouts, sizes, metadata, naming shapes, and observed HTTP behavior. The local artifact is intentionally not linked from a public report because operational details must not be published.
- [**ListObjectsV2 — Amazon S3 API Reference**](https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectsV2.html) — defines response limits, `KeyCount`, and `IsTruncated`, supporting the completeness interpretation.
- [**HeadObject — Amazon S3 API Reference**](https://docs.aws.amazon.com/AmazonS3/latest/API/API_HeadObject.html) — establishes that metadata can be retrieved without returning the object body and documents version-related limits.
- [**Naming Amazon S3 objects — Amazon S3 User Guide**](https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-keys.html) — authoritative context for the flat key namespace, case-sensitive keys, logical prefixes, and naming hazards.
- [**Organizing objects using prefixes — Amazon S3 User Guide**](https://docs.aws.amazon.com/AmazonS3/latest/userguide/using-prefixes.html) — confirms that prefixes are leading key strings, not directories.

### Dropped

- Search-result commentary and third-party S3 explainers were excluded because AWS primary documentation covers the limited interpretation needed here.
