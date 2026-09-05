# How publix works

## The problem it solves

Docker and Traefik will happily run and route containers. What they do not
give you is the part that makes a platform feel like one: knowing that the
new version works *before* users reach it, and being able to undo it when it
does not.

Everything below follows from that.

## Routing is split in two

This is the central design decision, and most of the properties publix
claims fall out of it.

**Immutable half — Docker labels.** A deployment's containers carry Traefik
labels defining their own service and a private hostname:

```
traefik.http.services.publix-api-1852b687.loadbalancer.server.port=8080
traefik.http.routers.publix-d-api-1852b687.rule=Host(`api-1852b687.apps.example.com`)
```

Traefik's Docker provider discovers this automatically, load-balances across
replicas, and drops it when the containers go away. Crucially, **nothing here
names a production domain.**

**Mutable half — a file publix owns.** Every hostname that can move between
deployments lives in one file in Traefik's file-provider directory:

```yaml
http:
  routers:
    publix-r-api-0:
      rule: Host(`api.example.com`)
      service: publix-api-1852b687@docker    # ← the only thing that changes
```

Traefik supports cross-provider service references, so a file router can
point at a Docker-discovered service.

### Why this matters

Container labels are immutable. If production domains lived on the
containers, moving traffic would mean recreating them — which is exactly
what you cannot do safely during a deploy, and cannot do *at all* for an
instant rollback.

By keeping the moving part in a file:

- **Cutover is atomic.** One `rename(2)`, then Traefik hot-reloads.
- **Cutover is reversible.** Nothing has been destroyed at the moment
  traffic moves.
- **A deployment can be tested before it serves.** It has a working private
  URL from the moment it starts, so the health gate exercises the real
  routing path.
- **Unrelated projects are undisturbed.** The file is rewritten only when its
  content actually changes.

## Detection reads the repository, not just its file names

Knowing a repository is "a Node project" is not enough to deploy it. A
SvelteKit app with `adapter-static` is a folder of files; the same app with
`adapter-node` is a long-running server. An Astro project is static until
someone sets `output: 'server'`. A Next.js app builds a one-gigabyte image
unless its config says `output: 'standalone'`, in which case it builds a
hundred-megabyte one.

None of that is visible from a directory listing. It is written in the
project's own config file, so that is what publix reads.

The same detection code runs in two places, against an abstraction over
"a repository" rather than over "a directory":

- **The import screen**, against the GitHub API. One call lists the root,
  and individual config files are fetched only when detection needs them —
  so publix can tell you a repository is a standalone Next.js app before it
  has cloned a byte of it.
- **The deploy**, against the checkout on disk.

Because both run the same code, what the import screen promises is what the
build does.

## Generated Dockerfiles

When a repository has no Dockerfile and no compose file, publix writes one:
a multi-stage build with dependencies in their own layer, a production-only
runtime stage, and an unprivileged user.

Three properties are deliberate:

- **It is never written to the repository.** The file is injected into the
  build context and discarded. Committing a Dockerfile is how you take
  over, and detection then defers to it completely.
- **It is printed into the build log.** It exists nowhere else, so that is
  the only place a user can see what was actually built.
- **It never installs system packages.** No `apk add`, no `apt-get`. A
  build that works on one network and fails on another is worse than one
  that never worked, because it fails only in production. Where a runtime
  needs CA certificates, they are copied from the build stage, which
  already has them.

## A deploy, phase by phase

```
checkout → resolve spec → build → start → health-gate → CUTOVER → drain → prune
└──────────────── previous version still serving ─────┘         └─ new version ─┘
```

1. **Checkout.** The commit is fetched into a per-project directory that is
   reused across deploys — the difference between a fifteen-second deploy and
   a two-minute one on a large repository.

2. **Resolve.** `deployment.yaml` is read and merged with what is actually in
   the repository: a Compose file, a Dockerfile, a framework's conventions.
   The result is validated in one pass that reports *every* problem, so
   fixing a spec is not one error per attempt. The resolved spec is stored on
   the deployment record.

3. **Build.** Images are tagged by commit, so a redeploy or a rollback of an
   already-built commit resolves to an image that exists and skips the build.

4. **Start.** The new generation comes up alongside the old one. Compose
   projects are the exception — Compose replaces containers in place, so two
   generations cannot coexist and those projects use `recreate`.

5. **Health gate.** Each replica is probed on the Docker network until it
   passes or the grace period expires. A container that has already exited
   fails immediately rather than burning the whole grace period. If the image
   declares its own `HEALTHCHECK`, that is trusted instead — it knows more
   about the app than publix can.

6. **Cutover.** The routing file is rewritten. This is the only step users
   can observe.

7. **Drain and prune.** The old generation finishes its in-flight requests,
   then is removed. Images past the retention are deleted.

A failure anywhere in 1–5 tears down the new containers and leaves the
previous deployment exactly as it was.

## One generation, two images

Only the live deployment keeps containers running. A host with twenty
projects runs twenty sets of containers, not sixty.

Two images per project are retained: the live one and the one before it. Two
is the floor at which a one-step rollback still needs no build, and rolling
back one step is what almost every rollback is. Older deployments are rebuilt
from their commit, which reproduces the same result more slowly.

## Rollback

A rollback re-creates the target deployment by whichever route is available:

- **Its image is on disk** — start it. This is what the retention guarantees
  for the previous deployment, and it is also the more faithful option: it is
  the exact artifact that was serving, not a rebuild that could differ if a
  base image has moved since.
- **Otherwise** — check out its commit and rebuild.

Either way the target's *recorded configuration* is restored with it. The
resolved `deployment.yaml` was stored on the deployment, so rolling back
brings back that deployment's domains and settings, not just its code.

## Shared volumes

The operator registers host directories on the server. A project asks for one
**by name** and never sees a path:

```yaml
volumes: [disk0]
```

publix mounts `<host path>/<project id>` at `/shared/disk0`. The per-project
subdirectory is the isolation boundary: two projects can both mount `disk0`
and neither can reach the other's files.

The directory is named after the **project ID**, which is generated once and
never changes — so renaming a project can never orphan its data or expose it
to another project that inherits the old name.

Deleting a project does not delete its shared-volume data. Unregistering a
volume does not either. Destroying data is always an explicit, separate act.

## Compose

publix does not reimplement Compose. A repository shipping a compose file has
described its stack precisely, often with dependency ordering and
healthchecks that took real effort. publix reads it to learn the shape, then
writes a **second** file with only its own additions — labels, environment,
shared volume binds — and lets Compose merge them. The repository is never
modified.

Two details worth knowing:

- **The Compose project name is stable across deploys.** Compose namespaces
  named volumes by project name, so a per-deployment name would hand every
  push a brand-new empty volume and silently destroy the stack's data.
- **Only the routed service joins the shared network**, attached after the
  stack is up rather than through the override. Compose's list-merge rules
  for `networks` would otherwise drop the stack's internal wiring.

## State

Docker is the source of truth for what is *running*. The store holds only
what labels cannot express: settings, secrets, and each deployment's resolved
spec. That split means publix can never disagree with reality about what is
actually up — and `publix reconcile` can rebuild Traefik's routing from
scratch at any time.

The store is a single JSON document written atomically. For a control plane
whose writes happen at human frequency, the operational value of "one file
you can read, back up, and edit" beats anything a database would add.
