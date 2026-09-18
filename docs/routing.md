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

## A protocol that is not HTTP

`tcp:` forwards raw connections. It has two modes, and which one you need
depends on whether there is TLS involved.

### By TLS SNI

For a backend that serves its own certificate — a custom CA, or a non-HTTP
protocol wrapped in TLS. Traefik reads the server name from the TLS
handshake and forwards the stream untouched:

```yaml
tcp:
  - sni: [mail.example.com]
    port: 993
    passthrough: true
```

`passthrough: true` is required. publix never terminates TLS for a TCP
route, because that would mean holding the certificate.

### By dedicated entry point

For a protocol that is not TLS at all — git over SSH, a database wire
protocol. There is no server name in the handshake, so nothing distinguishes
one connection from another except the port it arrived on:

```yaml
tcp:
  - entryPoint: gitssh
    port: 2222
    service: backend     # required for a compose stack
```

Everything arriving on that entry point goes to this one project. The entry
point must therefore be **dedicated** — publix refuses two routes claiming
the same one, but it cannot stop you from pointing a shared entry point at a
project, so do not.

**You have to declare the entry point yourself.** publix writes Traefik's
*dynamic* configuration; entry points live in its *static* configuration,
which publix does not own. In `deploy/docker-compose.override.yml`:

```yaml
services:
  traefik:
    command:
      # Compose replaces the command list rather than appending to it, so
      # copy every flag from deploy/docker-compose.yml first.
      - ...
      - --entrypoints.gitssh.address=:2222
    ports:
      - "80:80"
      - "443:443"
      - "2222:2222"
```

The name in `--entrypoints.<name>.address` must match `entryPoint:` in
deployment.yaml. Get it wrong and Traefik quietly drops the router: the port
answers nothing, with no error in publix.

`deploy/docker-compose.override.yml.example` carries a copy of this with
every flag filled in.

---

## What still moves atomically

Both kinds of route keep publix's central property. The container's labels
define the service; the routers that point at it live in the file publix
owns. A cutover rewrites that file and Traefik reloads it — the containers
are never recreated to move traffic, and a TCP route follows a rollback the
same way a hostname does.
