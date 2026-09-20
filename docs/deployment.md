# Deployment contract

Page Hub is a standalone application. It does not depend on a particular DNS provider, reverse proxy, identity provider, object-store vendor, or infrastructure repository. A deployment may use any external environment that satisfies the contracts below.

The deployment has two different access paths on one public origin:

```text
                         +-----------------------+
operator browser ------> | DNS, TLS, and router  | ------> browser authentication ------> Page Hub
publishing client -----> |                       | ------> Page Hub token ---------------> Page Hub
public reader ----------> |                       | ------> public reader ----------------> object storage
                         +-----------------------+
                                                                       |
Page Hub --------------------------------------------------------------+
   |                                                                   |
   +----> private catalog                                              +----> Publication objects
```

Page Hub owns management behavior and all accepted mutations. The hosting environment owns DNS, TLS termination, request dispatch, and the connection to its authentication system. Public Publication reads must remain available when the Page Hub management runtime is unavailable.

## External dependencies

A deployment must provide the following components.

| Dependency | Required behavior |
| --- | --- |
| Public DNS | Resolve the configured Page Hub origin to the TLS entry point. DNS records and providers are environment-specific. |
| TLS endpoint | Present a valid certificate for the origin, redirect or reject plaintext HTTP, and renew the certificate without manual application changes. TLS may terminate at the router or at the application host. |
| Request router | Match manager routes before Publication routes. It must support different authentication policies on the same origin. |
| Authentication | Admit only the configured operator to the browser manager. Admit publishing clients only through dedicated, revocable Page Hub tokens with the scopes defined by the [`web-share` integration contract](web-share-integration-contract.md). Browser authentication may use a gateway or Page Hub implementation. |
| Public Publication reader | Serve accepted Publication URLs without manager authentication. It may be a separately deployed Page Hub component or an external object-store proxy. It must remain available when the management runtime is down. |
| S3-compatible object storage | Store Publication objects and expose the operations required by Page Hub. Page Hub needs credentials scoped to the configured bucket. The endpoint must pass the compatibility checks for every write, copy, delete, checksum, and precondition that the implementation uses. The S3 API itself does not need to be public. |
| Private catalog store | Persist Projects, Publications, accepted manifests, descriptions, operation records, observations, and tombstones. It must not be stored in the public Publication bucket. |
| Secret injection | Supply storage credentials, catalog credentials, session or authentication secrets, and any provider credentials without placing them in images, source control, logs, or public objects. |
| Backup and restore | Back up the private catalog and test restoration. Storage alone cannot reconstruct stable identities, private descriptions, accepted history, or authorization decisions. |

Production deployments also need logs, health checks, and alerts for the Page Hub runtime, catalog, router, and object store.

## Routing contract

The public origin hosts a private manager at `/` and public Publications below top-level paths. The router must apply routes in this order:

1. authentication callbacks and other routes required by the selected browser authentication mechanism;
2. the exact manager page, manager assets, browser management API routes, and token-authenticated publishing API routes;
3. public Publication routes;
4. a final `404` for anything not handled above.

The implementation must publish its complete set of reserved manager paths. The router and Page Hub validation must use the same set. Page Hub must reject a Project path that conflicts with a reserved path.

The router must satisfy these rules:

- Unauthenticated requests to manager pages and browser APIs receive the configured browser authentication response. Token-authenticated publishing APIs return a JSON authentication error. Neither path falls through to a Publication.
- Publication URLs remain anonymously readable and do not trigger manager authentication.
- A missing manager route returns an authenticated application error or `404`. Publication index fallback must not handle it.
- State-changing management requests reach only Page Hub. Publishing clients do not write accepted Publication state by bypassing Page Hub.
- Private catalog data, credentials, session material, and private assets never enter a publicly readable object set.
- Authenticated responses use an intentional private cache policy. Public Publication cache policy is configured separately.

A deployment may implement routing with a reverse proxy, ingress controller, load balancer, edge worker, or application router. Page Hub relies on the behavior above, not on a specific product.

## Publication read compatibility

The public reader must preserve the URL behavior recorded by each accepted Publication manifest:

- exact object-key spelling and casing;
- the configured entry point;
- exact-file, directory-index, and Publication fallback behavior where selected;
- stored serving metadata such as content type and content encoding; and
- anonymous access only to public Publication content.

Adopting an existing Publication must not copy, rename, or rewrite its objects. The deployment must keep its existing public URL working. If an external reader already provides the required behavior, Page Hub may continue using it instead of replacing it.

## Authentication boundary

Browser authentication may run at the router or inside Page Hub. Publishing-token authentication runs inside Page Hub. The deployment must enforce these boundaries:

- every manager page and browser management API request requires the configured operator identity;
- every publishing API request requires a dedicated Page Hub token with the needed scope;
- every state-changing request is authorized on the server;
- browser sessions use secure cookie settings and protection against cross-site request forgery when cookie authentication is used;
- forwarded identity headers are accepted only from a trusted authentication component and are overwritten at that boundary;
- browser routes strip client-supplied authorization headers before trusting the authentication component;
- publishing routes strip cookies and client identity headers, then forward the bearer token to Page Hub only over the protected upstream connection;
- Page Hub stores only one-way hashes of publishing tokens and never logs token values;
- CORS is not treated as authentication or authorization;
- object-store credentials remain on a trusted server and are never sent to browser or publishing client code; and
- signing out invalidates the Page Hub browser session and, where supported, the identity-provider session.

The choice between an authentication gateway and application-managed browser authentication belongs to the runtime and authentication decision. The publishing-token contract remains the same in either arrangement.

## Runtime configuration

Environment variable and configuration-file names will be fixed with the implementation. The deployment must be able to provide these values:

- the canonical HTTPS origin;
- the reserved manager route set;
- object-store endpoint, region or compatibility settings, bucket name, and scoped credentials;
- private catalog connection and migration settings;
- authentication issuer, client identity, operator allowlist, callback URL, and session secrets where applicable;
- trusted proxy settings and route-specific browser and publishing authentication behavior;
- upload-size, file-count, operation-workspace, and storage-quota limits; and
- backup destination and retention settings.

Changing the canonical origin, reserved paths, or storage bucket is a migration. Do not treat those values as harmless runtime toggles.

## Deployment order

1. Provision object storage and the private catalog store.
2. Configure catalog backups and perform a restore test.
3. Install catalog migrations before starting a runtime version that requires them.
4. Deploy Page Hub with storage credentials limited to its bucket and required operations.
5. Configure browser authentication, publishing-token routing, and the ordered route rules on a non-public or temporary origin.
6. Verify manager isolation, publishing-token scopes, and public Publication behavior.
7. Point public DNS at the tested TLS entry point.
8. Adopt existing Publications without changing their objects or URLs.
9. Issue the publishing client token and move mutation clients to Page Hub only after the management path is healthy.

Rollback must leave the public reader and Publication objects in place. Roll back the Page Hub runtime and route changes together when their contracts differ. Restore the catalog only through a documented recovery procedure, then reconcile it with observed storage before accepting more mutations.

## Deployment checks

Run these checks before directing production traffic to a release:

- The TLS certificate validates for the configured origin.
- Plaintext HTTP redirects to HTTPS or is rejected.
- An unauthenticated `GET /` starts authentication or returns `401` or `403`.
- The configured operator can load the manager and call its read-only API.
- An unauthenticated state-changing API request is rejected.
- A publishing token can create and replace content but cannot read private descriptions, move, delete, reconcile, or manage tokens.
- Browser credentials do not authenticate the publishing client route, and publishing tokens do not authenticate browser manager routes.
- A known Publication URL remains anonymously readable with the expected body and metadata.
- Slash, entry-point, nested-index, and fallback behavior match that Publication's accepted routing mode.
- A missing or unauthorized manager path never returns Publication content.
- A conflicting Project path is rejected before any object write.
- Stopping the Page Hub management runtime does not take existing Publication reads offline.
- A catalog outage causes management writes to fail closed.
- Logs and health responses contain no credentials, session material, private descriptions, or object contents.
- The latest catalog backup can be restored in an isolated environment.

## Environment-specific documentation

Each deployment should keep a private runbook that records:

- the DNS record owner and change procedure;
- where TLS terminates and how renewal works;
- the router configuration source and deployment command;
- the browser authentication provider, operator admission rule, and recovery procedure;
- the publishing-token issuance, client installation, revocation, and rotation procedure;
- the object-store bucket, credential owner, and rotation procedure;
- the catalog location, migration command, backup schedule, and restore command; and
- rollback and incident contacts.

That runbook belongs to the deployment environment. It is not a Page Hub source dependency and should not be copied into this public repository if it contains private infrastructure details.
