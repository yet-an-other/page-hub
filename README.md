# Page Hub

Page Hub is a private manager for Publications served through a separate public reader.

## First checkpoint

The first checkpoint is an authenticated manager shell packaged as one Go binary. It
serves the React manager at `/`, reserves `/_page-hub/`, and reports a read-only
connectivity status for a configured S3-compatible bucket.

```sh
make verify
make release VERSION=0.1.0
```

The manager requires `PAGE_HUB_AUTH_ASSERTION_VALUE` in production. For local
loopback development only, set `PAGE_HUB_DEV_AUTH_BYPASS=true`. See
[`docs/first-checkpoint-deployment.md`](docs/first-checkpoint-deployment.md) for
reverse-proxy and secret-injection guidance.
