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

## Looking inside

The **Databases** page in the menu lists every project's databases with
their state and backups, and **Browse data** opens one: its tables,
collections or keys on the left with sizes and row counts, a page of rows
on the right (click any value to see it whole), and for Postgres, MySQL and
SQLite a query box.

Everything there is read-only. Postgres and MySQL reads run in a read-only
transaction with a 15 second timeout; SQLite is opened read-only with
`query_only`; a query must be a single statement. That protects against a
slip, not against the dashboard's own user — who can already deploy a
container holding the password.

| Engine | Lists | Shows |
| --- | --- | --- |
| postgres, mysql, sqlite | tables and views, with row counts and sizes | rows, 50 a page; any `SELECT` |
| mongo | collections | documents, filtered by a JSON query such as `{"status": "active"}` |
| redis | keys with type and TTL, filtered by a pattern (first 1000) | a string, or a list, hash, set, sorted set or stream page by page |

## Over MCP

`list_all_databases`, `explore_database`, `query_database`, `list_databases`, `create_database`, `update_database`, `delete_database`,
`restart_database`, `get_database_logs`, `backup_database`, `list_backups`,
`restore_database`, and for targets `list_backup_targets`,
`add_backup_target`, `test_backup_target`, `delete_backup_target`. The
server's instructions tell an agent to give a project its database this way
rather than by setting `DATABASE_URL` by hand. Passwords are masked in
everything the tools return, as they are for the browser.
