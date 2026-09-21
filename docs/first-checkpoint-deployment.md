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
PAGE_HUB_S3_ENDPOINT=https://<private-s3-endpoint>
PAGE_HUB_S3_REGION=<s3-region>
PAGE_HUB_S3_BUCKET=<publication-bucket>
PAGE_HUB_S3_ACCESS_KEY_ID=<bucket-scoped-access-key>
PAGE_HUB_S3_SECRET_ACCESS_KEY=<bucket-scoped-secret>
```

The S3 identity must be limited to the configured bucket and only the read
operation required by this checkpoint (`HeadBucket`). Do not put a root, account,
or all-buckets credential in this file. Rotate the file through the secret
injection system, not through Git.

A production service can run as an unprivileged user with a private catalog path
reserved for the later catalog checkpoint:

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
stopping Page Hub leaves the known public Publication URL available. A storage
outage should report `unavailable` or `misconfigured`, never zero usage or bucket
contents.
