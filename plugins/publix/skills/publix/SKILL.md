---
name: publix
description: Deploy and operate apps on publix, the self-hosted Docker + Traefik platform. Use when writing or fixing a repository's deployment.yaml, deploying/rolling back a project, adding a database (postgres, mysql, mongo, redis, sqlite) or backups, mounting volumes (project vs shared), routing domains/TCP/ports, setting env or secrets, scheduled jobs, CI-triggered deploys, or moving an app's existing database into publix. Triggers - "publix", "deployment.yaml", "배포", "publix에 올려", "DB 붙여", "볼륨", "shared volume", "백업".
---

# publix

publix builds a GitHub repository on push and runs it behind Traefik on one
server. **Everything about how a repo runs lives in its `deployment.yaml`**
(also accepted: `deployment.yml`, `.publix/deployment.yaml`). The server only
holds what must not be in git: secrets, registered volumes, backup targets,
domains it owns.

Two ways to act on a server:
- **MCP** (`publix` server, tools like `deploy_project`, `create_database`) —
  preferred when connected. Check with a tool search for `publix`.
- **HTTP API** with a bearer token from `publix token` (on the server:
  `docker exec publix-publix-1 publix token --ttl 1h`), e.g.
  `POST /api/projects/{id}/deploy`. Routes mirror the MCP tools.

## Workflow for deploying a repo

1. Read the repo: Dockerfile? compose file? framework? what port does it
   listen on? what state does it keep (DB, uploads, cache)?
2. Write `deployment.yaml` — start minimal (below), add only what is needed.
3. State → `databases:` for a database, `volumes:` for files. Never keep
   data inside the container.
4. Secrets → `${secret.NAME}` in the yaml; the value is set on the server
   (`set_env` with `secret: true`, or dashboard → Environment).
5. Import (`import_repo`) or push; then follow `get_build_logs` until the
   deployment is `live`. A deploy call only *queues* a build.
6. Verify through the domain, then check `get_runtime_logs`.

## Minimal deployment.yaml

```yaml
name: my-app            # default: repo name
port: 3000              # the port the app listens on (PORT is injected too)
domains:
  - app.example.com     # DNS must point at the server
health:
  path: /healthz        # a path makes it an http check; without one: tcp on `port`
  grace: 60s
```

`type` is detected: compose file > Dockerfile > framework template
(nextjs, nuxt, sveltekit, remix, astro, nestjs, vite, node, go, django,
fastapi, flask, …) > static. Set it explicitly when the guess is wrong:
`dockerfile | compose | framework | static | image`.

Full field list: [references/deployment-yaml.md](references/deployment-yaml.md).

## Databases — let publix run them

```yaml
databases:
  - postgres                 # name "main" → DATABASE_URL, DATABASE_HOST/_PORT/_USER/_PASSWORD/_NAME
  - engine: redis
    name: cache              # → REDIS_URL
  - engine: sqlite           # → DATABASE_URL=file:/data/<name>/<name>.db, DATABASE_PATH
    name: app
```

- Engines: `postgres` (17), `mysql` (8.4), `mongo` (8.2 → `MONGODB_URL`/`_URI`),
  `redis` (7), `sqlite`. `version:` pins the image tag at creation.
- Created before the build on the first deploy that declares it; **never
  deleted** when removed from the file. Data volume survives project delete.
- Do **not** invent passwords or write `DATABASE_URL` by hand, and do not
  ship a `postgres` service in compose for publix — declare it instead.
- Map to app-specific names with `${db.KEY}`:
  `PRISMA_URL: ${db.DATABASE_URL}`.
- Precedence: publix meta < database vars < yaml `env` < dashboard env. A
  dashboard `DATABASE_URL` silently overrides the managed one — remove it.
- Backups: register a target (host dir or rclone: Google Drive, S3…) once,
  then per database `backup: {schedule, target, keep}`.

Details, migration of an existing DB into publix, backup/restore:
[references/databases.md](references/databases.md).

## Volumes — files that must survive a deploy

Volumes are **registered on the server by name**; a repo names one, never a
host path. Ask the server what exists (`get_settings` → volumes) first.

```yaml
volumes:
  - disk0                    # → /shared/disk0
  - name: data-ssd
    mountPath: /mnt/data
    subPath: uploads         # optional subdirectory
    readOnly: false
    services: [api]          # compose only; default = every service
```

| Kind | Project mounting it gets | Use for |
|---|---|---|
| project volume | `<host path>/<project-id>[/subPath]` | the app's own uploads, cache, files |
| shared volume | `<host path>` itself | a media library / dataset several apps share (consider `readOnly`) |

Rules: a project volume is isolated per project ID (rename-safe). Prefer
`databases:` over a SQLite file on a volume — volumes are often NFS, where
SQLite locking is unreliable, and managed DBs get backups.

## Env and secrets

```yaml
env:
  API_KEY: ${secret.API_KEY}          # server-side secret (fails the deploy if missing)
  LOG_LEVEL: ${secret.LOG_LEVEL:-info} # optional with default
  REGION: ${env.REGION}               # publix server's own env
  BUILD: ${publix.COMMIT}             # deployment metadata
  PG: ${db.DATABASE_URL}              # a managed database
```

Always injected: `PORT`, `PUBLIX=1`, `PUBLIX_PROJECT`, `PUBLIX_PROJECT_ID`,
`PUBLIX_COMMIT`, `PUBLIX_BRANCH`, `PUBLIX_DEPLOYMENT_ID`, `PUBLIX_URL`.
Compose: the env reaches every service *and* the compose process, so
`${VAR}` interpolation inside the compose file works — but prefer putting
values in deployment.yaml rather than `${...}` in compose.

`set_env` / `set_domains` **replace the whole list** — read the project
first, send back everything to keep. A secret sent back with an empty value
keeps its stored value.

## Compose stacks

```yaml
type: compose
compose: docker-compose.yml
service: web       # receives the domains
port: 3000         # port of that service
```

publix generates an override (labels, env, volumes, networks); the repo is
never modified. Routed services join the shared `publix` network; all
services join it when the project has a networked database. Compose always
uses `recreate` (brief gap on deploy). Don't publish ports in compose for
HTTP — Traefik routes by domain.

## Routing beyond one domain

- `routes:` — redirects (`redirectTo`), `path` + `stripPath`, `basicAuth`,
  `headers`, per-service routes in compose with `priority` + `matchAny`.
- `tcp:` — TLS passthrough by SNI (`passthrough: true` required).
- `ports:` — publish a raw host port (git over SSH, game servers); forces
  `release.strategy: recreate` and `replicas: 1`.
- `appsDomain: none` — drop the free `<slug>.<apps-domain>` hostname.

See [references/routing.md](references/routing.md).

## Release, health, jobs

- `release.strategy`: `blue-green` (default; zero downtime) or `recreate`
  (single-writer resources, published ports, small hosts). `autoRollback`
  keeps the old version serving when the health gate fails.
- `health.type`: `http` (path/status) | `tcp` | `exec` | `none`. Default:
  `exec` if `command`, `http` if `path`, else `tcp` on `port`. `grace` is how
  long startup may take. An image's own `HEALTHCHECK` wins.
- `cron:` — `{name, schedule, command: [...], timeout}` run from the live
  image with the same env and volumes.

## CI-built images

When the image must be built elsewhere (private submodules, GPU, long
builds): `type: image` (or compose with images pinned to
`${PUBLIX_COMMIT}`), turn the project's **autoDeploy off**, and have CI call
`POST /api/projects/<id-or-slug>/deploy` with a bearer token *after* pushing
the image. Otherwise the push webhook deploys before the image exists.

## Operating

Common tasks, MCP tool names, diagnosis, and server upgrade:
[references/operations.md](references/operations.md).

## Pitfalls

- Port mismatch (app listens on 8080, yaml says 3000) → health gate fails;
  read the build/runtime logs, not just the status.
- App binds `127.0.0.1` → unreachable; it must listen on `0.0.0.0`.
- `${secret.X}` that is not set → the deploy fails instead of starting the
  app with a blank value, by design (use `:-default` if optional).
- A volume name not registered on the server → deploy fails listing it.
- Two generations of SQLite/single-writer apps overlapping → use
  `release.strategy: recreate`.
- Removing a `databases:` entry does not delete data; delete explicitly
  with `delete_database` (+ `deleteData`) if really intended.
