# Operating publix

## Reaching it

- **MCP**: `claude mcp add publix -- publix mcp` on the server; from elsewhere
  `claude mcp add publix --env PUBLIX_URL=https://<dashboard> --env PUBLIX_TOKEN=<token> -- publix mcp`,
  or streamable HTTP at `POST https://<dashboard>/api/mcp` with `Authorization: Bearer <token>`.
- **Token**: `publix token [--ttl 24h]` on the server (in the container:
  `docker exec publix-publix-1 publix token --ttl 1h`). It grants
  everything; changing the dashboard password revokes all tokens.
- **HTTP API**: same routes the dashboard uses, `{id}` is the project ID
  or slug:

| Action | Request |
|---|---|
| list / get project | `GET /api/projects`, `GET /api/projects/{id}` |
| update (autoDeploy, …) | `PATCH /api/projects/{id}` |
| deploy | `POST /api/projects/{id}/deploy` `{}` |
| env (replaces all) | `PUT /api/projects/{id}/env` `{"env":[{"key","value","secret"}]}` |
| databases | `GET/POST /api/projects/{id}/databases`, `PATCH/DELETE …/databases/{name}` |
| backup now / list / restore | `POST …/databases/{name}/backups`, `GET …/backups`, `POST …/restore` |
| backup targets | `GET/POST /api/backup-targets` |

## MCP tools

- Projects: `list_projects`, `get_project`, `create_project`, `update_project`, `delete_project`
- Deploying: `deploy_project`, `rollback_project`, `rollback_plan`, `cancel_deployment`, `get_deployment`
- Diagnosis: `get_build_logs`, `get_runtime_logs`, `list_containers`, `get_system`
- Config: `set_env`, `set_domains`, `run_cron_job`
- Databases: `list_databases`, `create_database`, `update_database`, `delete_database`, `restart_database`, `get_database_logs`, `backup_database`, `list_backups`, `restore_database`
- GitHub: `github_status`, `connect_github`, `disconnect_github`, `list_repos`, `inspect_repo`, `import_repo`
- Server: `get_settings`, `update_settings`, `add_apps_domain`, `set_default_apps_domain`, `delete_apps_domain`
- Routing: `add_proxy`, `update_proxy`, `delete_proxy`, `add_redirect`, `update_redirect`, `delete_redirect`
- Storage: `add_volume`, `delete_volume`; backup targets: `list_backup_targets`, `add_backup_target`, `test_backup_target`, `delete_backup_target`

## Deploy lifecycle

`queued → building → deploying (health gate) → live`, or `failed` /
`cancelled` / later `superseded`; failure leaves the
previous version serving (`autoRollback`). `deploy_project` returns when
queued — poll `get_deployment` / `get_build_logs` (last 200 lines; ask for
more with `tail`). Pushing to the release branch deploys when the project's
autoDeploy is on.

## Diagnosing a failed deploy

1. `get_build_logs` — the end usually names the problem (missing secret,
   unregistered volume, port conflict, image not found).
2. Health gate failure → `get_runtime_logs`: wrong `port`, app bound to
   127.0.0.1, slow startup (raise `health.grace`), crash on missing env.
3. Compose: `image not found` for a tag pinned to the commit means CI has not
   pushed it yet — deploy after CI, autoDeploy off.
4. Database not ready → `get_database_logs`; `restart_database`.

## Container names on the host

- app: `publix-<slug>-<deploymentId>-<n>`; compose: `publix-<slug>-<service>-1`
- database: `publix-db-<projectId>-<name>` (also its hostname on the
  `publix` network); data volume `publix-db-<projectId>-<dbId>`
- Traefik: `publix-traefik-1`; publix itself: `publix-publix-1`

## Volumes: registering one (server side)

publix runs in a container but creates containers on the host, so a host
path must be mounted into publix **at the same path**: add
`- /mnt/data:/mnt/data` to `deploy/docker-compose.override.yml`
(never the tracked compose file), `docker compose up -d`, then register in
Settings → Volumes (project or shared). System dirs and publix's own state
are refused; volumes cannot overlap.

## Upgrading the server

```sh
cd /opt/publix
sudo git fetch origin main && sudo git checkout --force -B main origin/main
sudo ./scripts/install.sh --email <acme-email> --skip-docker
```

Projects keep running; only publix restarts. Settings → Server shows the
running commit. State lives in `/var/lib/publix` (back it up).
