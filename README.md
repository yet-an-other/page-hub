# Page Hub

Page Hub is a private manager for Publications served through a separate public reader.

## First checkpoint

The first checkpoint is an authenticated manager shell packaged as one Go binary. It
serves the React manager at `/`, reserves `/_page-hub/`, keeps a private SQLite
catalog, and reports a read-only connectivity status for a configured S3-compatible
bucket.

```sh
make verify
make release VERSION=0.2.0
```

The manager requires `PAGE_HUB_AUTH_ASSERTION_VALUE` and `PAGE_HUB_CATALOG_PATH`
in production. For local loopback development only, set
`PAGE_HUB_DEV_AUTH_BYPASS=true`. See
[`docs/first-checkpoint-deployment.md`](docs/first-checkpoint-deployment.md) for
reverse-proxy and secret-injection guidance.

## Adopting Publications

Adoption is explicit and read-only against storage. Declare the candidate
against [`docs/adoption-declaration.schema.json`](docs/adoption-declaration.schema.json),
plan it, review the digest-bound plan, and commit it into the catalog:

```sh
export PAGE_HUB_S3_ENDPOINT=https://s3.example.invalid
export PAGE_HUB_S3_BUCKET=publications
export PAGE_HUB_S3_ACCESS_KEY_ID=...      # bucket-scoped, read-only
export PAGE_HUB_S3_SECRET_ACCESS_KEY=...

page-hub migrate -catalog /var/lib/page-hub/catalog.db
page-hub plan -declaration first-publication.json -out plan.json
page-hub commit -plan plan.json -operation-id "$(uuidgen)" -catalog /var/lib/page-hub/catalog.db
```

Planning never changes storage or the catalog. Committing rechecks storage
against the approved plan digest immediately before one catalog transaction and
never writes, copies, or deletes an object. The committed Project and
Publication appear in the manager inventory and survive restarts.
