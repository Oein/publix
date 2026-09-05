# Installing publix on a bare Ubuntu server

Nothing needs to be installed first. The installer brings up Docker, builds
publix, and starts it behind Traefik.

```bash
curl -fsSL https://raw.githubusercontent.com/Oein/publix/main/scripts/install.sh \
  | sudo bash -s -- \
      --email you@example.com \
      --apps-domain apps.example.com \
      --dashboard publix.example.com
```

That is the whole installation. The rest of this page explains what it does,
what to point at the server, and how to run it a different way.

---

## Before you start

**A server.** Ubuntu 22.04 or 24.04, or Debian 12. Two cores and 2 GB of RAM
is enough for a handful of small projects; builds are the memory-hungry part,
so give it 4 GB if you plan to build Next.js apps.

**Two DNS records**, both pointing at the server's public address:

| Record | Purpose |
| --- | --- |
| `publix.example.com` → `A` → your IP | the dashboard |
| `*.apps.example.com` → `A` → your IP | a URL for every project, automatically |

The wildcard is what lets a project work the moment you import it, before
you have configured a domain for it. Skip it if you would rather assign
every project its own hostname by hand.

**Ports 80 and 443 open.** Traefik needs both: 443 to serve, and 80 for
Let's Encrypt's HTTP challenge and the redirect to HTTPS.

```bash
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw allow OpenSSH
sudo ufw enable
```

Do **not** open 4321. The dashboard binds to loopback and is published
through Traefik; publix drives the Docker socket, which is equivalent to
root on the host.

---

## What the installer does

Every step checks before it acts, so re-running it upgrades rather than
duplicates.

1. Installs `ca-certificates`, `curl` and `git`.
2. Installs Docker from Docker's own apt repository, unless it is already
   there. Pass `--skip-docker` if you install it another way.
3. Clones the repository to `/opt/publix`, or fetches the latest `main` into
   it if it is already there. Run from inside a checkout instead, and it
   installs that checkout without fetching — see [Upgrading](#upgrading).
4. Creates `/var/lib/publix` (mode 0700 — it holds your GitHub token and
   every project's secrets) and `/etc/traefik/dynamic`.
5. Creates the `publix` Docker network that Traefik and every project share.
6. Writes `deploy/.env` with your ACME email.
7. Writes a Traefik router for the dashboard, if you passed `--dashboard`.
8. Builds the publix image and starts Traefik and publix.
9. Waits for the dashboard to answer, and shows you its logs if it does not.

### Options

```
--email ADDRESS       where Let's Encrypt sends expiry warnings (required)
--apps-domain DOMAIN  wildcard domain giving every project a URL
--dashboard DOMAIN    serve the dashboard on this hostname
--src DIR             where to keep the checkout (default /opt/publix)
--ref REF             git ref to install (default main)
--skip-docker         do not touch Docker; assume it already works
```

---

## First run

Open the dashboard and choose an admin password. Then, under
**Settings → Server**, set two things:

- **Apps domain** — `apps.example.com`. Every project gets
  `<project>.apps.example.com` for free.
- **Public URL** — `https://publix.example.com`. GitHub webhooks are sent
  here, so deploy-on-push does not work without it.

Then **Settings → GitHub** and connect either a personal access token
(fastest) or a GitHub App (right for an organisation). Your repositories
appear under **Import**.

### Connecting a GitHub App

publix has no OAuth login, so **the Callback URL is not used** — leave
“Request user authorization (OAuth) during installation” unchecked. The
only address GitHub needs is the webhook.

| Field | Value |
| --- | --- |
| Homepage URL | `https://publix.example.com` — required by GitHub, unused by publix |
| Callback URL | leave empty |
| Webhook URL | `https://publix.example.com/api/webhooks/github` |
| Webhook secret | the one shown under Settings → GitHub |
| Subscribe to events | **Push**, and nothing else |

Repository permissions:

| Permission | Level | Why |
| --- | --- | --- |
| Contents | Read-only | to clone. Read **and write** only if you want publix to commit a `deployment.yaml` for you when importing |
| Metadata | Read-only | mandatory; GitHub selects it for you |
| Commit statuses | Read and write | to report the deploy result next to the commit |
| Webhooks | Read and write | only if the App has no webhook of its own |

Then **Install** the App on the account or organisation whose repositories
you want to deploy, and paste the App ID and the private key into
Settings → GitHub. publix finds the installation itself when there is only
one; with several, paste the installation ID too — the error names them.

> **Creating the App is not enough — installing it is a separate step, and
> so is choosing what it can see.** An App with no repositories granted
> connects successfully and shows an empty Import screen. If you picked
> “Only select repositories”, only those appear; and if your repositories
> belong to an organisation, the App has to be installed on *that
> organisation*, not on your personal account.
>
> Settings → GitHub names the account the App is installed on and whether
> it was given all or only selected repositories, and links straight to the
> installation's settings on GitHub where that is changed.

Both the webhook URL and its secret are shown on that page, with a copy
button, precisely because an App's webhook is configured on the App rather
than on each repository.

> **One webhook, not two.** If the App has its own webhook pointing here,
> publix will *not* also add one to each repository — GitHub would then
> deliver every push twice. The settings page says which of the two is in
> effect. Leaving the App's webhook empty is equally fine; publix then
> creates a repository hook as you import, which is what a personal access
> token always does.

If you did not pass `--dashboard`, reach it over an SSH tunnel instead:

```bash
ssh -L 4321:127.0.0.1:4321 you@your-server
# then open http://127.0.0.1:4321
```

---

## Volumes: the one rule

publix runs in a container but creates containers on the **host**. It hands
paths to the Docker daemon, and the daemon resolves them on the host — so a
path that means one thing inside publix's container and another outside
produces app containers bind-mounting the wrong directory.

**Every host path publix touches must be mounted at the same path inside its
container.** The compose file already does this for `/var/lib/publix` and
`/etc/traefik/dynamic`. When you register a shared volume, add it too.

To offer projects `/mnt/data` as a volume named `disk0`:

```bash
sudo mkdir -p /mnt/data
```

Then in `/opt/publix/deploy/docker-compose.yml`, under the `publix`
service's volumes:

```yaml
      - /mnt/data:/mnt/data     # identical on both sides
```

```bash
cd /opt/publix
docker compose -f deploy/docker-compose.yml up -d
```

Now register it in **Settings → Volumes**. There are two kinds, and the
choice decides what a project actually gets:

| Kind | A project mounting `disk0` gets | For |
| --- | --- | --- |
| **Project volume** | `/mnt/data/<project-id>` | its own uploads, cache, database |
| **Shared volume** | `/mnt/data` itself | a dataset or media library several projects share |

A project volume is isolated: two projects can both mount it and neither can
read the other's files. A shared volume is deliberately not — every project
mounting it reads and writes the same files, and any of them can overwrite
what another wrote. Mark it read-only if the projects using it only need to
read.

Skip the compose mount above and the volume will appear to register fine,
then every project using it will bind an empty directory. Mount it.

---

## Running it another way

### From source, without containers

Needs Go 1.24 and Node 20+ on the server.

```bash
git clone https://github.com/Oein/publix && cd publix
make build && sudo make install
```

Then run it under systemd. `/etc/systemd/system/publix.service`:

```ini
[Unit]
Description=publix
After=docker.service
Requires=docker.service

[Service]
Type=simple
ExecStart=/usr/local/bin/publix serve --addr 127.0.0.1:4321
Restart=always
RestartSec=5
Environment=PUBLIX_HOME=/var/lib/publix
ReadWritePaths=/var/lib/publix /etc/traefik/dynamic
NoNewPrivileges=true
ProtectSystem=full
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable --now publix
```

Running on the host rather than in a container removes the path rule
entirely, since there is only one filesystem view. You still need Traefik,
the `publix` network, and both directories — steps 4 and 5 above.

### With a Traefik you already run

Pass `--skip-docker` and bring up only publix:

```bash
cd /opt/publix
docker compose -f deploy/docker-compose.yml up -d publix
```

publix needs your Traefik to have the **Docker provider** enabled (so it can
discover a deployment's containers) and a **watched file provider** whose
directory publix can write to. It owns exactly one file there,
`publix.yml`, and leaves everything else alone.

---

## Day to day

```bash
cd /opt/publix

docker compose -f deploy/docker-compose.yml logs -f publix     # follow logs
docker compose -f deploy/docker-compose.yml restart publix     # restart
docker compose -f deploy/docker-compose.yml ps                 # status
```

Restarting publix does not touch running projects. On startup it rewrites
Traefik's routing from its own state, so anything that drifted while it was
down is corrected.

### Upgrading

Re-run the one-liner. Piped from curl it fetches the latest `main` into
`/opt/publix`, rebuilds the image and restarts:

```bash
curl -fsSL https://raw.githubusercontent.com/Oein/publix/main/scripts/install.sh \
  | sudo bash -s -- --email you@example.com
```

Or do it by hand, which is the same three steps:

```bash
cd /opt/publix
sudo git pull
sudo docker compose -f deploy/docker-compose.yml build --pull
sudo docker compose -f deploy/docker-compose.yml up -d
```

Running `/opt/publix/scripts/install.sh` directly does **not** fetch. A
script run from inside a checkout installs *that* checkout, so that testing
a local change does not silently replace it with `main`. Use it when you
have already pulled, or to try a branch:

```bash
cd /opt/publix
sudo git fetch origin && sudo git checkout -B some-branch origin/some-branch
sudo ./scripts/install.sh --email you@example.com
```

Your projects, settings and secrets live in `/var/lib/publix` and are
untouched by an upgrade. Running containers keep serving while the new image
builds; only publix itself restarts, and on startup it rewrites Traefik's
routing from its own state. The dashboard's HTML is served `no-cache` with
fingerprinted assets, so a normal page load picks up the new build — no hard
refresh needed.

### Backing up

One file matters: `/var/lib/publix/publix.json`. It holds your projects,
settings, secrets and GitHub credentials, and nothing else is irreplaceable
— checkouts re-clone, images rebuild, and the routing file is regenerated by
`publix reconcile`.

```bash
sudo install -m 600 /var/lib/publix/publix.json /root/publix-backup.json
```

| Path | What |
| --- | --- |
| `/var/lib/publix/publix.json` | projects, settings, secrets (mode 0600) |
| `/var/lib/publix/work/` | repository checkouts, reused between deploys |
| `/var/lib/publix/logs/` | build logs, one file per deployment |
| `/etc/traefik/dynamic/publix.yml` | generated routing — do not edit |

---

## When something is wrong

**The installer stops on Docker's signing key.** The server cannot reach
`download.docker.com`. Install Docker another way and re-run with
`--skip-docker`.

**The image does not build.** The build needs npmjs.org, proxy.golang.org
and Docker Hub. Behind a proxy, configure it for the Docker *daemon* —
[docker.com/engine/daemon/proxy](https://docs.docker.com/engine/daemon/proxy/) —
not just your shell.

**The dashboard does not answer.**

```bash
docker compose -f deploy/docker-compose.yml logs --tail 50 publix
```

**A certificate is not issued.** Traefik needs port 80 reachable from the
internet for the HTTP challenge, and the DNS record must already resolve to
this server. Check with `docker compose -f deploy/docker-compose.yml logs traefik`.

**A project deploys but its domain 404s.** Check that publix wrote the
routing and that Traefik reads the same file:

```bash
sudo cat /etc/traefik/dynamic/publix.yml
```

Both containers mount `/etc/traefik/dynamic`; if you changed that path,
change it in both places.

**Settings → Server** shows whether Docker is reachable, whether publix can
write Traefik's directory, and whether the shared network exists. That page
is the first thing to look at.
