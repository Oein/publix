# publix

A self-hosted deployment platform for Docker and Traefik. One binary, one
`deployment.yaml` per repository, a web dashboard, and deploy-on-push from
GitHub.

If you already run Docker and Traefik by hand, publix is the layer that
makes them feel like a managed platform: import a repository, and it works
out how to build it, builds it, health-checks it, moves traffic to it
atomically, and gives you a URL — with rollback, logs, environment
variables and shared storage in the same place.

```
publix serve
```

Then open the dashboard, set a password, connect GitHub, and press Import.

---

## What it does

**One file describes a deployment.** `deployment.yaml` at the root of a
repository says how to build and run it. Most repositories need three lines,
because everything publix can work out for itself, it does — Dockerfile,
Compose file, static build, framework conventions, exposed port.

**Traffic moves atomically.** A new deployment is built, started and
health-checked on its own private URL before anything reaches it. Only then
is production traffic moved, in a single file write that Traefik hot-reloads.
A deployment that cannot start is a failed deploy with the previous version
still serving — not an outage.

**One deployment stays resident.** Once the new generation is healthy and
serving, the old one is drained and removed. A host running twenty projects
runs twenty sets of containers, not sixty.

**Rollback is one click.** Two images per project are kept, so returning to
the previous deployment needs no build at all. Anything older is rebuilt
from its commit. Either way, the deployment's recorded configuration comes
back with it — its domains and settings, not just its code.

**Shared storage is safe by construction.** You register host directories on
the server; projects ask for them *by name*. Each project receives its own
subdirectory, so two projects can mount the same volume and neither can read
the other's files.

---

## Getting started

### 1. Traefik

publix writes into Traefik's file-provider directory and expects its Docker
provider to be on. A minimal `traefik.yml`:

```yaml
entryPoints:
  web:
    address: ":80"
    http:
      redirections:
        entryPoint: { to: websecure, scheme: https }
  websecure:
    address: ":443"

providers:
  docker:
    exposedByDefault: false
    network: publix
  file:
    directory: /etc/traefik/dynamic
    watch: true

certificatesResolvers:
  letsencrypt:
    acme:
      email: you@example.com
      storage: /etc/traefik/acme.json
      httpChallenge: { entryPoint: web }
```

Create the shared network once:

```
docker network create publix
```

### 2. publix

```
go build -o publix ./cmd/publix
sudo ./publix serve --addr 127.0.0.1:4321
```

Open the dashboard, choose a password, then under **Settings → Server** set:

- **Apps domain** — a wildcard domain such as `apps.example.com`, pointed at
  this host. Every project then gets a working URL immediately, before you
  configure a custom domain.
- **Public URL** — where the dashboard is reachable. GitHub webhooks are
  pointed here, so deploy-on-push needs it.

### 3. GitHub

Under **Settings → GitHub**, connect either:

- a **personal access token** — fastest; classic tokens need the `repo`
  scope, fine-grained tokens need Contents (read), Metadata (read), Webhooks
  (read/write) and Commit statuses (write); or
- a **GitHub App** — right for an organisation: scoped per repository, and
  its tokens rotate on their own.

Your repositories then appear under **Import**. Press Import on one and
publix inspects it, shows you what it worked out, and deploys it.

---

## deployment.yaml

Every field has a defensible default. A repository with a Dockerfile that
`EXPOSE`s a port needs nothing at all.

```yaml
name: my-app
domains:
  - app.example.com

port: 3000

env:
  NODE_ENV: production
  DATABASE_URL: ${secret.DATABASE_URL}

volumes:
  - disk0

health:
  path: /healthz
  grace: 60s
```

### Build types

`type` is detected when omitted. A Compose file wins over a Dockerfile, which
wins over a framework guess — a repository that ships either has already
said how it wants to run.

```yaml
type: dockerfile        # dockerfile | compose | static | image | auto
dockerfile: Dockerfile
context: .
build:
  target: production    # a stage of a multi-stage build
  args:
    VERSION: "2"
```

```yaml
type: compose
compose: docker-compose.yml
service: web            # which service receives the project's domains
port: 8080              # the port that service listens on
```

```yaml
type: static
build:
  install: npm ci
  command: npm run build
  output: dist
  spa: true             # rewrite unknown paths to index.html
```

```yaml
type: image
image: ghcr.io/acme/app:1.4.2
port: 8080
```

### Routing

```yaml
domains:
  - app.example.com

routes:
  - domain: www.example.com
    redirectTo: app.example.com
  - domain: example.com
    path: /api
    stripPath: true
  - domain: internal.example.com
    basicAuth:
      - "admin:$apr1$..."      # htpasswd format
```

### Environment

Values may reference three namespaces. An unresolved reference fails the
deploy rather than starting your app with a blank setting — give it a
`:-default` if it is genuinely optional.

```yaml
env:
  DATABASE_URL: ${secret.DATABASE_URL}   # set in the dashboard
  HOME_REGION: ${env.REGION}             # from the publix server's own env
  BUILD: ${publix.COMMIT}                # deployment metadata
  LOG_LEVEL: ${secret.LOG_LEVEL:-info}   # optional, with a default
```

Every container also receives `PORT`, `PUBLIX_COMMIT`, `PUBLIX_BRANCH`,
`PUBLIX_DEPLOYMENT_ID`, `PUBLIX_PROJECT` and `PUBLIX_URL`.

Variables set in the dashboard override anything in `deployment.yaml`, so a
repository can never override a production credential by editing a file.

### Shared volumes

Register a volume on the server (`disk0` → `/mnt/data`), then:

```yaml
volumes:
  - disk0                    # mounts at /shared/disk0

  - name: uploads
    mountPath: /var/uploads
    readOnly: false

  - name: disk0
    subPath: cache           # a subdirectory of this project's own area
```

A project with ID `abcd1234` mounting `disk0` gets `/mnt/data/abcd1234` at
`/shared/disk0`. The project ID never changes, even if you rename the
project, so renaming can never orphan or expose its data.

### Release

```yaml
release:
  strategy: blue-green   # or recreate
  drain: 10s             # how long the old generation keeps serving
  autoRollback: true     # tear down a deployment that fails its health gate
  branch: main           # the branch that deploys to production
```

`blue-green` starts the new containers alongside the old, waits for health,
moves traffic, then reaps. `recreate` stops the old set first — the right
choice on a small host, or when the project holds an exclusive resource.
Compose projects always use `recreate`: Compose replaces containers in
place, and two generations of one stack cannot coexist.

### Health

The gate that makes a bad deploy a non-event.

```yaml
health:
  type: http          # http | tcp | exec | none
  path: /healthz
  status: 200
  interval: 2s
  timeout: 5s
  grace: 90s          # how long the app has to come up
```

If the image declares its own Docker `HEALTHCHECK`, that is used instead —
it knows more about the application than publix can.

### Scheduled jobs

Run in a throwaway container from the live image, with the same environment
and volumes the app has.

```yaml
cron:
  - name: nightly
    schedule: "0 3 * * *"
    command: ["node", "scripts/nightly.js"]
    timeout: 30m
```

### Resources

```yaml
replicas: 2           # ignored for compose projects
resources:
  cpu: "1.5"
  memory: 512M
  memoryReservation: 256M
```

---

## Docker Compose

publix does not reimplement Compose. It reads your compose file to learn the
stack's shape, generates a second file with its own additions — labels,
environment, shared volumes — and lets Compose merge them. **Your repository
is never modified.**

- The Compose project name is stable across deploys, so named volumes
  survive a redeploy rather than being replaced with empty ones.
- Only the routed service is attached to the shared `publix` network. The
  rest of the stack stays on its own network, unreachable from outside.
- Published ports in your compose file are respected as written.

```yaml
# deployment.yaml
type: compose
service: web
port: 3000
domains: [app.example.com]
volumes: [disk0]
```

Rolling back a Compose stack rebuilds it from the target commit, because
there is no second generation to switch back to.

---

## The command line

The dashboard is the primary interface; the CLI covers what belongs in a
shell script or a recovery session.

```
publix serve                          run the server and dashboard
publix projects                       list projects and their live deployment
publix deploy <project> [-ref BRANCH] deploy now, streaming the build log
publix rollback <project> [-to ID]    roll back
publix logs <project> [-f]            runtime logs
publix logs <project> -build <ID>     a deployment's build log
publix volumes [-add name=/path]      list or register shared volumes
publix validate [DIR]                 check a deployment.yaml before pushing
publix reconcile                      rewrite Traefik's routing file
```

`publix deploy` exits non-zero if the deployment fails, so it works in CI.

`publix validate` is the one to run before you push:

```
$ publix validate
Checking /srv/app/deployment.yaml

  type        compose  (Docker Compose)
  compose     docker-compose.yml
  service     web
  port        3000
  replicas    1
  strategy    recreate
  domains     app.example.com
  volumes     disk0 → /shared/disk0

Looks good.
```

---

## How it works

**Routing is split in two.** A deployment's containers carry Traefik labels
defining their own service and a private URL — created with the deployment,
discovered by Traefik's Docker provider, gone when the containers are. Every
hostname that can *move* between deployments lives in a single file publix
owns in Traefik's file-provider directory.

That split is what makes a cutover atomic. Moving production traffic is one
`rename(2)` and a hot reload: no container restart, no dropped connections,
and nothing to undo if the new deployment never becomes healthy.

**A deploy proceeds in phases**, and only the last one is visible to users:

1. Check out the commit (the checkout is reused between deploys).
2. Read and resolve `deployment.yaml` against what is actually in the repo.
3. Build the image, or reuse it if this commit has been built before.
4. Start the new generation and health-check it on its private URL.
5. Move traffic. *This is the only step users can observe.*
6. Drain and reap the old generation; prune images past the retention.

A failure in steps 1–4 leaves the live deployment untouched.

**Image tags are keyed by commit**, so a redeploy or a rollback of a commit
already built resolves to an image that exists and skips the build entirely.

---

## Building from source

```
make build       # dashboard + binary
make test        # unit tests
make test-all    # includes tests against a real Docker daemon
make dev         # run the server with a live-reloading dashboard
```

The dashboard is a Svelte 5 app compiled into the Go binary, so a build
produces one self-contained file with no assets to deploy alongside it.

## Requirements

- Docker 20.10 or newer, reachable via `DOCKER_HOST` or the standard socket
- Traefik v2 or v3, with the Docker provider and a watched file provider
- `git` on the host, and `docker compose` for Compose projects
- Go 1.24 and Node 20+ to build from source

## Security notes

- The dashboard is protected by a password hashed with PBKDF2-SHA256.
  Sessions are signed rather than stored, so changing the password
  invalidates every session everywhere.
- Webhook signatures are verified on every request; an unsigned call is
  refused. This endpoint has to be internet-reachable, so nothing else would
  be safe.
- Secret environment values are never sent to the browser, and credentials
  in clone URLs are stripped from anything written to a build log.
- Bind publix to loopback and put it behind Traefik with TLS. It runs with
  access to the Docker socket, which is equivalent to root on the host.
