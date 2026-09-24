# First-checkpoint deployment

This guide describes a portable deployment of the authenticated Page Hub manager.
Replace every value in angle brackets with a value from the deployment environment.
The examples use `page-hub.example.invalid` and a protected Unix socket; they do
not describe a particular homelab.

## Release and secret injection

Install the versioned Linux amd64 binary and verify its checksum before placing it
in the service directory:

```sh
sha256sum --check page-hub-<version>-linux-amd64.sha256
install -o root -g root -m 0755 page-hub-<version>-linux-amd64 /usr/local/libexec/page-hub
```

Keep the assertion and S3 credentials outside the binary, source tree, image, and
logs. For example, a secret manager can render a root-owned file at
`/etc/page-hub/page-hub.env` with mode `0600`:

```dotenv
PAGE_HUB_AUTH_ASSERTION_VALUE=<high-entropy-gateway-shared-value>
PAGE_HUB_CATALOG_PATH=/var/lib/page-hub/catalog.db
PAGE_HUB_STORAGE_QUOTA_BYTES=<exact-bucket-quota-in-bytes>
PAGE_HUB_PUBLIC_BASE_URL=<canonical-public-https-origin>
PAGE_HUB_S3_ENDPOINT=https://<private-s3-endpoint>
PAGE_HUB_S3_REGION=<s3-region>
PAGE_HUB_S3_BUCKET=<publication-bucket>
PAGE_HUB_S3_ACCESS_KEY_ID=<bucket-scoped-access-key>
PAGE_HUB_S3_SECRET_ACCESS_KEY=<bucket-scoped-secret>
```

`PAGE_HUB_STORAGE_QUOTA_BYTES` is required: the manager refuses to start
without a positive integer quota in bytes. Usage is always calculated from
complete bucket observations, never from a vendor-specific quota interface,
so the value must match the quota configured on the storage service.

`PAGE_HUB_PUBLIC_BASE_URL` is required: the canonical public origin every
Publication is served from. The manager joins it with Publication paths to
record canonical public URLs and to redirect authenticated previews. It must
match the origin the adoption plan was committed with.

The S3 identity must be limited to the configured bucket and only the read
operations required by this checkpoint (`HeadBucket` and object reads for
adoption planning). Do not put a root, account, or all-buckets credential in
this file. Rotate the file through the secret injection system, not through Git.

The private catalog lives outside the publication bucket. Create its schema
once with the explicit migration command; the runtime never migrates and
refuses to start against an incompatible or unmigrated catalog:

```sh
sudo -u <page-hub-user> /usr/local/libexec/page-hub migrate -catalog /var/lib/page-hub/catalog.db
```

Administrative commands take the catalog's exclusive lock. Stop the manager
before running `migrate` or `commit`; both refuse to run while the runtime
holds the catalog.

A production service runs as an unprivileged user with a protected Unix socket:

```ini
# /etc/systemd/system/page-hub.service
[Unit]
Description=Page Hub private manager
After=network-online.target
Wants=network-online.target

[Service]
User=<page-hub-user>
Group=<page-hub-group>
ExecStart=/usr/local/libexec/page-hub
EnvironmentFile=/etc/page-hub/page-hub.env
Environment=PAGE_HUB_LISTEN_ADDR=unix:/run/page-hub/page-hub.sock
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/run/page-hub /var/lib/page-hub
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

Create `/run/page-hub` with the service account and the reverse-proxy group as its
only permitted users. Set the socket mode to `0660`; do not expose the manager on a
public TCP listener.

For local development only, an explicit bypass is allowed on a loopback listener:

```sh
PAGE_HUB_DEV_AUTH_BYPASS=true PAGE_HUB_LISTEN_ADDR=127.0.0.1:8080 page-hub
```

The binary rejects that bypass on a non-loopback listener or an externally
reachable socket.

## nginx route precedence and trusted assertion

The manager locations must appear before the anonymous Publication catch-all. The
exact authentication gateway and its subrequest endpoint are deployment choices;
the important boundary is that the gateway overwrites the assertion and nginx
never forwards a client-supplied value.

The following is a shape to adapt, not a drop-in provider configuration:

```nginx
upstream page_hub_manager {
    server unix:/run/page-hub/page-hub.sock;
}

# The auth subrequest admits the configured operator and returns the opaque
# assertion in X-Page-Hub-Assertion. It must strip that header from its client
# request before writing the trusted response header.
location = /_page_hub_gateway_auth {
    internal;
    proxy_pass http://<browser-auth-gateway>;
    proxy_set_header X-Page-Hub-Assertion "";
}

# Exact manager document, reserved manager assets, APIs, and health checks all
# stay ahead of the public Publication reader.
location = / {
    auth_request /_page_hub_gateway_auth;
    auth_request_set $page_hub_assertion $upstream_http_x_page_hub_assertion;
    proxy_set_header X-Page-Hub-Assertion $page_hub_assertion;
    proxy_set_header X-Page-Hub-Identity "";
    proxy_set_header X-Forwarded-User "";
    proxy_set_header X-Forwarded-Email "";
    proxy_set_header X-Auth-Request-User "";
    proxy_set_header X-Auth-Request-Email "";
    proxy_set_header Authorization "";
    proxy_set_header Cookie "";
    proxy_intercept_errors on;
    error_page 500 502 504 =503 /_page_hub_manager_unavailable;
    proxy_pass http://page_hub_manager;
}

# Reserve the bare prefix too. Without this exact location nginx's public
# catch-all would handle /_page-hub before Page Hub can redirect it.
location = /_page-hub {
    auth_request /_page_hub_gateway_auth;
    auth_request_set $page_hub_assertion $upstream_http_x_page_hub_assertion;
    proxy_set_header X-Page-Hub-Assertion $page_hub_assertion;
    proxy_set_header X-Page-Hub-Identity "";
    proxy_set_header X-Forwarded-User "";
    proxy_set_header X-Forwarded-Email "";
    proxy_set_header X-Auth-Request-User "";
    proxy_set_header X-Auth-Request-Email "";
    proxy_set_header Authorization "";
    proxy_set_header Cookie "";
    proxy_intercept_errors on;
    error_page 500 502 504 =503 /_page_hub_manager_unavailable;
    proxy_pass http://page_hub_manager;
}

location ^~ /_page-hub/ {
    auth_request /_page_hub_gateway_auth;
    auth_request_set $page_hub_assertion $upstream_http_x_page_hub_assertion;
    proxy_set_header X-Page-Hub-Assertion $page_hub_assertion;
    proxy_set_header X-Page-Hub-Identity "";
    proxy_set_header X-Forwarded-User "";
    proxy_set_header X-Forwarded-Email "";
    proxy_set_header X-Auth-Request-User "";
    proxy_set_header X-Auth-Request-Email "";
    proxy_set_header Authorization "";
    proxy_set_header Cookie "";
    proxy_intercept_errors on;
    error_page 500 502 504 =503 /_page_hub_manager_unavailable;
    proxy_pass http://page_hub_manager;
}

location = /_page_hub_manager_unavailable {
    internal;
    default_type application/json;
    add_header Cache-Control "no-store" always;
    return 503 '{"error":"manager_unavailable"}';
}

# This remains independent of Page Hub. It must not be used as an error fallback
# for either manager location.
location / {
    proxy_set_header X-Page-Hub-Assertion "";
    proxy_set_header X-Page-Hub-Identity "";
    proxy_set_header X-Forwarded-User "";
    proxy_set_header X-Forwarded-Email "";
    proxy_set_header X-Auth-Request-User "";
    proxy_set_header X-Auth-Request-Email "";
    proxy_set_header Authorization "";
    proxy_set_header Cookie "";
    proxy_pass http://<independent-public-publication-reader>;
}
```

Use the selected gateway's JSON error handling for browser APIs rather than
redirecting an API request to an HTML sign-in page. Confirm that the gateway
removes client cookies, identity headers, authorization headers, and assertion
headers before adding its own trusted assertion. Page Hub compares the configured
assertion in constant time on every manager request. A missing, invalid, or
client-spoofed assertion receives `401` and never enters the public reader.

Keep the manager's private responses on `Cache-Control: no-store`. The public
reader owns its own anonymous cache policy. A stopped or unhealthy Page Hub
process must produce a manager error, not public Publication content; the
independent reader must continue serving existing Publications.

## Post-deploy checks

Run these checks from an administrative network path. Keep the assertion in the
secret manager or shell environment and do not paste it into logs or shell
history:

```sh
export PAGE_HUB_ASSERTION=<read-from-secret-manager>

# Missing assertion is rejected and does not return a Publication.
curl --silent --show-error --include https://page-hub.example.invalid/ \
  | grep -E '^HTTP/|401'

# The authenticated manager and its API are reachable.
curl --fail --silent --show-error \
  -H "X-Page-Hub-Assertion: ${PAGE_HUB_ASSERTION}" \
  https://page-hub.example.invalid/
curl --fail --silent --show-error \
  -H "X-Page-Hub-Assertion: ${PAGE_HUB_ASSERTION}" \
  https://page-hub.example.invalid/_page-hub/api/v1/status
curl --fail --silent --show-error \
  -H "X-Page-Hub-Assertion: ${PAGE_HUB_ASSERTION}" \
  https://page-hub.example.invalid/_page-hub/api/v1/inventory

# Health output contains only status fields; inspect it without printing secrets.
curl --fail --silent --show-error \
  -H "X-Page-Hub-Assertion: ${PAGE_HUB_ASSERTION}" \
  https://page-hub.example.invalid/_page-hub/healthz

# The public reader remains anonymous and independent.
curl --fail --silent --show-error \
  https://page-hub.example.invalid/<known-public-publication-path>
```

Also verify that an invalid assertion is rejected, a manager asset is served only
under `/_page-hub/`, the configured bucket reports `reachable` in the manager, and
stopping Page Hub leaves the known public Publication URL available. The
inventory API returns the cataloged Projects and Publications; an empty catalog
returns an empty list, not an error. A storage outage should report `unavailable`
or `misconfigured`, never bucket contents.

## Adopting the first declared Publication

Adoption is a two-command, storage-read-only workflow. Declare the candidate in
a JSON file that validates against
[`adoption-declaration.schema.json`](adoption-declaration.schema.json)
and name every object exactly:

```sh
# Plan the candidate. This reads storage and writes a reviewable plan;
# it never changes storage or the catalog.
/usr/local/libexec/page-hub plan \
  -declaration /etc/page-hub/adoption/first-publication.json \
  -out /tmp/first-publication-plan.json
```

Review the plan: its `digest` binds the exact observed keys, sizes, ETags,
modification times, metadata, and body digests. Then stop the manager, commit
the approved plan, and start the manager again:

```sh
/usr/local/libexec/page-hub commit \
  -plan /tmp/first-publication-plan.json \
  -operation-id "$(uuidgen)" \
  -catalog /var/lib/page-hub/catalog.db
```

Commit rechecks the candidate against storage immediately before its single
catalog transaction: any changed byte, metadata value, or key set rejects the
commit without accepting anything. A committed Publication appears in the
manager inventory and survives a restart. Re-running `commit` with the same
operation ID and plan returns the recorded result instead of duplicating it.
