# Audiobookshelf URL Policy

**Status:** Enforced for the production Audiobookshelf clients used by sync,
edition drafts, and the edition creator's ABS cover requests in the
standalone `edition` and `image-tool` commands. A later create API must use
the same shared client. The deployment-wide mode is validated when
configuration loads; profile URLs are validated when profiles are created or
updated, and an invalid profile URL is rejected with HTTP 400. The shared
client checks destinations when requests and redirects are made.

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

The implementation explicitly rejects unspecified, multicast, and link-local
addresses, cloud metadata addresses such as `169.254.169.254`, documentation
and benchmarking ranges, and selected other special-purpose ranges.
`public_only` also rejects every address that only `allow_private` adds.
IPv4-mapped IPv6 addresses are classified by their IPv4 address.

## URL validation

A configured base URL must be an absolute `http` or `https` URL with a host and
no userinfo, query, or fragment. `public_only` requires HTTPS. A trailing
slash is removed, as today. Configuration loading validates the trust value
and a configured standalone URL. Web profile creation and URL updates
validate the profile URL; the client constructor also rejects invalid URL or
trust values before sending a request. Each request checks URL syntax. New
connections check the resolved address and connected peer, because DNS answers
can change after a URL is saved. Redirects also check their destination
addresses before following them. An existing validated connection remains
usable if DNS is temporarily unavailable.

HTTPS certificate verification stays enabled in both modes.

Audiobookshelf requests connect directly and ignore `HTTP_PROXY`,
`HTTPS_PROXY`, and `NO_PROXY`. A proxy would resolve destinations outside the
checked dialer, so an operator must provide a direct route to Audiobookshelf.

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

The shared client policy is wired into sync, edition drafts, and the edition
creator's ABS requests from the standalone `edition` and `image-tool`
commands, replacing the creator's separate redirect check for those requests. Tests cover malformed URLs,
address classification in each mode, mixed allowed and disallowed DNS
answers, redirects to disallowed destinations, and token stripping outside
the configured origin or base path. The edition capability route does not
contact ABS.
