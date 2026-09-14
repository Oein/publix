# publix over MCP

publix speaks the [Model Context Protocol](https://modelcontextprotocol.io),
so an AI agent can operate it directly: list projects, read a failing build
log, set an environment variable, deploy, roll back, register a domain —
everything the dashboard can do, as 37 tools.

There is no second implementation behind them. Each tool is dispatched, in
process, through the same routing tree that serves the dashboard, so a tool
gets the same validation, the same conflict checks and the same error
messages as the button that does the job in the UI. A rule the dashboard
enforces is a rule the agent cannot get around.

---

## Connecting

### Claude Code, on the server

```bash
claude mcp add publix -- publix mcp
```

That is the whole configuration. Run on the publix host, `publix mcp` mints
its own token from the state file and talks to the server on
`127.0.0.1:4321`.

### From another machine

Mint a token on the server:

```bash
publix token
```

It prints a bearer token valid for 90 days. Then, on the client:

```bash
claude mcp add publix \
  --env PUBLIX_URL=https://publix.example.com \
  --env PUBLIX_TOKEN=<the token> \
  -- publix mcp
```

`publix mcp` refuses to mint a token for a remote server, because a token is
signed with one server's key and no other server would accept it.

### Any MCP client

The command speaks the stdio transport, which is what most clients expect:

| Field | Value |
| --- | --- |
| Command | `publix` |
| Arguments | `mcp` |
| Environment | `PUBLIX_URL`, `PUBLIX_TOKEN` (both optional on the server itself) |

### Over HTTP, without the CLI

The server exposes the Streamable HTTP transport at `/api/mcp` directly, for
a client that speaks it:

```
POST https://publix.example.com/api/mcp
Authorization: Bearer <token>
Content-Type: application/json
```

`publix mcp` is a pump between stdio and this endpoint and nothing more, so
the two routes offer exactly the same tools.

---

## What the tools cover

**Projects** — `list_projects`, `get_project`, `create_project`,
`update_project`, `delete_project`

**Deploying** — `deploy_project`, `rollback_project`, `rollback_plan`,
`cancel_deployment`, `get_deployment`

**Diagnosis** — `get_build_logs`, `get_runtime_logs`, `list_containers`,
`get_system`

**Project configuration** — `set_env`, `set_domains`, `run_cron_job`

**GitHub** — `github_status`, `connect_github`, `disconnect_github`,
`list_repos`, `inspect_repo`, `import_repo`

**Server settings** — `get_settings`, `update_settings`,
`add_apps_domain`, `set_default_apps_domain`, `delete_apps_domain`

**Routing** — `add_proxy`, `update_proxy`, `delete_proxy`, `add_redirect`,
`update_redirect`, `delete_redirect`

**Storage** — `add_volume`, `delete_volume`

**Account** — `change_password`

Tools carry the protocol's annotations, so a client can tell a read from a
deploy from a deletion and ask for confirmation where it matters.

---

## Things worth knowing

**A deploy is asynchronous.** `deploy_project` returns once the build is
queued. It succeeding means the build started, not that it worked — the
outcome is in `get_build_logs`. Traffic only moves once the new version
passes its health check, so a failed build leaves the previous version
serving.

**`set_env` and `set_domains` replace the whole list.** They are not
additive. An agent has to read the project first and send back everything it
means to keep. This is the dashboard's behaviour too, and the tool
descriptions say so plainly.

**Secrets stay secret.** Secret values are redacted on the way out, the same
as they are for the browser, so an agent can see that `DATABASE_URL` exists
without being able to read it. Sending a secret back with an empty value
keeps the stored value — which is what makes editing one variable safe.

**Logs are trimmed.** `get_build_logs` returns the last 200 lines by
default, from the end, where the explanation of a failure usually is. Ask
for more with `tail`.

---

## Security

The MCP endpoint is the entire dashboard behind one URL, and it is protected
by exactly the same credential — there is no second kind of key and no
weaker path in.

- A token is the same signed session the dashboard issues. Changing the
  dashboard password revokes every token along with every browser session.
- A token grants everything. publix has one account and no notion of a
  reduced-permission agent, so an agent holding a token can delete a project
  as readily as it can list one. Give one out on that basis.
- `publix token` reads the state file directly, so it needs the same access
  to the host that running publix does.
- Tokens last 90 days by default; `publix token -ttl 24h` issues a shorter
  one for a session you do not want outliving the day.

To revoke a token before it expires, change the dashboard password. That is
the only revocation there is, and it logs out every browser too.
