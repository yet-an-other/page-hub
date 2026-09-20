# `share.bdgn.me` routing deployment handoff

Observed and checked on 2026-09-18. The infrastructure source is the private [`yet-an-other/homelab`](https://github.com/yet-an-other/homelab) repository at commit [`329775e`](https://github.com/yet-an-other/homelab/commit/329775e85a4e9333972bbcf2455038d90eea96f2). The repository is also cloned at `/home/ib/Projects/homelab` on the operator workstation.

## Finding

Page Hub can use the existing `share.bdgn.me` origin without changing public DNS, the WAN firewall rule, or the bastion SNI route. TLS and HTTP routing already terminate on `s3-node`. The smallest deployment change is to reserve manager and API paths in the `s3-node` nginx vhost before its public Publication catch-all, then proxy those paths to Page Hub. The public catch-all can keep reading the `web-share` bucket when the manager is down.

Current request path:

```text
public Cloudflare DNS
  share.bdgn.me CNAME -> bdgn.me A -> home public entry
    -> OPNsense TCP/443 port forward
      -> bastion nginx stream router, SNI only, no TLS termination
        -> s3-node nginx, TLS and HTTP termination
          -> 127.0.0.1:7480 Ceph RadosGW
            -> web-share bucket
```

## Ownership and change paths

| Layer | Current owner and source | Change path |
| --- | --- | --- |
| Public DNS | The Cloudflare `bdgn.me` zone. `share` is a CNAME to the apex. The record is operator-managed and is not infrastructure as code. The homelab [domain notes](https://github.com/yet-an-other/homelab/blob/329775e85a4e9333972bbcf2455038d90eea96f2/CONTEXT.md#L65-L73) record the split-horizon setup. | Cloudflare dashboard. No change is needed while Page Hub keeps the same origin. Cloudflare credentials in the private Ansible inventory are for DNS-01 certificate issuance, not record deployment. |
| Public TCP entry | OPNsense forwards public TCP/443 to the bastion. This rule is operator-managed and absent from the Git repository. | OPNsense dashboard at the existing internal firewall URL. No change is needed while the same hostname and port stay on the bastion. |
| SNI routing | Bastion nginx passes TLS through by SNI. [`vm-bastion/templates/nginx.conf.j2`](https://github.com/yet-an-other/homelab/blob/329775e85a4e9333972bbcf2455038d90eea96f2/vm-bastion/templates/nginx.conf.j2#L79-L134) maps `share.bdgn.me` to the `s3-node` upstream. | Edit the template and run `./apply.sh vm-bastion` only if the hostname or destination host changes. Page Hub does not need that change. |
| TLS and HTTP routing | `s3-node` nginx terminates TLS. [`vm-s3/share.conf`](https://github.com/yet-an-other/homelab/blob/329775e85a4e9333972bbcf2455038d90eea96f2/vm-s3/share.conf#L39-L136) owns the `share.bdgn.me` vhost and its catch-all public reader. | Edit `vm-s3/share.conf`, extend [`vm-s3/create-vm-s3.yaml`](https://github.com/yet-an-other/homelab/blob/329775e85a4e9333972bbcf2455038d90eea96f2/vm-s3/create-vm-s3.yaml#L385-L416), then run `./apply.sh vm-s3`. |
| Certificate | The same `vm-s3` play requests one Let's Encrypt certificate covering `s3-node.bdgn.me`, `s3.bdgn.me`, and `share.bdgn.me`. [`ansible/add-ssl-certificate.yaml`](https://github.com/yet-an-other/homelab/blob/329775e85a4e9333972bbcf2455038d90eea96f2/ansible/add-ssl-certificate.yaml#L55-L88) uses `acme.sh` with Cloudflare DNS-01 credentials from the private inventory. | The existing certificate and renewal path already cover Page Hub at `share.bdgn.me`. Add a name to the `sites` argument and rerun the play only if the hostname changes. |
| Public reader and storage | `s3-node` nginx proxies GET and HEAD to loopback RadosGW. The bucket grants anonymous `GetObject` only from loopback. [`vm-s3/create-vm-s3.yaml`](https://github.com/yet-an-other/homelab/blob/329775e85a4e9333972bbcf2455038d90eea96f2/vm-s3/create-vm-s3.yaml#L217-L405) owns the RGW user, bucket policy, quota, and nginx deployment. | Keep the public reader as its own nginx locations. Page Hub may call the internal S3 endpoint with server-held credentials. Do not send those credentials to browser or publishing client code. |
| Existing identity system | The homelab runs ZITADEL at the internal `sso.bdgn.me` name. Bastion already has a reviewed nginx plus oauth2-proxy pattern with an exact-email allowlist. See [ADR-0004](https://github.com/yet-an-other/homelab/blob/329775e85a4e9333972bbcf2455038d90eea96f2/docs/adr/0004-bastion-panel-authentication-via-zitadel.md), the [oauth2-proxy config](https://github.com/yet-an-other/homelab/blob/329775e85a4e9333972bbcf2455038d90eea96f2/vm-bastion/templates/xform-oauth2-proxy.cfg.j2), and the [nginx gateway](https://github.com/yet-an-other/homelab/blob/329775e85a4e9333972bbcf2455038d90eea96f2/vm-bastion/templates/fallback.conf.j2). | Reuse is possible, but not automatic. Page Hub needs its own OIDC client, callback, cookie secret, exact operator allowlist, service unit, and path-specific nginx rules. The IdP is internal, so sign-in requires LAN or VPN access. |

## Deployment choices available to the runtime decision

### Keep TLS on `s3-node` and run Page Hub there

This needs one host and one play. Add the Page Hub service, its private catalog, and an auth component to `vm-s3`, then put exact manager routes ahead of the public reader. This is the shortest path. The Page Hub process can reach RadosGW through loopback.

The public reader must remain an nginx-to-RGW path rather than pass through the Page Hub process. A stopped manager then affects manager routes only.

### Keep TLS on `s3-node` and proxy manager routes to another internal host

This keeps the same DNS, firewall, bastion, certificate, and public reader. `s3-node` remains the HTTP router and proxies only the reserved manager paths to the new runtime. Use this if the chosen runtime or catalog belongs on another VM or container.

The proxy must return a manager error when the runtime is down. It must never hand a failed manager request to the Publication fallback chain.

### Move TLS or path routing to the bastion

This is deployable but unnecessary. It would turn the current layer-4 SNI router into an HTTP boundary for this hostname, move or duplicate certificate ownership, and couple public Publication reads to more bastion configuration. There is no current requirement that justifies it.

## Route and authentication constraints

The current `location /` is a public GET and HEAD catch-all. It performs directory-index and Project-root fallback. Page Hub must add higher-priority locations for the exact manager page, manager assets, browser management API, token-authenticated publishing API, auth callback, and sign-out paths.

Those locations need these properties:

- nginx selects them before `location /`;
- every manager document and browser API request requires the configured operator;
- every publishing API request requires a scoped Page Hub bearer token;
- API authentication failures stay JSON responses rather than HTML sign-in pages;
- upstream failure returns a manager error and never enters Publication fallback;
- state-changing methods are accepted only on Page Hub routes;
- browser cookies use secure, HTTP-only, and intentional SameSite settings;
- private responses use `Cache-Control: no-store` or another explicit private policy;
- browser routes strip client-supplied authorization and identity headers before trusting gateway assertions;
- publishing routes strip cookies and identity headers, then forward the bearer token only to Page Hub through the protected Unix socket;
- Page Hub rejects Project paths that collide with the reserved route set; and
- the public Publication locations keep their current anonymous behavior.

The existing ZITADEL pattern is a real option for the single operator. Its main limit is deliberate: `sso.bdgn.me` resolves only inside the LAN or over VPN. If #7 requires sign-in without LAN or VPN, it must choose another identity boundary or separately decide to expose the IdP.

## Secrets and operator access

- Homelab source access: private GitHub repository or the local clone.
- Deployment inventory: `ansible/inventory.secret.yaml`, linked from the private AppSecrets store. Do not copy it into Page Hub.
- Host access: existing `bastion` and `s3-node` SSH aliases.
- DNS changes: Cloudflare dashboard.
- WAN forwarding changes: OPNsense dashboard.
- Application and nginx deployment: homelab Ansible from `/home/ib/Projects/homelab`.

No credential values were read for this handoff.

## Checks performed

- Public DNS through Cloudflare's resolver returned `share.bdgn.me CNAME bdgn.me` and the apex public address.
- External probes from five countries received `404` at the root and `200` for the accepted Page Hub prototype Publication. This matches the current routing contract.
- The deployed bastion nginx config contains the `share.bdgn.me` to `s3-node` SNI route, and nginx is active.
- The deployed `/etc/nginx/conf.d/share.conf` checksum matches the rendered `vm-s3/share.conf` template. nginx and RadosGW are active.
- The deployed Let's Encrypt certificate includes `share.bdgn.me` and expires on 2026-12-07. Renewal remains the `acme.sh` responsibility installed by Ansible.

## Handoff to #7

Treat `s3-node` nginx as the existing path router and TLS boundary. Pick whether the Page Hub process runs on that host or behind it. Then choose between the already deployed internal ZITADEL pattern and application-managed authentication. The DNS, WAN, and bastion layers do not constrain that choice unless the decision introduces a new hostname or requires sign-in outside LAN or VPN.
