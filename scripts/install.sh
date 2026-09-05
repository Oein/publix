#!/usr/bin/env bash
#
# publix installer for a fresh Ubuntu or Debian server.
#
# Installs Docker if it is missing, prepares the directories publix and
# Traefik share, and brings both up. Safe to run more than once: every step
# checks before it acts, so re-running upgrades rather than duplicates.
#
#   curl -fsSL https://raw.githubusercontent.com/Oein/publix/main/scripts/install.sh | sudo bash -s -- \
#     --email you@example.com --apps-domain apps.example.com --dashboard publix.example.com
#
# Or, from a checkout:
#
#   sudo ./scripts/install.sh --email you@example.com

set -euo pipefail

REPO_URL="${PUBLIX_REPO:-https://github.com/Oein/publix}"
REPO_REF="${PUBLIX_REF:-main}"
INSTALL_DIR="${PUBLIX_SRC:-/opt/publix}"
STATE_DIR="/var/lib/publix"
TRAEFIK_DIR="/etc/traefik/dynamic"

ACME_EMAIL=""
APPS_DOMAIN=""
DASHBOARD_DOMAIN=""
SKIP_DOCKER=0

# --- output ------------------------------------------------------------------

if [ -t 1 ]; then
  BOLD=$'\033[1m'; GREEN=$'\033[32m'; YELLOW=$'\033[33m'; RED=$'\033[31m'; DIM=$'\033[2m'; RESET=$'\033[0m'
else
  BOLD=""; GREEN=""; YELLOW=""; RED=""; DIM=""; RESET=""
fi

step() { printf '%s==>%s %s\n' "$GREEN" "$RESET" "$*"; }
info() { printf '    %s%s%s\n' "$DIM" "$*" "$RESET"; }
warn() { printf '%s !%s %s\n' "$YELLOW" "$RESET" "$*" >&2; }
die()  { printf '%s ✗%s %s\n' "$RED" "$RESET" "$*" >&2; exit 1; }

usage() {
  cat <<'USAGE'
publix installer

Usage: install.sh --email <address> [options]

Required:
  --email ADDRESS         where Let's Encrypt sends certificate warnings

Options:
  --apps-domain DOMAIN    wildcard domain giving every project a URL,
                          e.g. apps.example.com (point *.apps.example.com here)
  --dashboard DOMAIN      serve the publix dashboard on this hostname
  --src DIR               where to keep the checkout (default /opt/publix)
  --ref REF               git ref to install (default main)
  --skip-docker           do not touch Docker; assume it is already working
  -h, --help              show this message
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --email)         ACME_EMAIL="${2:?--email needs a value}"; shift 2 ;;
    --apps-domain)   APPS_DOMAIN="${2:?--apps-domain needs a value}"; shift 2 ;;
    --dashboard)     DASHBOARD_DOMAIN="${2:?--dashboard needs a value}"; shift 2 ;;
    --src)           INSTALL_DIR="${2:?--src needs a value}"; shift 2 ;;
    --ref)           REPO_REF="${2:?--ref needs a value}"; shift 2 ;;
    --skip-docker)   SKIP_DOCKER=1; shift ;;
    -h|--help)       usage; exit 0 ;;
    *)               usage >&2; die "unknown option: $1" ;;
  esac
done

# --- preflight ---------------------------------------------------------------

[ "$(id -u)" -eq 0 ] || die "run this with sudo: it installs packages and writes to /etc and /var/lib"
[ -n "$ACME_EMAIL" ] || { usage >&2; die "--email is required"; }

case "$ACME_EMAIL" in
  *@*.*) ;;
  *) die "--email does not look like an address: $ACME_EMAIL" ;;
esac

if [ ! -r /etc/os-release ]; then
  die "cannot identify this system (no /etc/os-release). This script targets Ubuntu and Debian."
fi
# shellcheck disable=SC1091
. /etc/os-release
case "${ID:-}${ID_LIKE:-}" in
  *ubuntu*|*debian*) ;;
  *) warn "this script targets Ubuntu and Debian; ${PRETTY_NAME:-this system} may need manual steps" ;;
esac

if [ "$(uname -m)" != "x86_64" ] && [ "$(uname -m)" != "aarch64" ]; then
  warn "untested architecture: $(uname -m)"
fi

printf '\n%spublix%s — installing on %s\n\n' "$BOLD" "$RESET" "${PRETTY_NAME:-this server}"

# --- packages ----------------------------------------------------------------

step "Installing prerequisites"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq --no-install-recommends ca-certificates curl git gnupg >/dev/null
info "ca-certificates, curl, git"

if [ "$SKIP_DOCKER" -eq 0 ]; then
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    info "Docker $(docker version --format '{{.Server.Version}}' 2>/dev/null || echo '(not running)') already installed"
  else
    step "Installing Docker"
    # Docker's own apt repository, which is what their documentation
    # recommends and what keeps `docker compose` current.
    install -m 0755 -d /etc/apt/keyrings
    distro="ubuntu"
    case "${ID:-}" in debian) distro="debian" ;; esac

    # Download to a temporary file first: a partial key left in place would
    # make every later apt-get fail with a signature error instead of the
    # network error that actually happened.
    keytmp="$(mktemp)"
    if ! curl -fsSL --retry 3 --retry-delay 2 "https://download.docker.com/linux/${distro}/gpg" -o "$keytmp"; then
      rm -f "$keytmp"
      cat >&2 <<HINT

${RED} ✗${RESET} Could not download Docker's signing key from download.docker.com.

  This is a network problem, not a publix one. Common causes:
    · an outbound proxy or TLS-inspecting firewall
    · no route to the internet from this server

  If Docker is available another way — your distribution's own package, or
  an internal mirror — install it and re-run this script with:

      $0 --email $ACME_EMAIL --skip-docker

HINT
      exit 1
    fi
    install -m 0644 "$keytmp" /etc/apt/keyrings/docker.asc
    rm -f "$keytmp"
    codename="${VERSION_CODENAME:-${UBUNTU_CODENAME:-}}"
    [ -n "$codename" ] || die "cannot determine the distribution codename; install Docker manually and re-run with --skip-docker"
    printf 'deb [arch=%s signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/%s %s stable\n' \
      "$(dpkg --print-architecture)" "$distro" "$codename" > /etc/apt/sources.list.d/docker.list
    apt-get update -qq
    apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin >/dev/null
    systemctl enable --now docker >/dev/null 2>&1 || true
    info "Docker $(docker version --format '{{.Server.Version}}' 2>/dev/null || echo installed)"
  fi
fi

if ! docker info >/dev/null 2>&1; then
  cat >&2 <<HINT

${RED} ✗${RESET} The Docker daemon is not responding.

  Try:  systemctl start docker
  Then: systemctl status docker

HINT
  exit 1
fi

# --- source ------------------------------------------------------------------

# Where the source comes from, in order of what the user most likely meant.
#
# Running ./scripts/install.sh from a checkout must install THAT checkout —
# reaching out to GitHub instead would ignore whatever they are sitting on,
# and fail outright on a server with no route to it. Only when the script
# was piped from curl (so it has no path of its own) does it fetch.
script_dir=""
case "$0" in
  */*) script_dir="$(cd "$(dirname "$0")" 2>/dev/null && pwd || true)" ;;
esac

if [ -n "$script_dir" ] && [ -f "$script_dir/../Dockerfile" ] && [ -f "$script_dir/../deploy/docker-compose.yml" ]; then
  INSTALL_DIR="$(cd "$script_dir/.." && pwd)"
  step "Using the checkout at $INSTALL_DIR"
elif [ -d "$INSTALL_DIR/.git" ]; then
  step "Updating $INSTALL_DIR"
  git -C "$INSTALL_DIR" remote set-url origin "$REPO_URL"
  git -C "$INSTALL_DIR" fetch --quiet --force origin "$REPO_REF"
  git -C "$INSTALL_DIR" checkout --quiet --force FETCH_HEAD
else
  step "Cloning $REPO_URL into $INSTALL_DIR"
  git clone --quiet --branch "$REPO_REF" "$REPO_URL" "$INSTALL_DIR"
fi

[ -f "$INSTALL_DIR/deploy/docker-compose.yml" ] || die "$INSTALL_DIR does not look like a publix checkout"
info "$(git -C "$INSTALL_DIR" log -1 --pretty='%h %s' 2>/dev/null || echo 'local checkout')"

# --- directories and network -------------------------------------------------

step "Preparing directories"
mkdir -p "$STATE_DIR" "$TRAEFIK_DIR"
# 0700: the state file holds GitHub tokens and every project's secrets.
chmod 700 "$STATE_DIR"
chmod 755 "$TRAEFIK_DIR"
info "$STATE_DIR (state, checkouts, build logs)"
info "$TRAEFIK_DIR (routing publix generates)"

if docker network inspect publix >/dev/null 2>&1; then
  info "docker network 'publix' already exists"
else
  step "Creating the shared docker network"
  docker network create publix >/dev/null
fi

# --- configuration -----------------------------------------------------------

step "Writing deploy/.env"
umask 077
cat > "$INSTALL_DIR/deploy/.env" <<ENV
# Written by scripts/install.sh on $(date -u +%Y-%m-%dT%H:%M:%SZ)
ACME_EMAIL=$ACME_EMAIL
ENV
umask 022
info "ACME_EMAIL=$ACME_EMAIL"

if [ -n "$DASHBOARD_DOMAIN" ]; then
  step "Routing the dashboard at $DASHBOARD_DOMAIN"
  # A file of our own. publix owns only publix.yml in this directory and
  # leaves everything else alone, so this survives every deploy.
  cat > "$TRAEFIK_DIR/publix-dashboard.yml" <<DASH
# Written by scripts/install.sh. Serves the publix dashboard itself.
# publix does not route itself — it has no deployment — so this is a
# hand-written router pointing at the container on the shared network.
http:
  routers:
    publix-dashboard:
      rule: "Host(\`$DASHBOARD_DOMAIN\`)"
      entryPoints: [websecure]
      service: publix-dashboard
      tls:
        certResolver: letsencrypt
  services:
    publix-dashboard:
      loadBalancer:
        servers:
          - url: "http://publix:4321"
DASH
fi

# --- build and start ---------------------------------------------------------

step "Building publix (this takes a few minutes the first time)"
cd "$INSTALL_DIR"
if ! docker compose -f deploy/docker-compose.yml build --pull; then
  cat >&2 <<HINT

${RED} ✗${RESET} The publix image did not build.

  The build needs to reach npmjs.org, proxy.golang.org and the Docker Hub.
  If this server is behind a proxy, configure it for the Docker daemon:
    https://docs.docker.com/engine/daemon/proxy/

HINT
  exit 1
fi

step "Starting Traefik and publix"
docker compose -f deploy/docker-compose.yml up -d

# Wait for the dashboard rather than declaring success and leaving the user
# to discover a crash loop on their own.
step "Waiting for publix to answer"
ready=0
for _ in $(seq 1 60); do
  if curl -fsS --max-time 2 http://127.0.0.1:4321/api/health >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 2
done

if [ "$ready" -ne 1 ]; then
  warn "publix did not answer within two minutes. Its logs:"
  docker compose -f deploy/docker-compose.yml logs --tail 40 publix >&2
  die "installation finished but publix is not healthy"
fi

# --- done --------------------------------------------------------------------

printf '\n%s✓ publix is running%s\n\n' "$GREEN$BOLD" "$RESET"

if [ -n "$DASHBOARD_DOMAIN" ]; then
  printf '  Dashboard   %shttps://%s%s\n' "$BOLD" "$DASHBOARD_DOMAIN" "$RESET"
  printf '              %spoint an A record for %s at this server first%s\n' "$DIM" "$DASHBOARD_DOMAIN" "$RESET"
else
  printf '  Dashboard   %shttp://127.0.0.1:4321%s\n' "$BOLD" "$RESET"
  printf '              %sfrom your laptop: ssh -L 4321:127.0.0.1:4321 %s@this-server%s\n' \
    "$DIM" "${SUDO_USER:-root}" "$RESET"
fi

cat <<NEXT

  Next:
    1. Open the dashboard and choose an admin password.
    2. Settings → Server:
NEXT
if [ -n "$APPS_DOMAIN" ]; then
  printf '         apps domain  %s   (point *.%s at this server)\n' "$APPS_DOMAIN" "$APPS_DOMAIN"
else
  printf '         apps domain  a wildcard domain, so every project gets a URL\n'
fi
if [ -n "$DASHBOARD_DOMAIN" ]; then
  printf '         public URL   https://%s   (GitHub webhooks are sent here)\n' "$DASHBOARD_DOMAIN"
else
  printf '         public URL   where this dashboard is reachable, for GitHub webhooks\n'
fi
cat <<NEXT
    3. Settings → GitHub: connect a token or a GitHub App.
    4. Import a repository.

  Manage it with:
    cd $INSTALL_DIR
    docker compose -f deploy/docker-compose.yml logs -f publix
    docker compose -f deploy/docker-compose.yml restart publix

NEXT
