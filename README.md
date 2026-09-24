# Page Hub

Page Hub is a private manager for Publications served through a separate public reader.

## First checkpoint

The first checkpoint is an authenticated manager shell packaged as one Go binary. It
serves the React manager at `/`, reserves `/_page-hub/`, keeps a private SQLite
catalog, and reports a read-only connectivity status for a configured S3-compatible
bucket.

```sh
make verify
make release VERSION=0.3.0
```

The manager requires `PAGE_HUB_AUTH_ASSERTION_VALUE`, `PAGE_HUB_CATALOG_PATH`,
and `PAGE_HUB_STORAGE_QUOTA_BYTES` (the exact bucket quota in bytes; usage is
calculated from bucket observations, never a vendor quota interface) in
production. For local loopback development only, set
`PAGE_HUB_DEV_AUTH_BYPASS=true`. See
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

## Observing storage

After adoption the runtime requests a storage observation in the background at
startup, and the manager reports how accepted Publications compare with
storage: each Publication is `in sync`, `drifted`, or `missing`, and the
manager separates accepted usage from unclaimed storage against the configured
quota. An ordinary observation lists the bucket and compares exact keys,
sizes, ETags, modification times, serving metadata, and user metadata; body
SHA-256 verification happens only when something differs, or when a complete
reconciliation requests it. Unexpected objects stay unclaimed — Page Hub
never attaches them to a nearby Publication — and any unclassified finding
records that later storage mutations would need a global lock. Observations
never change accepted state and never write to storage.
