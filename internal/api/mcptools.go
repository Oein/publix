package api

import (
	"encoding/json"
	"net/http"

	"github.com/Oein/publix/internal/mcp"
	"github.com/Oein/publix/internal/store"
)

// mcpTool describes one dashboard capability declaratively: the HTTP call it
// makes and the arguments that feed it. See mcp.go for how it is run.
type mcpTool struct {
	name  string
	title string
	desc  string

	method string
	// path is the route, with {placeholders} filled from arguments of the
	// same name. Those arguments are not also sent in the body.
	path string
	// query names arguments sent as query parameters.
	query []string
	// local names arguments this layer handles itself and never forwards.
	local []string
	// alias renames an argument to the field the handler reads.
	alias map[string]string

	schema mcp.Schema
	hints  mcp.Annotations

	// post rewrites a successful response before the model sees it.
	post func(args map[string]json.RawMessage, body []byte) []byte
}

// Schema helpers. They exist to keep the catalogue below readable: it is a
// long list, and what matters in it is the prose, not the plumbing.

func mcpString(desc string) mcp.Property {
	return mcp.Property{Type: "string", Description: desc}
}

func mcpBool(desc string) mcp.Property {
	return mcp.Property{Type: "boolean", Description: desc}
}

func mcpInt(desc string) mcp.Property {
	return mcp.Property{Type: "integer", Description: desc}
}

func mcpStringList(desc string) mcp.Property {
	return mcp.Property{Type: "array", Description: desc, Items: &mcp.Property{Type: "string"}}
}

func mcpEnum(desc string, values ...string) mcp.Property {
	return mcp.Property{Type: "string", Description: desc, Enum: values}
}

func schemaOf(required []string, props map[string]mcp.Property) mcp.Schema {
	return mcp.Schema{Type: "object", Properties: props, Required: required}
}

func boolPtr(b bool) *bool { return &b }

// Annotation helpers, named for what the tool does to the server.

func readsOnly() mcp.Annotations {
	return mcp.Annotations{ReadOnlyHint: true, DestructiveHint: boolPtr(false), IdempotentHint: true}
}

// reachesGitHub marks a read that leaves this server, which is worth
// distinguishing: it can be slow, and it can fail for reasons publix cannot
// fix.
func reachesGitHub() mcp.Annotations {
	a := readsOnly()
	a.OpenWorldHint = boolPtr(true)
	return a
}

// creates marks a tool that adds something without disturbing what is there.
func creates() mcp.Annotations {
	return mcp.Annotations{DestructiveHint: boolPtr(false)}
}

// replaces marks a tool that overwrites existing configuration.
func replaces() mcp.Annotations {
	return mcp.Annotations{DestructiveHint: boolPtr(true), IdempotentHint: true}
}

// removes marks a tool that takes something away.
func removes() mcp.Annotations {
	return mcp.Annotations{DestructiveHint: boolPtr(true), IdempotentHint: true}
}

// Common argument shapes.

func projectArg() mcp.Property {
	return mcpString("The project's id or slug, exactly as list_projects reports it.")
}

func envArg() mcp.Property {
	return mcp.Property{
		Type:        "array",
		Description: "The complete set of environment variables. This REPLACES what is stored, so include every variable you want to keep.",
		Items: &mcp.Property{
			Type:     "object",
			Required: []string{"key"},
			Properties: map[string]mcp.Property{
				"key":    mcpString("The variable name, e.g. DATABASE_URL."),
				"value":  mcpString("The value. For a variable already stored as a secret, leaving this empty keeps the stored value rather than clearing it."),
				"secret": mcpBool("Hide the value from the dashboard and from these tools once saved. Use it for credentials."),
			},
		},
	}
}

func databaseArg() mcp.Property {
	return mcpString("The database's name within the project, as list_databases reports it.")
}

func backupPolicyArg() mcp.Property {
	return mcp.Property{
		Type:        "object",
		Description: "Automatic backups. An empty schedule turns them off.",
		Properties: map[string]mcp.Property{
			"schedule": mcpString("A cron expression or @daily, @hourly, @weekly. Times are the server's."),
			"target":   mcpString("The backup target's name. May be omitted when only one is registered."),
			"keep":     mcpInt("How many backups to keep on the target; older ones are deleted. Defaults to 7, 0 keeps all."),
		},
	}
}

// mcpTools is the catalogue: every capability the publix dashboard has.
//
// The grouping is the order a client lists them in, so it follows the
// dashboard's own shape — projects first, then the repository connection,
// then server-wide settings.
func (s *Server) mcpTools() []mcpTool {
	return []mcpTool{
		// --- Projects -------------------------------------------------

		{
			name:   "list_projects",
			title:  "List projects",
			desc:   "List every project with its URL, hostnames, detected framework, whether a build is running, and its live and most recent deployments. Start here: the id and slug that every other project tool needs come from this listing.",
			method: http.MethodGet,
			path:   "/api/projects",
			hints:  readsOnly(),
		},
		{
			name:   "get_project",
			title:  "Get a project",
			desc:   "Everything publix knows about one project: its repository and branch, hostnames, environment variables (secret values redacted), settings, and its deployment history with the status of each attempt.",
			method: http.MethodGet,
			path:   "/api/projects/{project}",
			schema: schemaOf([]string{"project"}, map[string]mcp.Property{"project": projectArg()}),
			hints:  readsOnly(),
		},
		{
			name:   "create_project",
			title:  "Create a project",
			desc:   "Create a project by hand, without going through GitHub import. Prefer import_repo when GitHub is connected — it also registers the push webhook and starts the first deploy. This is the right tool for a repository publix has no credentials for, or for setting up a project before its code exists.",
			method: http.MethodPost,
			path:   "/api/projects",
			schema: schemaOf([]string{"name"}, map[string]mcp.Property{
				"name":       mcpString("A human name for the project. Its slug, and so its generated URL, is derived from this."),
				"repo":       mcpString("The GitHub repository in owner/name form."),
				"branch":     mcpString("The branch to deploy. Defaults to main."),
				"rootDir":    mcpString("Subdirectory to build from, for a monorepo. Defaults to the repository root."),
				"specPath":   mcpString("Path to deployment.yaml if it is not at the root of rootDir."),
				"domains":    mcpStringList("Custom hostnames this project should answer on, in addition to its generated URL."),
				"autoDeploy": mcpBool("Deploy automatically when the branch is pushed."),
				"appsDomain": mcpString("Which registered apps domain the generated hostname sits under. Omit for the server default; \"none\" opts out of a generated hostname entirely."),
			}),
			hints: creates(),
		},
		{
			name:   "update_project",
			title:  "Update a project",
			desc:   "Change a project's settings. Only the fields you send are touched, so this is safe to use for a single setting. Changing domains here replaces the whole list, exactly as set_domains does.",
			method: http.MethodPatch,
			path:   "/api/projects/{project}",
			schema: schemaOf([]string{"project"}, map[string]mcp.Property{
				"project":     projectArg(),
				"name":        mcpString("A new human name. This does not change the slug or the generated URL."),
				"description": mcpString("A short note about what this project is."),
				"branch":      mcpString("The branch to deploy."),
				"rootDir":     mcpString("Subdirectory to build from."),
				"specPath":    mcpString("Path to deployment.yaml."),
				"autoDeploy":  mcpBool("Deploy automatically when the branch is pushed."),
				"paused":      mcpBool("Pause the project: pushes stop triggering deploys. What is already running keeps serving."),
				"domains":     mcpStringList("Custom hostnames, replacing the current list."),
				"appsDomain":  mcpString("Which registered apps domain the generated hostname sits under. \"none\" opts out of one."),
			}),
			hints: replaces(),
		},
		{
			name:   "delete_project",
			title:  "Delete a project",
			desc:   "Remove a project: stop and delete its containers, drop its routing, and unregister its GitHub webhook. This cannot be undone and the project stops serving immediately. Data in its volumes is deliberately left on the host, and the reply says where.",
			method: http.MethodDelete,
			path:   "/api/projects/{project}",
			schema: schemaOf([]string{"project"}, map[string]mcp.Property{"project": projectArg()}),
			hints:  removes(),
		},

		// --- Deploying ------------------------------------------------

		{
			name:   "deploy_project",
			title:  "Deploy a project",
			desc:   "Start a deployment and return as soon as it is queued — the build runs in the background, so this returning successfully means it started, not that it worked. Follow it with get_build_logs, or with get_project to see whether the live deployment changed. Traffic only moves once the new version passes its health check; a failed build leaves the current version serving.",
			method: http.MethodPost,
			path:   "/api/projects/{project}/deploy",
			schema: schemaOf([]string{"project"}, map[string]mcp.Property{
				"project": projectArg(),
				"ref":     mcpString("A branch, tag or commit SHA to deploy. Defaults to the project's configured branch."),
				"force":   mcpBool("Rebuild even when the commit has already been built, ignoring the image cache."),
			}),
			hints: creates(),
		},
		{
			name:   "rollback_project",
			title:  "Roll back a project",
			desc:   "Return a project to an earlier deployment, bringing back the configuration recorded with it as well as its code. Recent deployments still have their image and come back without a build; older ones are rebuilt from their commit. Call rollback_plan first if it matters which of the two this will be.",
			method: http.MethodPost,
			path:   "/api/projects/{project}/rollback",
			schema: schemaOf([]string{"project"}, map[string]mcp.Property{
				"project":    projectArg(),
				"deployment": mcpString("The deployment id to return to. Defaults to the one before the live deployment."),
			}),
			hints: replaces(),
		},
		{
			name:   "rollback_plan",
			title:  "Preview a rollback",
			desc:   "Say what rolling back would do before committing to it: whether the image is still held, so the rollback is instant, or whether it would have to be rebuilt from source.",
			method: http.MethodGet,
			path:   "/api/projects/{project}/rollback-plan",
			query:  []string{"deployment"},
			schema: schemaOf([]string{"project"}, map[string]mcp.Property{
				"project":    projectArg(),
				"deployment": mcpString("The deployment id to consider. Defaults to the one before the live deployment."),
			}),
			hints: readsOnly(),
		},
		{
			name:   "cancel_deployment",
			title:  "Cancel a running deployment",
			desc:   "Stop the deployment currently building for a project. What is already serving is untouched.",
			method: http.MethodPost,
			path:   "/api/projects/{project}/cancel",
			schema: schemaOf([]string{"project"}, map[string]mcp.Property{"project": projectArg()}),
			hints:  removes(),
		},
		{
			name:   "get_deployment",
			title:  "Get a deployment",
			desc:   "One deployment in detail: its status, the commit it built, how long it took, and why it failed if it did.",
			method: http.MethodGet,
			path:   "/api/projects/{project}/deployments/{deployment}",
			schema: schemaOf([]string{"project", "deployment"}, map[string]mcp.Property{
				"project":    projectArg(),
				"deployment": mcpString("The deployment id, as get_project's history reports it."),
			}),
			hints: readsOnly(),
		},

		// --- Logs -----------------------------------------------------

		{
			name:   "get_build_logs",
			title:  "Get build logs",
			desc:   "The build output for one deployment — what the Dockerfile did, and what it said when it failed. This is the first place to look when a deploy did not work.",
			method: http.MethodGet,
			path:   "/api/projects/{project}/deployments/{deployment}/logs",
			local:  []string{"tail"},
			schema: schemaOf([]string{"project", "deployment"}, map[string]mcp.Property{
				"project":    projectArg(),
				"deployment": mcpString("The deployment id, as get_project's history reports it."),
				"tail": mcp.Property{
					Type:        "integer",
					Description: "How many lines from the end to return. Defaults to 200. A build failure is usually explained in the last few.",
					Default:     200,
				},
			}),
			hints: readsOnly(),
			post:  tailLines,
		},
		{
			name:   "get_runtime_logs",
			title:  "Get runtime logs",
			desc:   "Recent output from a project's running containers — what the application itself is saying, as opposed to what its build said. Fails if the project has nothing running.",
			method: http.MethodGet,
			path:   "/api/projects/{project}/runtime-logs",
			query:  []string{"tail"},
			schema: schemaOf([]string{"project"}, map[string]mcp.Property{
				"project": projectArg(),
				"tail": mcp.Property{
					Type:        "integer",
					Description: "How many lines from the end of each container's output. 1 to 5000, defaulting to 200.",
					Default:     200,
				},
			}),
			hints: readsOnly(),
		},
		{
			name:   "list_containers",
			title:  "List a project's containers",
			desc:   "The containers a project is running, with their state and live CPU and memory use. Use it to tell a project that is deployed from one that is actually healthy.",
			method: http.MethodGet,
			path:   "/api/projects/{project}/containers",
			schema: schemaOf([]string{"project"}, map[string]mcp.Property{"project": projectArg()}),
			hints:  readsOnly(),
		},

		// --- Project configuration ------------------------------------

		{
			name:   "set_env",
			title:  "Set environment variables",
			desc:   "Replace a project's environment variables. This is a whole-list replacement: read the project first and send back everything you mean to keep, or the omitted variables are removed. Secret values are never readable — send a secret back with an empty value to keep what is stored. Changes take effect on the next deployment, not immediately.",
			method: http.MethodPut,
			path:   "/api/projects/{project}/env",
			schema: schemaOf([]string{"project", "env"}, map[string]mcp.Property{
				"project": projectArg(),
				"env":     envArg(),
			}),
			hints: replaces(),
		},
		{
			name:   "set_domains",
			title:  "Set custom domains",
			desc:   "Replace the custom hostnames a project answers on. This is a whole-list replacement, and it does not affect the generated URL, which the project keeps either way. A hostname already claimed by another project is refused. Point DNS at this server before relying on a new name.",
			method: http.MethodPut,
			path:   "/api/projects/{project}/domains",
			schema: schemaOf([]string{"project", "domains"}, map[string]mcp.Property{
				"project": projectArg(),
				"domains": mcpStringList("The complete list of bare hostnames, e.g. [\"app.example.com\"]. Send an empty list to remove them all."),
			}),
			hints: replaces(),
		},
		{
			name:   "run_cron_job",
			title:  "Run a scheduled job now",
			desc:   "Trigger one of the scheduled jobs from the project's deployment.yaml immediately, without waiting for its schedule. Returns as soon as the job is started.",
			method: http.MethodPost,
			path:   "/api/projects/{project}/cron/{job}/run",
			schema: schemaOf([]string{"project", "job"}, map[string]mcp.Property{
				"project": projectArg(),
				"job":     mcpString("The job's name, as it appears under cron in the project's deployment.yaml."),
			}),
			hints: creates(),
		},

		// --- Databases ------------------------------------------------

		{
			name:   "list_all_databases",
			title:  "List every database on the server",
			desc:   "Every database publix runs, across all projects, with the project each belongs to, its engine and version, whether it is running, its environment variables (password masked) and backup schedule.",
			method: http.MethodGet,
			path:   "/api/databases",
			schema: schemaOf(nil, map[string]mcp.Property{}),
			hints:  readsOnly(),
		},
		{
			name:   "list_databases",
			title:  "List a project's databases",
			desc:   "The databases publix runs for a project: engine, version, whether the server is running, the environment variables each one gives the project (password masked), its backup schedule and recent backup history. Read this to learn which variable names an app should use, e.g. DATABASE_URL or REDIS_URL.",
			method: http.MethodGet,
			path:   "/api/projects/{project}/databases",
			schema: schemaOf([]string{"project"}, map[string]mcp.Property{"project": projectArg()}),
			hints:  readsOnly(),
		},
		{
			name:   "create_database",
			title:  "Add a database to a project",
			desc:   "Start a database for a project and wire it in. publix pulls the image, creates a data volume, generates credentials and waits until the server accepts connections; the project's containers then receive the connection details as environment variables — <PREFIX>_URL plus _HOST, _PORT, _USER, _PASSWORD and _NAME (mongo also gets _URI; sqlite gets _URL and _PATH). The first SQL or SQLite database gets the prefix DATABASE (so DATABASE_URL), the first redis REDIS, the first mongo MONGODB; later ones are named after the database. The variables only reach containers created after this, so call deploy_project afterwards. A repository can ask for the same thing declaratively under `databases:` in deployment.yaml, which is the better choice when the app depends on it.",
			method: http.MethodPost,
			path:   "/api/projects/{project}/databases",
			schema: schemaOf([]string{"project", "engine"}, map[string]mcp.Property{
				"project":   projectArg(),
				"engine":    mcpEnum("Which database to run.", "postgres", "mysql", "mongo", "redis", "sqlite"),
				"name":      mcpString("A name within the project, lowercase with dashes. Defaults to main (cache for redis)."),
				"version":   mcpString("The image tag. Defaults to postgres 17, mysql 8.4, mongo 8.2, redis 7. Fixed once created."),
				"envPrefix": mcpString("The variable prefix, e.g. DATABASE for DATABASE_URL. Omit for the conventional default."),
				"backup":    backupPolicyArg(),
			}),
			hints: creates(),
		},
		{
			name:   "update_database",
			title:  "Change a database's settings",
			desc:   "Change a database's environment variable prefix or its automatic backup schedule. Engine, version and credentials cannot change. A new prefix reaches containers on the next deploy. Send backup with an empty schedule to turn scheduled backups off.",
			method: http.MethodPatch,
			path:   "/api/projects/{project}/databases/{database}",
			schema: schemaOf([]string{"project", "database"}, map[string]mcp.Property{
				"project":   projectArg(),
				"database":  databaseArg(),
				"envPrefix": mcpString("A new variable prefix."),
				"backup":    backupPolicyArg(),
			}),
			hints: replaces(),
		},
		{
			name:   "delete_database",
			title:  "Delete a database",
			desc:   "Stop and remove a project's database. Its data volume is kept unless deleteData is true; with deleteData the data is gone for good, so take a backup_database first. The project keeps working only if nothing still uses the variables, and loses them on its next deploy.",
			method: http.MethodDelete,
			path:   "/api/projects/{project}/databases/{database}",
			query:  []string{"deleteData"},
			schema: schemaOf([]string{"project", "database"}, map[string]mcp.Property{
				"project":    projectArg(),
				"database":   databaseArg(),
				"deleteData": mcpBool("Also delete the data volume. Irreversible."),
			}),
			hints: removes(),
		},
		{
			name:   "restart_database",
			title:  "Restart a database",
			desc:   "Restart a database server, or recreate its container if it has gone missing, and wait until it accepts connections again. Data is kept.",
			method: http.MethodPost,
			path:   "/api/projects/{project}/databases/{database}/restart",
			schema: schemaOf([]string{"project", "database"}, map[string]mcp.Property{
				"project":  projectArg(),
				"database": databaseArg(),
			}),
			hints: replaces(),
		},
		{
			name:   "get_database_logs",
			title:  "Get a database's logs",
			desc:   "Recent output from a database server — where a crash-looping database or a refused login explains itself.",
			method: http.MethodGet,
			path:   "/api/projects/{project}/databases/{database}/logs",
			query:  []string{"tail"},
			schema: schemaOf([]string{"project", "database"}, map[string]mcp.Property{
				"project":  projectArg(),
				"database": databaseArg(),
				"tail":     mcpInt("How many lines from the end. Defaults to 200."),
			}),
			hints: readsOnly(),
		},
		{
			name:   "explore_database",
			title:  "Look inside a database",
			desc:   "Without object: what the database holds — tables and views (postgres, mysql, sqlite, with row counts and sizes), collections (mongo), or keys with their type and TTL (redis; filter with a glob in match, at most 1000 keys). With object: a page of rows from that table, documents from that collection (narrow with a JSON filter), or the contents of that redis key. Read-only; long values are cut.",
			method: http.MethodGet,
			path:   "/api/projects/{project}/databases/{database}/explore",
			query:  []string{"object", "offset", "limit", "filter", "match"},
			schema: schemaOf([]string{"project", "database"}, map[string]mcp.Property{
				"project":  projectArg(),
				"database": databaseArg(),
				"object":   mcpString("A table, view, collection or redis key, exactly as the overview names it. Omit for the overview."),
				"offset":   mcpInt("Rows to skip. Defaults to 0."),
				"limit":    mcpInt("Rows to return, at most 500. Defaults to 50."),
				"filter":   mcpString("Mongo only: a JSON query document, e.g. {\"status\": \"active\"}."),
				"match":    mcpString("Redis only, for the overview: a key pattern such as session:*."),
			}),
			hints: readsOnly(),
		},
		{
			name:   "query_database",
			title:  "Run a read-only SQL query",
			desc:   "Run one SQL statement against a postgres, mysql or sqlite database and get up to limit rows back. It runs in a read-only transaction (sqlite: query_only) with a 15 second timeout, so it cannot change data; write it as a SELECT (or WITH … SELECT). One statement only — no semicolons.",
			method: http.MethodPost,
			path:   "/api/projects/{project}/databases/{database}/query",
			schema: schemaOf([]string{"project", "database", "sql"}, map[string]mcp.Property{
				"project":  projectArg(),
				"database": databaseArg(),
				"sql":      mcpString("The query, e.g. SELECT id, email FROM users ORDER BY id DESC."),
				"limit":    mcpInt("Rows to return, at most 500. Defaults to 50."),
			}),
			hints: readsOnly(),
		},
		{
			name:   "backup_database",
			title:  "Back up a database now",
			desc:   "Take a backup now and write it to a backup target, waiting until it is done. Postgres is dumped with pg_dump (custom format), MySQL with mysqldump, Mongo with mongodump, Redis as an RDB snapshot and SQLite through its online backup API. Returns the backup's file name and size.",
			method: http.MethodPost,
			path:   "/api/projects/{project}/databases/{database}/backups",
			schema: schemaOf([]string{"project", "database"}, map[string]mcp.Property{
				"project":  projectArg(),
				"database": databaseArg(),
				"target":   mcpString("The backup target to write to. Defaults to the database's scheduled target, or the only one registered."),
			}),
			hints: creates(),
		},
		{
			name:   "list_backups",
			title:  "List a database's backups",
			desc:   "The backup files present on a target for one database, newest first, and the recent history of backups and restores with any errors.",
			method: http.MethodGet,
			path:   "/api/projects/{project}/databases/{database}/backups",
			query:  []string{"target"},
			schema: schemaOf([]string{"project", "database"}, map[string]mcp.Property{
				"project":  projectArg(),
				"database": databaseArg(),
				"target":   mcpString("The backup target to look on. Defaults as for backup_database."),
			}),
			hints: mcp.Annotations{ReadOnlyHint: true, DestructiveHint: boolPtr(false), IdempotentHint: true, OpenWorldHint: boolPtr(true)},
		},
		{
			name:   "restore_database",
			title:  "Restore a database from a backup",
			desc:   "Replace a database's current contents with a backup. Everything written since that backup is lost, so consider backup_database first. Redis is briefly stopped while its files are swapped; after restoring an SQLite database, redeploy so the app reopens the file.",
			method: http.MethodPost,
			path:   "/api/projects/{project}/databases/{database}/restore",
			schema: schemaOf([]string{"project", "database", "file"}, map[string]mcp.Property{
				"project":  projectArg(),
				"database": databaseArg(),
				"file":     mcpString("The backup file name, exactly as list_backups reports it."),
				"target":   mcpString("The backup target holding it. Defaults as for backup_database."),
			}),
			hints: removes(),
		},

		// --- GitHub ---------------------------------------------------

		{
			name:   "github_status",
			title:  "GitHub connection status",
			desc:   "Whether GitHub is connected, as a token or as an App, which account it authenticates as, and the webhook URL publix expects deliveries on.",
			method: http.MethodGet,
			path:   "/api/github",
			hints:  reachesGitHub(),
		},
		{
			name:   "connect_github",
			title:  "Connect GitHub",
			desc:   "Store GitHub credentials, verifying them before saving. Supply either a personal access token, or an App's id, installation id and private key. Fields left empty keep what is already stored, so this can be used to change one thing.",
			method: http.MethodPut,
			path:   "/api/github",
			schema: schemaOf(nil, map[string]mcp.Property{
				"token":          mcpString("A personal access token. Classic tokens need the repo scope; fine-grained ones need Contents, Metadata, Webhooks and Commit statuses."),
				"appId":          mcpString("A GitHub App's id. The right choice for an organisation."),
				"installationId": mcpString("The App's installation id on the account or organisation."),
				"privateKey":     mcpString("The App's private key, in PEM form."),
				"apiBase":        mcpString("API base URL, for GitHub Enterprise. Defaults to github.com."),
				"webhookSecret":  mcpString("Match a secret the App already uses. One is generated if this is omitted."),
			}),
			hints: replaces(),
		},
		{
			name:   "disconnect_github",
			title:  "Disconnect GitHub",
			desc:   "Forget the stored GitHub credentials. Projects remain, but private clones, push-triggered deploys and commit statuses stop working. The webhook secret is kept so existing hooks are not silently broken.",
			method: http.MethodDelete,
			path:   "/api/github",
			hints:  removes(),
		},
		{
			name:   "list_repos",
			title:  "List GitHub repositories",
			desc:   "Repositories publix can see with its stored credentials, each marked with whether it has already been imported. Narrow with q on a large account rather than paging through everything.",
			method: http.MethodGet,
			path:   "/api/github/repos",
			query:  []string{"account", "q"},
			schema: schemaOf(nil, map[string]mcp.Property{
				"account": mcpString("Limit to one account or organisation. Omit for the default one."),
				"q":       mcpString("Substring match against the full name, e.g. \"api\"."),
			}),
			hints: reachesGitHub(),
		},
		{
			name:   "inspect_repo",
			title:  "Inspect a repository",
			desc:   "What publix can work out about a repository without cloning it: the framework it detects, whether the repository already has a deployment.yaml, the one publix would generate for it, its branches, and anything it is unsure about. Do this before import_repo — it is how you see what the import will actually do.",
			method: http.MethodGet,
			path:   "/api/github/repos/{owner}/{repo}/inspect",
			query:  []string{"ref"},
			schema: schemaOf([]string{"owner", "repo"}, map[string]mcp.Property{
				"owner": mcpString("The repository's owner, e.g. an organisation name."),
				"repo":  mcpString("The repository's name, without the owner."),
				"ref":   mcpString("Branch, tag or commit to inspect. Defaults to the repository's default branch."),
			}),
			hints: reachesGitHub(),
		},
		{
			name:   "import_repo",
			title:  "Import a repository",
			desc:   "The one-step path from a repository to a running deployment: create the project, register the push webhook, and start the first deploy. Run inspect_repo first and pass any environment variables the build needs here, so the first attempt is not the one that fails for want of them.",
			method: http.MethodPost,
			path:   "/api/github/import",
			schema: schemaOf([]string{"owner", "repo"}, map[string]mcp.Property{
				"owner":      mcpString("The repository's owner."),
				"repo":       mcpString("The repository's name, without the owner."),
				"branch":     mcpString("The branch to deploy. Defaults to the repository's default branch."),
				"name":       mcpString("A human name for the project. Defaults to the repository name."),
				"rootDir":    mcpString("Subdirectory to build from, for a monorepo."),
				"slug":       mcpString("The subdomain the project answers on under its apps domain. Chosen now because renaming it later changes a URL that may already be shared."),
				"domains":    mcpStringList("Custom hostnames, in addition to the generated URL."),
				"env":        envArg(),
				"appsDomain": mcpString("Which registered apps domain the generated hostname sits under. \"none\" opts out of one."),
				"autoDeploy": mcpBool("Deploy on every push to the branch. Defaults to on."),
				"writeSpec":  mcpBool("Commit the deployment.yaml below to the repository. Off by default: this writes to someone's repository."),
				"spec":       mcpString("The deployment.yaml to commit, normally the suggestion from inspect_repo. Only used with writeSpec."),
				"deploy":     mcpBool("Start a deployment straight after importing. Defaults to on."),
			}),
			hints: creates(),
		},

		// --- Server settings ------------------------------------------

		{
			name:   "get_settings",
			title:  "Get server settings",
			desc:   "The whole server configuration: Docker network, Traefik paths and entry points, the registered apps domains, proxy and redirect rules, shared volumes, and the build and retention limits.",
			method: http.MethodGet,
			path:   "/api/settings",
			hints:  readsOnly(),
		},
		{
			name:   "update_settings",
			title:  "Update server settings",
			desc:   "Change server-wide settings. Only the fields you send are touched. These affect every project, and getting the Traefik directory or the public URL wrong will break routing or deploy-on-push, so read get_settings first.",
			method: http.MethodPut,
			path:   "/api/settings",
			schema: schemaOf(nil, map[string]mcp.Property{
				"network":           mcpString("The Docker network projects and Traefik share."),
				"traefikDynamicDir": mcpString("Absolute path to Traefik's dynamic configuration directory — the one it watches. publix writes publix.yml into it."),
				"entryPoints":       mcpStringList("Traefik entry point names to publish routers on, e.g. [\"websecure\"]."),
				"certResolver":      mcpString("Traefik certificate resolver for TLS, e.g. \"letsencrypt\"."),
				"publicUrl":         mcpString("The dashboard's own public URL. GitHub deploy-on-push needs this, because it is how the webhook URL is built."),
				"workDir":           mcpString("Absolute path where publix checks out and builds repositories."),
				"keepImages":        mcpInt("How many images to keep per project. Keeping two is what makes a rollback instant."),
				"keepDeployments":   mcpInt("How many deployment records to keep in the history."),
				"buildConcurrency":  mcpInt("How many builds may run at once."),
			}),
			hints: replaces(),
		},
		{
			name:   "get_system",
			title:  "Check system health",
			desc:   "Whether the things publix depends on are actually working: the Docker daemon, the Traefik directory and whether publix can write to it, the shared network, and the version and project counts. The first thing to check when something is wrong and it is not clear where.",
			method: http.MethodGet,
			path:   "/api/system",
			hints:  readsOnly(),
		},

		// --- Apps domains ---------------------------------------------

		{
			name:   "add_apps_domain",
			title:  "Register an apps domain",
			desc:   "Register a parent domain that projects get their generated hostnames under, as <slug>.<domain>. A wildcard DNS record for *.<domain> must point at this server for those URLs to resolve.",
			method: http.MethodPost,
			path:   "/api/apps-domains",
			schema: schemaOf([]string{"domain"}, map[string]mcp.Property{
				"domain":      mcpString("The parent domain, e.g. apps.example.com."),
				"description": mcpString("A note about what this domain is for."),
				"default":     mcpBool("Make this the domain projects use when they do not name one."),
			}),
			hints: creates(),
		},
		{
			name:   "set_default_apps_domain",
			title:  "Set the default apps domain",
			desc:   "Choose which registered apps domain projects use when they have not named one. Every project relying on the default moves to the new parent, and their URLs change with it.",
			method: http.MethodPost,
			path:   "/api/apps-domains/{domain}/default",
			schema: schemaOf([]string{"domain"}, map[string]mcp.Property{
				"domain": mcpString("A registered apps domain, as get_settings lists them."),
			}),
			hints: replaces(),
		},
		{
			name:   "delete_apps_domain",
			title:  "Unregister an apps domain",
			desc:   "Remove a parent domain. Projects that named it fall back to the default rather than becoming unreachable, and the reply says which ones moved.",
			method: http.MethodDelete,
			path:   "/api/apps-domains/{domain}",
			schema: schemaOf([]string{"domain"}, map[string]mcp.Property{
				"domain": mcpString("The registered apps domain to remove."),
			}),
			hints: removes(),
		},

		// --- Proxies and redirects ------------------------------------

		{
			name:   "add_proxy",
			title:  "Add a proxy rule",
			desc:   "Serve a hostname from a backend publix does not deploy — something already running on the host or elsewhere on the network. Refused for a hostname a project already serves, because such a rule would never take effect.",
			method: http.MethodPost,
			path:   "/api/proxies",
			schema: schemaOf([]string{"domain", "target"}, map[string]mcp.Property{
				"domain":             mcpString("The hostname to serve, e.g. legacy.example.com."),
				"path":               mcpString("Limit the rule to a path prefix. Omit to match the whole host."),
				"target":             mcpString("Where to send the traffic, e.g. http://127.0.0.1:8080."),
				"stripPath":          mcpBool("Remove the path prefix before forwarding."),
				"passHostHeader":     mcpBool("Forward the original Host header. On by default."),
				"insecureSkipVerify": mcpBool("Do not verify the backend's TLS certificate. For a self-signed backend."),
				"description":        mcpString("A note about what this rule is for."),
			}),
			hints: creates(),
		},
		{
			name:   "update_proxy",
			title:  "Update a proxy rule",
			desc:   "Replace an existing proxy rule, found by the hostname it currently serves. The rule is replaced wholesale, so send every field you want it to keep.",
			method: http.MethodPut,
			path:   "/api/proxies/{domain}",
			alias:  map[string]string{"newDomain": "domain"},
			schema: schemaOf([]string{"domain", "target"}, map[string]mcp.Property{
				"domain":             mcpString("The hostname the rule currently serves — which rule to change."),
				"newDomain":          mcpString("A different hostname to serve instead. Omit to leave it where it is."),
				"path":               mcpString("Limit the rule to a path prefix."),
				"target":             mcpString("Where to send the traffic."),
				"stripPath":          mcpBool("Remove the path prefix before forwarding."),
				"passHostHeader":     mcpBool("Forward the original Host header. On by default."),
				"insecureSkipVerify": mcpBool("Do not verify the backend's TLS certificate."),
				"description":        mcpString("A note about what this rule is for."),
			}),
			hints: replaces(),
		},
		{
			name:   "delete_proxy",
			title:  "Delete a proxy rule",
			desc:   "Remove a proxy rule. The hostname stops being served as soon as Traefik reloads.",
			method: http.MethodDelete,
			path:   "/api/proxies/{domain}",
			schema: schemaOf([]string{"domain"}, map[string]mcp.Property{
				"domain": mcpString("The hostname the rule serves."),
			}),
			hints: removes(),
		},
		{
			name:   "add_redirect",
			title:  "Add a redirect rule",
			desc:   "Forward a hostname somewhere else with an HTTP redirect, for an old domain or a bare apex pointing at www. Refused for a hostname a project already serves.",
			method: http.MethodPost,
			path:   "/api/redirects",
			schema: schemaOf([]string{"domain", "target"}, map[string]mcp.Property{
				"domain":      mcpString("The hostname to forward from, e.g. old.example.com."),
				"path":        mcpString("Limit the rule to a path prefix. Omit to match the whole host."),
				"target":      mcpString("Where to send visitors, e.g. https://example.com."),
				"keepPath":    mcpBool("Append the requested path to the target, so /a/b lands on the same path there. On by default."),
				"permanent":   mcpBool("Send 301 rather than 302. Browsers cache a 301 hard, so use it only once the destination is settled."),
				"description": mcpString("A note about what this rule is for."),
			}),
			hints: creates(),
		},
		{
			name:   "update_redirect",
			title:  "Update a redirect rule",
			desc:   "Replace an existing redirect rule, found by the hostname it currently forwards. The rule is replaced wholesale, so send every field you want it to keep.",
			method: http.MethodPut,
			path:   "/api/redirects/{domain}",
			alias:  map[string]string{"newDomain": "domain"},
			schema: schemaOf([]string{"domain", "target"}, map[string]mcp.Property{
				"domain":      mcpString("The hostname the rule currently forwards — which rule to change."),
				"newDomain":   mcpString("A different hostname to forward instead. Omit to leave it where it is."),
				"path":        mcpString("Limit the rule to a path prefix."),
				"target":      mcpString("Where to send visitors."),
				"keepPath":    mcpBool("Append the requested path to the target. On by default."),
				"permanent":   mcpBool("Send 301 rather than 302."),
				"description": mcpString("A note about what this rule is for."),
			}),
			hints: replaces(),
		},
		{
			name:   "delete_redirect",
			title:  "Delete a redirect rule",
			desc:   "Remove a redirect rule. The hostname stops being forwarded as soon as Traefik reloads.",
			method: http.MethodDelete,
			path:   "/api/redirects/{domain}",
			schema: schemaOf([]string{"domain"}, map[string]mcp.Property{
				"domain": mcpString("The hostname the rule forwards."),
			}),
			hints: removes(),
		},

		// --- Volumes --------------------------------------------------

		{
			name:   "add_volume",
			title:  "Register a volume",
			desc:   "Register a host directory that projects can mount by name, without ever seeing its path. A project-scoped volume gives each project its own subdirectory, so two projects mounting the same one cannot read each other's files; a shared one gives every project the same directory, for a dataset they genuinely share. Project scope is the safe default.",
			method: http.MethodPost,
			path:   "/api/volumes",
			schema: schemaOf([]string{"name", "path"}, map[string]mcp.Property{
				"name":         mcpString("The name projects ask for in their deployment.yaml."),
				"path":         mcpString("Absolute path to the host directory."),
				"scope":        mcpEnum("project gives each project its own subdirectory; shared gives every project the same directory. Defaults to project.", string(store.ScopeProject), string(store.ScopeShared)),
				"description":  mcpString("A note about what this directory holds."),
				"readOnly":     mcpBool("Mount it read-only in every project."),
				"defaultMount": mcpString("Where to mount it inside the container when a project does not say."),
				"create":       mcpBool("Create the host directory if it does not exist."),
			}),
			hints: creates(),
		},
		{
			name:   "delete_volume",
			title:  "Unregister a volume",
			desc:   "Stop offering a host directory to projects. Refused while a project still mounts it. The directory and everything in it is left on the host untouched.",
			method: http.MethodDelete,
			path:   "/api/volumes/{name}",
			schema: schemaOf([]string{"name"}, map[string]mcp.Property{
				"name": mcpString("The registered volume's name."),
			}),
			hints: removes(),
		},

		// --- Backup targets -------------------------------------------

		{
			name:   "list_backup_targets",
			title:  "List backup targets",
			desc:   "The places database backups can be written to, and which databases are scheduled to use each. rclone credentials are never returned.",
			method: http.MethodGet,
			path:   "/api/backup-targets",
			hints:  readsOnly(),
		},
		{
			name:   "add_backup_target",
			title:  "Register a backup target",
			desc:   "Register somewhere database backups can go. A local target is a host directory — a second disk, an NFS or USB mount. An rclone target is anywhere rclone reaches: Google Drive, S3, Backblaze B2, Dropbox, OneDrive, SFTP and more; it needs the remote's section of an rclone.conf (for Google Drive, run `rclone config` on any machine with a browser, then `rclone config show <remote>`). publix writes and deletes a test file before saving, so a wrong path or expired credential is reported now.",
			method: http.MethodPost,
			path:   "/api/backup-targets",
			schema: schemaOf([]string{"name", "type"}, map[string]mcp.Property{
				"name":         mcpString("The name databases refer to it by, e.g. nas or gdrive."),
				"type":         mcpEnum("local writes to a host directory; rclone uploads with rclone.", string(store.TargetLocal), string(store.TargetRclone)),
				"path":         mcpString("For local: the absolute host directory."),
				"remote":       mcpString("For rclone: the destination, e.g. gdrive:publix-backups."),
				"rcloneConfig": mcpString("For rclone: the config section defining the remote, starting with [name]."),
				"description":  mcpString("A note about the target."),
				"skipTest":     mcpBool("Save without the test write."),
			}),
			hints: mcp.Annotations{DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
		},
		{
			name:   "test_backup_target",
			title:  "Test a backup target",
			desc:   "Write and delete a test file on a registered target, to check its path or credentials still work.",
			method: http.MethodPost,
			path:   "/api/backup-targets/{name}/test",
			schema: schemaOf([]string{"name"}, map[string]mcp.Property{"name": mcpString("The target's name.")}),
			hints:  mcp.Annotations{DestructiveHint: boolPtr(false), IdempotentHint: true, OpenWorldHint: boolPtr(true)},
		},
		{
			name:   "delete_backup_target",
			title:  "Unregister a backup target",
			desc:   "Stop offering a backup target. Refused while a database's schedule uses it. Backups already written there are left untouched.",
			method: http.MethodDelete,
			path:   "/api/backup-targets/{name}",
			schema: schemaOf([]string{"name"}, map[string]mcp.Property{"name": mcpString("The target's name.")}),
			hints:  removes(),
		},

		// --- Account --------------------------------------------------

		{
			name:   "change_password",
			title:  "Change the dashboard password",
			desc:   "Change the dashboard password. This signs out every session everywhere, which is the point of changing it — including the token these tools are using, so expect the next call to fail until a new one is issued with `publix token`.",
			method: http.MethodPost,
			path:   "/api/auth/password",
			schema: schemaOf([]string{"current", "new"}, map[string]mcp.Property{
				"current": mcpString("The current password."),
				"new":     mcpString("The new password. At least 8 characters."),
			}),
			hints: removes(),
		},
	}
}
