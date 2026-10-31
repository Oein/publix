# Databases and backups

publix can run a project's database for it. Adding one is a single step:
publix pulls the image, creates a data volume, generates credentials, waits
until the server accepts connections, and hands the connection details to
every container of the project as environment variables. Nobody types a
password or a `DATABASE_URL`.

| Engine | Image (default tag) | Variables, for the first of its kind |
| --- | --- | --- |
| `postgres` | `postgres:17-alpine` | `DATABASE_URL`, `DATABASE_HOST`, `_PORT`, `_USER`, `_PASSWORD`, `_NAME` |
| `mysql` | `mysql:8.4` | `DATABASE_URL`, `DATABASE_HOST`, `_PORT`, `_USER`, `_PASSWORD`, `_NAME` |
| `mongo` | `mongo:8.2` | `MONGODB_URL`, `MONGODB_URI`, `MONGODB_HOST`, `_PORT`, `_USER`, `_PASSWORD`, `_NAME` |
| `redis` | `redis:7-alpine` | `REDIS_URL`, `REDIS_HOST`, `REDIS_PORT`, `REDIS_PASSWORD` |
| `sqlite` | — | `DATABASE_URL` (`file:/data/main/main.db`), `DATABASE_PATH` |

A second database of the same kind is prefixed with its own name instead —
a Postgres called `analytics` gives `ANALYTICS_URL` — and the prefix can be
set explicitly. Two databases can never claim the same variable.

## Adding one

**From the repository.** Declare it in `deployment.yaml`:

```yaml
databases:
  - postgres                 # main, DATABASE_URL

  - engine: redis
    name: cache              # REDIS_URL

  - engine: mongo
    version: "7"
    envPrefix: DOCS          # DOCS_URL, DOCS_URI, …

env:
  # map a database onto whatever name the app reads
  PRISMA_DATABASE_URL: ${db.DATABASE_URL}
```

A declared database is created on the first deploy that mentions it, before
the build starts, so a deploy that needs one fails in seconds if it cannot
have it, and the server is already accepting connections when the app first
starts. Removing it from the file does **not** delete it: deleting a
database is too consequential to happen because a line went missing in a
merge.

**From the dashboard or an agent.** The project's **Databases** tab, or the
`create_database` MCP tool. The variables reach containers when they are
created, so redeploy afterwards.

### Precedence

publix's own variables < databases < `deployment.yaml` env < the
dashboard's Environment tab. Setting `DATABASE_URL` in the dashboard
therefore points the app at an external database without removing the
managed one.

## How it runs

- Each server runs in its own container, `publix-db-<project id>-<name>`,
  which is also its hostname on the shared network. It is built from IDs, so
  renaming a project does not move the address in its `DATABASE_URL`.
- Data lives in a docker volume named after the database's own ID, so a
  database deleted and re-created under the same name never picks up the
  old data — or the old password, which the engine recorded at first start.
- Servers are reached only over the private network and have no TLS, so a
  Postgres URL carries `?sslmode=disable`; clients such as lib/pq would
  otherwise insist on TLS and refuse to connect. Every service of a compose
  stack joins that network when the project has a database, routed or not.
- Containers restart with the host (`unless-stopped`), and publix recreates
  any whose container has gone missing when it starts.
- Redis runs with `appendonly yes`: asked for a database, nobody expects to
  lose writes since the last snapshot on a restart.
- SQLite has no server. Its directory is a volume mounted at
  `/data/<name>` in every container of the project, compose services and
  scheduled jobs included, made writable for whatever user the image runs
  as.
- Deleting a project, or a database, removes the container and **keeps the
  data volume**. The reply names it; `deleteData` removes it as well.

## Backups

Backups go to a **backup target**, registered under **Settings → Backups**:

- **A directory** on the host — a second disk, an NFS or USB mount. The same
  paths are refused as for volumes.
- **rclone** — Google Drive, S3, Backblaze B2, Dropbox, OneDrive, SFTP and
  anything else rclone supports. Paste the remote's section of an
  `rclone.conf`; it is stored on the server and never returned.

For Google Drive:

```bash
rclone config               # n → name it gdrive → storage "drive" → log in in the browser
rclone config show gdrive   # paste this into publix, with remote gdrive:publix-backups
```

This can be done on a laptop; the server never needs a browser. publix
writes and deletes a test file before saving a target, so a wrong path or
an expired token is reported then, not at 3am.

Each database then gets a schedule (cron, or `@daily`) and a retention
count on its Databases tab, or with `update_database`. **Back up now** and
**Restore** are on the same tab.

| Engine | Dump | Restore |
| --- | --- | --- |
| postgres | `pg_dump -Fc` (`.dump`) | `pg_restore --clean --if-exists` |
| mysql | `mysqldump --single-transaction` (`.sql`) | `mysql < file` |
| mongo | `mongodump --archive --gzip` (`.archive.gz`) | `mongorestore --drop` |
| redis | `redis-cli --rdb` (`.rdb`) | stop, swap the snapshot, rebuild the AOF, start |
| sqlite | SQLite's online backup API, then `tar` (`.tar.gz`) | replace the directory; redeploy afterwards |

Dumps are taken with the database's own image, so `pg_dump` always matches
the server's major version; SQLite goes through its backup API (in
`python:3.12-alpine`), which takes a consistent copy while the app keeps
writing and folds in the `-wal` file, and everything runs in throwaway containers.
That is why backups work the same whether publix runs on the host or in a
container. Files are laid out as

```
<target>/<project id>/<database>/<project>-<database>-20261029-030000.dump
```

so retention only ever prunes one database's own files, and a database
re-created under the same name can be restored from its predecessor's
backups. Backups keep running for a paused project.

## Backup policy fields

`update_database` / `PATCH /api/projects/{id}/databases/{name}`:

```json
{"backup": {"schedule": "30 4 * * *", "target": "truenas-hdd", "keep": 14}}
```

`schedule` is 5-field cron or `@daily`/`@hourly`/`@weekly`; `keep` is how
many of this database's files stay on the target. Register targets first
(`add_backup_target`): `{"name","type":"local","path"}` or
`{"name","type":"rclone","remote":"gdrive:publix-backups","rcloneConfig":"[gdrive]\n..."}`.
publix test-writes before saving.

## Moving an existing database into publix

For an app that already runs its own Postgres (compose service) or keeps a
SQLite file on a volume. Data is copied while the app is stopped, so nothing
is written in between. Keep the old data in place until the new one is
verified.

1. **Patch the repo on a branch**: add `databases:` (same engine; for
   Postgres pin `version:` to the old major), remove the DB service and its
   `pgdata` volume from compose, remove hand-written `DATABASE_URL` /
   `POSTGRES_PASSWORD` from `env`, drop `depends_on: db`. For SQLite, point
   the app at `${db.DATABASE_URL}` / `/data/<name>/` and drop a volume that
   only held the file. Open a PR.
2. **Stop auto-deploy** on the project (`update_project autoDeploy:false`)
   so merging does not deploy before the data is there. (CI-deployed
   projects: merging triggers CI, which deploys at its end — migrate first.)
3. **Create the database** with the same name the yaml declares
   (`create_database`); the deploy adopts it.
4. **Stop the app containers** (`docker stop publix-<slug>-...`), not the old DB.
5. **Copy the data**
   - Postgres:
     ```sh
     docker exec <old-db> pg_dump -U <olduser> -Fc <olddb> > /var/backups/x.dump
     docker exec -i publix-db-<projectId>-main pg_restore -U app -d main --no-owner --no-acl --exit-on-error < /var/backups/x.dump
     ```
     Compare per-table row counts in both.
   - SQLite: copy `<file>`, plus `-wal`/`-shm` if present, into the volume
     `publix-db-<projectId>-<dbId>` as `<name>.db` (an `alpine` container
     mounting both), `chmod 666`, compare `sha256sum`.
6. **Remove dashboard env that overrides it** (e.g. a `DATABASE_URL` set in
   the dashboard), merge the PR, deploy, wait for `live`.
7. **Verify** through the app (real records, not just a 200), then turn
   autoDeploy back on, set a backup policy and take a first backup.

Managed Postgres: user `app`, database = the db name (`main`), host
`publix-db-<projectId>-<name>`, URL ends in `?sslmode=disable`.
