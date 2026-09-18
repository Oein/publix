# Routing beyond one hostname per project

Most projects need nothing here. A repository gets a hostname, the hostname
reaches its port, and that is the whole story. This page is for the two
cases where it is not: a hostname that has to be split between services, and
a protocol that is not HTTP.

---

## Splitting one hostname across services

A compose stack can serve one hostname from more than one service. Give each
route the service it reaches and a priority, highest first:

```yaml
type: compose
compose: docker-compose.yml
service: frontend
port: 3000

routes:
  - domain: git.example.com
    service: backend
    priority: 110
    matchAny:
      - pathPrefix: /api
      - header: Content-Type
        regexp: application/x-git.*
      - query: service
        regexp: git-.*
      - pathRegexp: /.+/.+/info/refs
      - pathRegexp: /.+/.+/git-.*

  - domain: git.example.com
    service: frontend
    priority: 100
```

`matchAny` narrows a route to requests meeting **at least one** of its
conditions. Everything not matched falls through to the lower-priority
route, which is why the catch-all needs no conditions of its own.

Set `priority` explicitly whenever two routes share a hostname. Traefik's
own default ranks by rule length, which decides nothing you meant: a route
that carves a few requests out of a hostname has to outrank the catch-all
for it, and whose rule is the longer string is no way to settle that.

### Conditions

Each entry in `matchAny` sets exactly one selector.

| Selector | Matches |
| --- | --- |
| `pathPrefix: /api` | paths starting with `/api` |
| `pathRegexp: /.+/.+/git-.*` | the path, against a regular expression |
| `method: POST` | the HTTP method |
| `header: X-Thing` + `regexp:` | that header's value |
| `query: service` + `regexp:` | that query parameter's value |

Two selectors in one entry is an error rather than a guess: publix would
have to decide whether you meant *and* or *or*, and either reading would
silently route traffic the way you did not ask. List them separately to
match any of them.

There is deliberately **no raw Traefik rule**. A hand-written rule could
match a hostname the project does not own, and publix's routing rests on a
repository being unable to claim traffic that is not its own. Every
condition above narrows a route already anchored to a host publix checked.

---

## A protocol that is not HTTP, but is TLS

`tcp:` forwards raw TLS connections by SNI — for a backend that serves its
own certificate (a custom CA), or a non-HTTP protocol wrapped in
TLS. Traefik reads the server name from the handshake and forwards the
stream untouched:

```yaml
tcp:
  - sni: [mail.example.com]
    port: 993
    passthrough: true
```

`passthrough: true` is required. publix never terminates TLS for a TCP
route, because that would mean holding the certificate. For a compose stack,
add `service:` to say which container owns the port.

---

## A protocol Traefik cannot route at all

`ports:` publishes a container port on the server, the way `docker run -p`
does:

```yaml
ports:
  - host: 2222
    container: 2222     # defaults to host
    service: backend    # required for a compose stack
    protocol: tcp       # tcp (default) or udp
    bind: 127.0.0.1     # defaults to every address
```

Adding that to `deployment.yaml` is the whole configuration. There is
nothing to register on the server, no Traefik entry point to declare and
nothing to restart — which is the point: a repository describes its own
deployment, and a port is part of that.

**Why not route it through Traefik?** Traefik matches TLS by SNI and HTTP by
host header. A protocol that is neither — git over SSH, a database wire
protocol — carries nothing to match on. There is no way to share a port
between projects and nothing for a reverse proxy to decide, so publishing
the port directly is not a workaround; it is the only honest answer.

### What it costs

A published port cannot move atomically. Two generations of a deployment
cannot both hold it, so a project publishing a port must use the recreate
strategy, and publix enforces that rather than assuming it:

```
release.strategy: must be recreate when ports are published — a host port
cannot be held by two deployments at once, so the new one could not start
while the old one is still serving
```

Compose projects are already forced to recreate, so this only constrains the
other kinds. `replicas:` must be 1 for the same reason.

This means a deploy that publishes a port has a gap: the old container stops
before the new one starts. HTTP traffic for the same project still cuts over
atomically — only the published port blinks.

Two projects cannot publish the same port. publix refuses the second at
deploy time, before spending a build on it, naming both projects — Docker
would refuse the bind too, but only after the image was built and with a
message naming neither.

---

## What still moves atomically

Everything Traefik routes keeps publix's central property. The container's
labels define the service; the routers that point at it live in the file
publix owns. A cutover rewrites that file and Traefik reloads it — the
containers are never recreated to move traffic, and an SNI route follows a
rollback the same way a hostname does.

A published port is the one exception, and it is inherent rather than a
shortcut: a port is held by a process, not named in a file, so it cannot be
in two places at once. That is the trade `ports:` makes, and the reason it
is a separate section rather than another kind of route.
