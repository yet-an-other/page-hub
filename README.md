# Page Hub

Page Hub is a private manager for Publications served through a separate public reader.

## First checkpoint

The first checkpoint is an authenticated manager shell packaged as one Go binary. It
serves the React manager at `/`, reserves `/_page-hub/`, keeps a private SQLite
catalog, and reports a read-only connectivity status for a configured S3-compatible
bucket.

```sh
make verify
make release VERSION=0.5.0
```

The manager requires `PAGE_HUB_AUTH_ASSERTION_VALUE`, `PAGE_HUB_CATALOG_PATH`,
`PAGE_HUB_STORAGE_QUOTA_BYTES` (the exact bucket quota in bytes; usage is
calculated from bucket observations, never a vendor quota interface), and
`PAGE_HUB_PUBLIC_BASE_URL` (the canonical public origin used for canonical
public URLs and authenticated previews) in production. For local loopback
development only, set `PAGE_HUB_DEV_AUTH_BYPASS=true`. See
[`docs/first-checkpoint-deployment.md`](docs/first-checkpoint-deployment.md) for
reverse-proxy and secret-injection guidance.

## Adopting Publications

Adoption is explicit and read-only against storage. Declare the complete batch
against [`docs/adoption-declaration.schema.json`](docs/adoption-declaration.schema.json)
— every Project, Publication path, object key, entry point, routing mode, and
public-route probe — then plan it, review the digest-bound plan, and commit it
into the catalog as one transaction:

```sh
export PAGE_HUB_S3_ENDPOINT=https://s3.example.invalid
export PAGE_HUB_S3_BUCKET=publications
export PAGE_HUB_S3_ACCESS_KEY_ID=...      # bucket-scoped, read-only
export PAGE_HUB_S3_SECRET_ACCESS_KEY=...
export PAGE_HUB_PUBLIC_BASE_URL=https://share.bdgn.me

page-hub migrate -catalog /var/lib/page-hub/catalog.db
page-hub plan -declaration adoption-batch.json -out plan.json
page-hub commit -plan plan.json -operation-id "$(uuidgen)" -catalog /var/lib/page-hub/catalog.db
```

Planning reads storage, probes the declared public routes, and records exact
keys and case, sizes, modification times, opaque ETags, serving and user
metadata, SHA-256 body digests, canonical public URLs, and probe observations
in a digest-bound plan. The batch must cover the complete bucket: a missing,
unexpected, ambiguously owned, or route-overlapping object rejects the whole
batch. Planning never changes storage or the catalog.

Committing repeats the storage, public-probe, and catalog checks immediately
before one catalog transaction that accepts every declared Project and
Publication or none of them. It never writes, copies, or deletes an object.
Commit refuses to run when `PAGE_HUB_PUBLIC_BASE_URL` differs from the origin
recorded in the approved plan. Reusing an operation ID with the same plan
returns its durable result; reuse with different content fails. The accepted
batch appears in the manager inventory and survives restarts.

## Verifying a release

One repository command qualifies a checkout hermetically — no production
credentials, private configuration, or network access:

```sh
make verify
```

`make verify` formats and lints Go and TypeScript, runs the Go unit and
integration tests, verifies the generated OpenAPI TypeScript types are
current, builds the React manager, embeds it into the Go binary, and runs
the Playwright acceptance suite (desktop and narrow viewports, keyboard and
automated accessibility checks) against that compiled binary.

`make release VERSION=<version>` builds the versioned Linux amd64 binary and
its SHA-256 checksum. The binary reports its version and compatible catalog
schema range:

```sh
page-hub version
```

## Observing storage

After adoption the observation service requests a storage scan at startup,
daily, when the inventory opens, and when the operator selects Refresh — and
only one full scan runs at a time; concurrent triggers join it. Each scan
reports how accepted Publications compare with storage: each Publication is
`in sync`, `drifted`, or `missing`, and the manager separates accepted usage
from unclaimed storage against the configured quota. An observation older
than five minutes is marked stale; a failed scan keeps the last checked
values visible with a warning and never substitutes zero, while storage
outages leave the manager serving its catalog with degraded readiness.
An ordinary observation lists the bucket and compares exact keys, sizes,
ETags, modification times, serving metadata, and user metadata; body
SHA-256 verification happens only when something differs, or when a complete
reconciliation requests it. Unexpected objects stay unclaimed — Page Hub
never attaches them to a nearby Publication — and any unclassified finding
records that later storage mutations would need a global lock. Observations
never change accepted state and never write to storage.

## Checking storage compatibility

`page-hub check` is an opt-in, read-only compatibility check for a configured
S3-compatible endpoint such as RadosGW. It lists the complete bucket and
downloads a bounded sample of object bodies completely, computing their
SHA-256 digests. It requires only the `PAGE_HUB_S3_*` variables — no catalog,
no assertion, no quota — writes nothing to storage, and records nothing, so it
is safe to run against production before adoption. Its failure never alters
storage and is never a requirement for ordinary CI:

```sh
page-hub check
```

It prints a JSON report:

```json
{
  "objects": 3,
  "totalBytes": 74,
  "downloadedKeys": ["docs", "guides/getting-started/app.js", "guides/getting-started/index.html"]
}
```
