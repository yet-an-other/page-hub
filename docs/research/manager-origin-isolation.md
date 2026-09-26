# Manager origin isolation

Research for yet-an-other/page-hub#23. Public report: no private deployment values appear here. Deployment facts come from the public repository (CONTEXT.md, ADR-0002, docs/deployment.md, docs/routing-deployment.md, internal/server/server.go); the deployment is otherwise described generically (the share origin, the SSO origin, the object-storage reader).

## Question

Which browser and server mechanisms can stop a published page, served from the same origin as the manager, from making authenticated manager requests? What does each cost in routing, authentication, certificates, and compatibility with existing Publications?

Threat model, stated precisely: a published page runs **same-origin script**, not a cross-site attacker. It can issue `fetch()`/XHR to `/` and `/_page-hub/…` with cookies attached (same-origin requests carry credentials), read every response, read any token rendered in the manager DOM, and set any request header JavaScript is allowed to set — everything except the [Fetch-spec forbidden headers](https://fetch.spec.whatwg.org/) (`Cookie`, `Origin`, `Set-Cookie`, and anything starting with `proxy-` or `sec-`). Mechanisms that only distinguish "cross-site" from "same-site" therefore do not address this ticket.

## Summary

On a single origin, nothing in the browser distinguishes manager code from publication code, so every CSRF-grade defense (SameSite, Origin checks, Fetch Metadata, CSRF tokens, custom headers) is defeated by definition. Genuine isolation requires splitting the two trust domains by origin: moving the **manager** to a dedicated origin (best fit, publications untouched) or giving public responses **CSP `sandbox`** without `allow-same-origin` (opaque origin; strong but breaks storage-, cookie-, and service-worker-dependent publications). Independently of that choice, a top-level service-worker file published at a root path can claim scope `/` and persistently intercept manager URLs — Page Hub should reserve those paths regardless.

## Candidate mechanisms

### 1. CSP `sandbox` on public Publication responses

**Stops same-origin scripts? Yes, substantially** — for credentialed API calls — **without moving anything.**

The [CSP3 `sandbox` directive](https://www.w3.org/TR/CSP3/) applies an HTML sandbox policy to the resource "just as though it had been included in an `iframe` with a `sandbox` property", via CSP-derived sandboxing flags — including on top-level documents. Per the [HTML Standard](https://html.spec.whatwg.org/multipage/iframe-embed-object.html), sandboxed content "is treated as being from a unique opaque origin", and forms, popups, downloads, and modals are disabled unless re-enabled by keywords (`allow-forms`, `allow-popups`, `allow-downloads`, `allow-top-navigation`, …).

With `Content-Security-Policy: sandbox allow-scripts` on every public response:

- **Requests lose the session.** The document's origin is opaque, so its "site for cookies" computes to an opaque origin, which can never be same-site with the real origin: per [RFC 6265bis §5.2.1](https://datatracker.ietf.org/doc/html/draft-ietf-httpbis-rfc6265bis), "if `origin` is not same-site with `top-origin`, return an origin set to an opaque origin", and a request is "same-site" only if the target origin is same-site with the initiator's site-for-cookies. SameSite=Lax/Strict cookies are therefore withheld from all subresource requests (Lax still sends them on top-level safe-method navigations — i.e. the operator normally loading the manager). Additionally, `fetch()`'s default credentials mode is "`same-origin`" ([Fetch Standard](https://fetch.spec.whatwg.org/)), and a request from an opaque origin is not same-origin, so cookies are not attached even before SameSite applies. Reading responses would require CORS headers, which the manager does not send.
- **Storage and cookies vanish.** `localStorage`, `sessionStorage`, `document.cookie`, and related access fail ("the document is sandboxed and lacks the `allow-same-origin` flag" SecurityError; [MDN iframe sandbox](https://developer.mozilla.org/en-US/docs/Web/HTML/Reference/Elements/iframe), [WHATWG list discussion](https://lists.whatwg.org/pipermail/whatwg-whatwg.org/2013-February/081286.html)).
- **Service workers cannot register** (registration requires script URL, scope, and client to be same-origin; [Service Workers spec](https://www.w3.org/TR/service-workers/)) — this also closes candidate 5 for sandboxed pages.
- **Scripts keep running** with `allow-scripts`; plugins, popups, downloads, and form submission stay blocked. Top-level navigation to the manager still works (that is just the operator browsing) but cannot be automated into a CSRF via forms.

**Keywords:** `allow-same-origin` must **not** be used — it "causes the content to be treated as being from its real origin", i.e. no isolation at all. The HTML warning that `allow-scripts` + `allow-same-origin` together let embedded content break out applies to embedder-controlled iframe attributes; a server-set header is re-applied on every reload, so the classic escape does not apply — but `allow-same-origin` alone already defeats the purpose. Note also CSP3: sandbox "will be ignored entirely when delivered in a `Content-Security-Policy-Report-Only` header" — sandbox cannot be dry-run via Report-Only.

**What breaks.** Publications using `localStorage`/`sessionStorage`/IndexedDB/cookies, credentialed same-origin `fetch`, service workers, popups (`window.open`, OAuth-style flows), form POSTs to same-origin endpoints, or downloads. SPA History routing may survive: the [current spec](https://html.spec.whatwg.org/multipage/nav-history-apis.html) gates `pushState` on "can have its URL rewritten", which compares the document's **URL** components, explicitly noting origin and URL "can mismatch … in sandboxed iframes"; older engines compared against the opaque origin and threw `SecurityError`, so treat SPA routing as a per-engine compatibility risk to test. The number of affected Publications is unknown; an audit is required first.

**Deployment cost.** Low and server-only: one `add_header Content-Security-Policy "sandbox allow-scripts" always;` on the public reader locations (including every fallback stage that can serve an HTML document) and never on manager/auth responses. No DNS, TLS, or oauth2-proxy/OIDC changes. nginx `add_header` inheritance rules mean the header must be placed where it is not silently dropped by other `add_header` directives in the same context.

### 2. Move the manager to a dedicated origin

**Stops same-origin scripts? Yes, with two cheap additions.** Publication pages become cross-origin to the manager. [CORS](https://fetch.spec.whatwg.org/) then blocks their scripts from reading manager responses, so CSRF tokens and DOM-rendered secrets become unreadable again. But a subdomain of the same registrable domain is **same-site**, not same-origin: SameSite does **not** withhold the cookie ([RFC 6265bis](https://datatracker.ietf.org/doc/html/draft-ietf-httpbis-rfc6265bis): same-site is defined by registrable-domain match), so a publication script can still fire a "simple" non-preflighted credentialed POST that executes server-side. Two defenses close that:

- **Origin allowlist:** the `Origin` header is attached to all non-GET/HEAD requests and CORS requests ([Fetch Standard](https://fetch.spec.whatwg.org/)), is a forbidden header (unforgeable by script), and would read as the share origin, not the manager origin. Reject mismatches on state-changing routes.
- **Fetch Metadata:** require `Sec-Fetch-Site: same-origin` (reject `same-site`, `cross-site`, and `none`) on manager API routes; the spec defines exactly these values ([W3C Fetch Metadata](https://www.w3.org/TR/fetch-metadata/)). Browser support: Chrome 76+, Firefox 90+, Safari/iOS 16.4+ ([caniuse](https://caniuse.com/mdn-http_headers_sec-fetch-site)) — acceptable for an operator-controlled browser set; decide fail-open vs fail-closed for missing headers at the nginx layer, which already sees only authenticated traffic.

Cookie hygiene: keep the session cookie host-only (no `Domain` attribute — without Domain, "the user agent will return the cookie only to the origin server"; 6265bis), which the current gateway already satisfies; a `__Host-` name prefix additionally *enforces* Secure + host-only + `Path=/` at set time (6265bis §5.7 step 21) and prevents any future accidental Domain scoping.

**What breaks.** Existing Publications: **nothing** — public URLs, fallback routing, and the public reader are untouched. The manager's URL changes (operator bookmark), and the public routing contract plus ADR-0002, which currently mandate the same-origin `/` + `/_page-hub/` layout, need revision.

**Deployment cost.** Moderate, one-time: a DNS record; the TLS certificate gains the new name (existing ACME automation handles an added SAN) or moves to a wildcard; a second vhost or router rule for the manager origin (the edge SNI map gains one entry if the hostname terminates elsewhere); oauth2-proxy `redirect_url` and the SSO client's registered redirect URI change; the cookie stays host-scoped to the new manager host automatically. Sign-in for new sessions still requires the existing SSO reachability. Publication isolation is a permanent property of routing, not of response headers that a misconfigured location could drop.

### 3. Move public Publications to a separate origin

**Stops same-origin scripts? Yes** — same mechanics as candidate 2 with the direction reversed; the manager keeps the share origin and publications become cross-origin.

**What breaks.** Every existing Publication URL changes. That directly conflicts with the project's own vocabulary — a Move means "old links stop working and no redirect remains" — and with the deployment contract's rule that adopting a Publication "must keep its existing public URL working". External inbound links rot. If the new origin stays inside the same registrable domain, it is still same-site, so candidate 2's Origin/Fetch-Metadata checks are still required; only a different registrable domain would make SameSite=Lax itself protective.

**Deployment cost.** High: DNS + TLS for the new public origin, reader/routing split, catalog URL migration for every Publication, and a deliberate breaking of the URL-stability promise. It buys the same isolation as candidate 2 while touching the public surface instead of the private one.

### 4. Request-level defenses on the shared origin

State plainly which ones a same-origin script defeats — all of them except step-up:

- **SameSite=Lax (deployed):** only distinguishes cross-site initiators; a same-origin script is same-site by definition ([RFC 6265bis](https://datatracker.ietf.org/doc/html/draft-ietf-httpbis-rfc6265bis)). No protection.
- **Fetch Metadata:** the publication script's requests carry `Sec-Fetch-Site: same-origin`, byte-identical to the manager UI's own requests ([W3C](https://www.w3.org/TR/fetch-metadata/)). No protection. Also browser-only and unsupported in older Safari ([caniuse](https://caniuse.com/mdn-http_headers_sec-fetch-site)).
- **Origin header checks:** `Origin` is present on same-origin non-GET requests but equals the shared origin ([Fetch Standard](https://fetch.spec.whatwg.org/)). No protection on one origin; effective only after candidates 2/3.
- **Custom request headers (e.g. requiring a bespoke header):** JavaScript may set any header outside the forbidden list ([Fetch Standard](https://fetch.spec.whatwg.org/)). No protection.
- **CSRF tokens:** a same-origin script can `fetch()` the manager page and APIs and read the token out of the response or the DOM. This is the OWASP rule that any script-injection defeats all CSRF mitigations, applied at deployment scale: "any Cross-Site Scripting (XSS) can be used to defeat all CSRF mitigation techniques" ([OWASP CSRF Prevention Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html)). No protection.
- **Re-authentication / step-up confirmation (e.g. WebAuthn per destructive action): partly effective** — the only same-origin mechanism with real effect. `navigator.credentials.get()` requires user activation and an authenticator user-presence gesture in browsers (see [WebAuthn spec](https://www.w3.org/TR/webauthn-2/), [MDN](https://developer.mozilla.org/en-US/docs/Web/API/CredentialsContainer/get), and Safari's documented gesture requirement), so it stops silent background abuse of rename/move/delete APIs. It remains vulnerable to same-origin UI spoofing (a forged confirmation dialog overlaid on a publication page) and it taxes every destructive action. Cost: per-action UX plus WebAuthn or password-confirmation wiring in the manager; plausible for a single operator.

### 5. Service workers: scope rules and the top-level script hole

The [Service Workers spec](https://www.w3.org/TR/service-workers/) sets the default scope by "parsing `./` with scriptURL" (the script's directory), rejects any scope not under the max scope (`SecurityError`), and only a `Service-Worker-Allowed` response header on the script can widen the max scope ([MDN](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Service-Worker-Allowed)). Registration additionally requires script URL and scope to be same-origin with the registering client.

Consequences for Page Hub:

- A script at `/<project>/sw.js` gets scope `/<project>/` — it **cannot** cover `/` or `/_page-hub/`, and the object-storage-backed public reader never emits `Service-Worker-Allowed`, so widening is impossible. Scope-confined service workers remain a defacement/persistence risk *within that Publication only* (they can serve arbitrary content for that project's URLs on later visits).
- **The hole:** a single-file Root Publication served at a top-level path (its public URL is exactly `/<name>` with a JavaScript MIME type) resolves default scope **`/`**, because the directory of `/<name>` is `/`. Any same-origin page — today, any Publication — can register it. A scope-`/` service worker intercepts all navigations and subresource fetches in scope, including the manager pages: it can serve forged manager HTML (phishing the operator's next sign-in) and its own `fetch()` calls are same-origin with cookies attached (default credentials mode "`same-origin`"). It persists after the page closes. Nothing in the current reserved-path set (`/`, `/_page-hub`) prevents a top-level `sw.js`-shaped Publication.
- **Defenses:** Page Hub validation can reject Root Publication entry files at top-level paths that would resolve scope `/` (any top-level file object with a JavaScript MIME type); the manager page can enumerate and unregister unexpected registrations (`navigator.serviceWorker.getRegistrations()` works same-origin); `Clear-Site-Data: "storage"` wipes service workers origin-wide ([W3C Clear-Site-Data](https://www.w3.org/TR/clear-site-data/)) but destroys all publication persistence too. CSP sandbox (candidate 1) blocks registration outright; separate origins (candidates 2/3) bound any registered worker to the publication origin.

**Stops same-origin scripts?** As a standalone mechanism: no. As path policy plus monitoring: yes for the scope-`/` escalation specifically. **Deployment cost:** validation-rule change in Page Hub plus optionally a registration audit in the manager UI; no infrastructure change.

### 6. Other mechanisms considered

- **Clear-Site-Data:** clears cookies/storage/cache for the origin ([W3C](https://www.w3.org/TR/clear-site-data/)). Useful sign-out hygiene and a service-worker eviction hammer; not a request-isolation mechanism. Breaks publication storage if used routinely.
- **COOP/COEP:** isolate cross-origin browsing contexts and embed cross-origin resources with CORP; they do not constrain same-origin `fetch` or cookie attachment ([MDN COOP](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Cross-Origin-Opener-Policy)). Not applicable.
- **Suborigins:** the W3C draft ([w3c.github.io/webappsec-suborigins](https://w3c.github.io/webappsec-suborigins/)) would partition origins by path via an `Suborigins` header — exactly this use case ("isolate the admin module") — but the spec is dormant in the now-archived Web Application Security Working Group and no current browser implements it. Not deployable.
- **Cookie `Path` attribute:** `Path` is a URL-prefix attachment filter, not a trust boundary; any same-origin script may request any path. The `__Host-` prefix *requires* `Path=/` (6265bis §5.7 step 21), so path-scoped manager cookies are not even expressible under it. Not a defense.
- **Client certificates (mTLS) for manager routes:** the certificate is host-scoped and presented by the browser on any request to that host, including a publication script's `fetch`. Adds certificate management per device without separating code trust domains. Poor fit.

## Comparison table

| Mechanism | Stops same-origin publication script? | Breaks existing Publications? | Deployment cost | Fit |
| --- | --- | --- | --- | --- |
| Manager on dedicated origin + Origin/`Sec-Fetch-Site` allowlist | Yes (CORS blocks reads; checks block blind writes) | None; manager URL and ADR/contract revision | DNS record, cert SAN, second vhost/SNI entry, OIDC redirect change | **1 — best** |
| CSP `sandbox allow-scripts` (no `allow-same-origin`) on public responses | Yes (opaque origin: no cookies, no storage, no SW, no CORS reads) | Unknown count: storage-, cookie-, SW-, popup-, download-, form-dependent publications; SPA routing needs testing | One nginx header on all public locations | **2 — strong, audit first** |
| Step-up (WebAuthn / re-auth) on destructive APIs | Partly (needs user gesture; spoofable UI) | None for readers; per-action manager UX | Manager + authenticator wiring | **3 — defense-in-depth only** |
| Service-worker path policy (reserve top-level JS files) | No (targets the scope-`/` escalation only) | Future single-file Root Publications at top level | Page Hub validation + manager audit | **Hardening, do regardless** |
| Publications on a separate origin | Yes | Every public URL changes; breaks the URL-stability promise | DNS, cert, reader split, catalog migration | 4 — dominated by option 1 |
| Request-level defenses alone (SameSite, Origin, Fetch Metadata, tokens, custom headers) | No — same-origin script reads tokens and sends cookie-bearing requests | None | Low | 5 — not a solution |
| Suborigins | (would be yes) | (untested) | None possible | Not deployable |
| Clear-Site-Data / COOP/COEP / cookie Path / mTLS | No | — | — | Not applicable |

## Ranking (best fit first)

1. **Manager on a dedicated origin**, plus Origin-header and `Sec-Fetch-Site: same-origin` allowlists (and CSRF tokens, which become meaningful again). One-time routing/certificate/OIDC cost, zero impact on Publications, isolation enforced by routing rather than per-response headers.
2. **CSP `sandbox allow-scripts` on public responses.** Strongest fix without new infrastructure, but the publication breakage set is unknown and sandbox cannot be rehearsed via Report-Only; requires an audit of storage/SW/cookie usage and a policy for exceptions.
3. **Step-up confirmation for destructive APIs** — worth layering onto either of the above; insufficient alone.
4. **Publications on a separate origin** — maximal manager isolation but breaks the existing-URL promise; only preferable if the share origin must become manager-only for other reasons.
5. **Request-level defenses alone** — provably ineffective against the threat in this ticket.

Regardless of ranking, reserve the top-level service-worker escalation paths and never emit `Service-Worker-Allowed` from the public reader.

## Open questions for the decision ticket

- Do any current Publications rely on `localStorage`/`sessionStorage`/IndexedDB, cookies, service workers, credentialed same-origin `fetch`, popups, or downloads? (Determines candidate 1's real blast radius; publications can be audited from the catalog plus a storage-feature probe.)
- If sandbox is chosen: is a per-publication opt-out or per-publication sandbox flag acceptable, and who signs off on testing SPA routing in the operator's actual browsers?
- Operator browser baseline: are all browsers used for the manager ≥ the Fetch Metadata support floor (Safari 16.4+), and should missing `Sec-Fetch-Site` fail open or closed behind the authentication gateway?
- Which manager hostname, SAN vs wildcard certificate, and which layer owns the new vhost and SNI entry?
- Should permanent delete, move, and Project operations require step-up regardless of the origin decision?
- Exact reservation rule for service-worker-capable top-level files (path + JavaScript MIME type), manager-side registration audit, and whether sign-out should send `Clear-Site-Data`.
- Scope of the ADR-0002 / routing-contract revision if the same-origin layout is abandoned (the public contract currently requires it).
- Sign-in reachability: moving the manager does not change that new SSO sessions need LAN/VPN; confirm that remains acceptable.

## Sources

Kept:

- [CSP3 (W3C)](https://www.w3.org/TR/CSP3/) — sandbox directive, CSP-derived flags, Report-Only limitation.
- [HTML Standard, iframe sandbox](https://html.spec.whatwg.org/multipage/iframe-embed-object.html) — opaque origin, keyword semantics, allow-scripts+allow-same-origin escape warning.
- [HTML Standard, session history APIs](https://html.spec.whatwg.org/multipage/nav-history-apis.html) — pushState gated on URL rewriting, not origin; sandboxed-document note.
- [Fetch Standard (WHATWG)](https://fetch.spec.whatwg.org/) — forbidden headers incl. `Sec-` prefix, default credentials mode, Origin attachment rules.
- [RFC 6265bis draft (IETF)](https://datatracker.ietf.org/doc/html/draft-ietf-httpbis-rfc6265bis) — site-for-cookies, same-site/registrable domain, SameSite Strict/Lax enforcement, `__Host-` prefix, host-only cookies.
- [Fetch Metadata Request Headers (W3C)](https://www.w3.org/TR/fetch-metadata/) — `Sec-Fetch-Site` values and server guidance.
- [caniuse: Sec-Fetch-Site](https://caniuse.com/mdn-http_headers_sec-fetch-site) — browser support matrix (Safari 16.4+).
- [Service Workers (W3C)](https://www.w3.org/TR/service-workers/) — default/max scope, `Service-Worker-Allowed`, same-origin registration checks.
- [MDN: Service-Worker-Allowed](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Service-Worker-Allowed) — scope widening rules, supplement to the spec.
- [MDN: Content-Security-Policy sandbox](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Content-Security-Policy/sandbox) — sandbox token reference.
- [MDN: iframe](https://developer.mozilla.org/en-US/docs/Web/HTML/Reference/Elements/iframe) — sandbox keyword semantics and storage/cookie effects.
- [OWASP CSRF Prevention Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html) — XSS defeats all CSRF mitigations.
- [W3C Clear-Site-Data](https://www.w3.org/TR/clear-site-data/) — what each directive clears.
- [W3C Suborigins (dormant draft)](https://w3c.github.io/webappsec-suborigins/) — the path-scoped-origin proposal; archived WG, unimplemented.
- [WebAuthn Level 2 (W3C)](https://www.w3.org/TR/webauthn-2/) and [MDN CredentialsContainer.get()](https://developer.mozilla.org/en-US/docs/Web/API/CredentialsContainer/get) — user-interaction requirements for step-up.
- [WHATWG mailing list: sandboxed origin flag](https://lists.whatwg.org/pipermail/whatwg-whatwg.org/2013-February/081286.html) — flag blocks `document.cookie`/`localStorage`.
- Local, read-only: CONTEXT.md, docs/adr/0002, docs/deployment.md, docs/routing-deployment.md, internal/server/server.go, and the private deployment notes (genericized here).

Dropped:

- OpenPWA, CentralCSP, Stack Overflow answers, Chrome extension forum threads — non-primary or duplicative of spec text.
- Vendor blogs and mirrored OWASP copies — kept the official OWASP page instead.
