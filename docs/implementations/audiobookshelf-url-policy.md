# Audiobookshelf URL Policy

**Status:** Defined; not yet enforced. The edition create work implements this
policy once, where Audiobookshelf (ABS) clients are constructed, before the
create endpoint ships. Until then, sync, the edition draft, and the CLI keep
today's behavior.

The service fetches a configured ABS base URL on the server with that
configuration's ABS token. In a multi-user web deployment a profile owner
chooses that URL, so an unrestricted destination lets the owner make the
server send authenticated requests to other hosts on its network. Most
deployments are self-hosted and reach ABS through a LAN address, a Docker
service name, or loopback, so a blanket private-address ban would break them.
The policy therefore separates the operator's network trust decision from the
profile owner's URL choice.

## Trust mode

The trust mode is a deployment setting, not a profile setting:

- YAML: `audiobookshelf.network_trust: allow_private|public_only`
- Environment: `AUDIOBOOKSHELF_NETWORK_TRUST=allow_private|public_only`

It follows the existing `internal/config.Load` precedence (defaults, then the
optional config file, then the environment). It applies to the CLI's
`audiobookshelf.url` and to every web profile's `audiobookshelf_url`. Profile
settings and the profile API cannot select or loosen it. An unsupported value
is a configuration error rather than a silent fallback.

| Mode | Allowed destination addresses | Schemes |
|---|---|---|
| `allow_private` (default) | Globally routable unicast, RFC 1918 IPv4, shared address space (`100.64.0.0/10`, used by Tailscale and similar overlays), IPv6 unique-local (`fc00::/7`), and loopback | HTTP or HTTPS |
| `public_only` | Globally routable unicast only | HTTPS only |

`allow_private` is the default so existing self-hosted, Docker, and single-user
deployments keep working without a configuration change. Operators who let
untrusted users create profiles should set `public_only`.

Both modes reject unspecified, multicast, broadcast, link-local (including
cloud metadata endpoints such as `169.254.169.254` and `fe80::/10`),
documentation, benchmarking, and other reserved or non-routable addresses.
`public_only` also rejects every address that only `allow_private` adds.
IPv4-mapped IPv6 addresses are classified by their IPv4 address.

## URL validation

A configured base URL must be an absolute `http` or `https` URL with a host and
no userinfo, query, or fragment. A trailing slash is removed, as today.
Validate the syntax when a profile or CLI configuration is saved or loaded,
and report a clear configuration error. Validate destination addresses at
connection time, because DNS answers can change after the configuration is
saved.

HTTPS certificate verification stays enabled in both modes.

## Connections and redirects

- Resolve the destination hostname for every connection. Reject the
  destination if any A or AAAA result is outside the trust mode, connect only
  to a validated address, and check the connected peer address against the
  same policy.
- Apply the same checks to every redirect target before following it,
  including its scheme.
- Send the ABS bearer token only when the request target has the configured
  base URL's scheme, host, and port, and its normalized path is the configured
  base path or a descendant at a path-segment boundary. Evaluate this against
  the original configured base URL on every redirect, and strip the token for
  any other target.

## Scope

One shared client boundary enforces the policy for sync, edition drafts,
edition creation, and the standalone `edition create` command, replacing the
edition creator's separate redirect check. Tests cover malformed URLs,
disallowed addresses in each mode, allowed local deployments, DNS answers with
mixed allowed and disallowed addresses, redirects to disallowed destinations,
and token stripping on redirects. The edition capability route does not
contact ABS.
