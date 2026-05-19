#!/usr/bin/env bash
set -euo pipefail

# Guard against `curl ... | bash` which breaks interactive prompts.
# The correct invocation is: bash -c "$(curl -sSL https://install.tindra.sh)"
if [ ! -t 0 ]; then
  exec < /dev/tty || { printf '\nerror: run with:  bash -c "$(curl -sSL https://install.tindra.sh)"\n\n' >&2; exit 1; }
fi

# ── colours ───────────────────────────────────────────────────────────────────
if [ -t 1 ]; then
  BOLD="\033[1m"; DIM="\033[2m"; GREEN="\033[32m"
  CYAN="\033[36m"; YELLOW="\033[33m"; RED="\033[31m"; RESET="\033[0m"
else
  BOLD=""; DIM=""; GREEN=""; CYAN=""; YELLOW=""; RED=""; RESET=""
fi

step() { printf "\n${BOLD}${CYAN}→${RESET}  %s\n" "$*"; }
ok()   { printf "   ${GREEN}✓${RESET}  %s\n" "$*"; }
warn() { printf "   ${YELLOW}!${RESET}  %s\n" "$*"; }
die()  { printf "\n${RED}error:${RESET}  %s\n\n" "$*" >&2; exit 1; }

ask() {
  # ask VAR "Prompt" ["default"]
  local __var="$1" __prompt="$2" __default="${3:-}"
  if [ -n "$__default" ]; then
    printf "   %s ${DIM}[%s]${RESET}: " "$__prompt" "$__default"
  else
    printf "   %s: " "$__prompt"
  fi
  local __val
  read -r __val || true
  [ -z "$__val" ] && __val="$__default"
  printf -v "$__var" '%s' "$__val"
}

ask_password() {
  # ask_password VAR "Prompt"  — reads without echo, confirms, enforces min length
  local __var="$1" __prompt="$2"
  while true; do
    printf "   %s: " "$__prompt"
    local __val1
    read -rs __val1 || true
    printf "\n"
    printf "   Confirm: "
    local __val2
    read -rs __val2 || true
    printf "\n"
    if [ "$__val1" != "$__val2" ]; then
      warn "Passwords don't match. Try again."
      continue
    fi
    if [ "${#__val1}" -lt 12 ]; then
      warn "Password must be at least 12 characters."
      continue
    fi
    printf -v "$__var" '%s' "$__val1"
    break
  done
}

ask_yn() {
  # ask_yn VAR "Prompt" [y|n]
  local __var="$1" __prompt="$2" __default="${3:-y}"
  local __hint; [ "$__default" = "y" ] && __hint="Y/n" || __hint="y/N"
  printf "   %s ${DIM}[%s]${RESET}: " "$__prompt" "$__hint"
  local __val
  read -r __val || true
  __val="${__val:-$__default}"
  case "$__val" in
    y|Y|yes|Yes|YES) printf -v "$__var" '%s' "y" ;;
    *)               printf -v "$__var" '%s' "n" ;;
  esac
}

# ── header ────────────────────────────────────────────────────────────────────
printf "\n${BOLD}Tindra${RESET}  ${DIM}self-hosted install${RESET}\n"
printf "${DIM}Error tracking and performance monitoring in a single container.${RESET}\n"

# ── preflight ─────────────────────────────────────────────────────────────────
step "Checking prerequisites"

command -v docker >/dev/null 2>&1 \
  || die "docker is not installed. Get it at https://docs.docker.com/get-docker/"
ok "docker found ($(docker --version | cut -d' ' -f3 | tr -d ','))"

# Prefer the v2 plugin (docker compose), fall back to standalone docker-compose.
if docker compose version >/dev/null 2>&1; then
  DC="docker compose"
  ok "docker compose found ($(docker compose version --short 2>/dev/null || echo 'v2'))"
elif command -v docker-compose >/dev/null 2>&1; then
  DC="docker-compose"
  ok "docker-compose found ($(docker-compose version --short 2>/dev/null || docker-compose version | head -1))"
else
  die "docker compose not found. Get it at https://docs.docker.com/compose/install/"
fi

# ── install directory ─────────────────────────────────────────────────────────
step "Install directory"
printf "   ${DIM}Where to write docker-compose.yml.${RESET}\n"

ask INSTALL_DIR "Path" "$(pwd)"

# Expand a leading ~ that read won't expand automatically
INSTALL_DIR="${INSTALL_DIR/#\~/$HOME}"

[ -d "$INSTALL_DIR" ] || die "Directory does not exist: $INSTALL_DIR"

COMPOSE_FILE="$INSTALL_DIR/docker-compose.yml"

if [ -f "$COMPOSE_FILE" ]; then
  warn "docker-compose.yml already exists at $INSTALL_DIR"
  ask_yn OVERWRITE "Overwrite it?" "n"
  [ "$OVERWRITE" = "y" ] || { printf "\nAborted.\n\n"; exit 0; }
fi

# ── public URL ────────────────────────────────────────────────────────────────
step "Public URL"
printf "   ${DIM}The URL your users will reach Tindra at. Used to generate project DSNs.${RESET}\n"

while true; do
  ask PUBLIC_URL "URL (e.g. https://tindra.example.com)"
  [ -n "$PUBLIC_URL" ] && break
  warn "Required."
done

PUBLIC_URL="${PUBLIC_URL%/}"  # strip trailing slash

case "$PUBLIC_URL" in
  https://*) COOKIE_SECURE="true"  ;;
  *)         COOKIE_SECURE="false" ;;
esac

# ── local port ────────────────────────────────────────────────────────────────
step "Host port"
printf "   ${DIM}The port Docker will bind on this machine (your reverse proxy target).${RESET}\n"

ask HOST_PORT "Port" "8080"

case "$HOST_PORT" in
  ''|*[!0-9]*) die "Port must be a number." ;;
esac

# ── first account ─────────────────────────────────────────────────────────────
step "First account"
printf "   ${DIM}Tindra has no sign-up page — this creates your admin account.${RESET}\n"

while true; do
  ask FIRST_EMAIL "Email"
  [ -n "$FIRST_EMAIL" ] && break
  warn "Required."
done

ask FIRST_NAME "Name"

ask_password FIRST_PASSWORD "Password"
ok "Account details collected"

# ── database password ─────────────────────────────────────────────────────────
step "Generating database password"

if command -v openssl >/dev/null 2>&1; then
  DB_PASSWORD="$(openssl rand -hex 24)"
elif [ -r /dev/urandom ]; then
  DB_PASSWORD="$(LC_ALL=C tr -dc 'A-Za-z0-9' < /dev/urandom | head -c 48 || true)"
else
  die "Cannot generate a random password — install openssl and try again."
fi

[ -n "$DB_PASSWORD" ] || die "Password generation failed."
ok "48-character random password generated"

# ── write docker-compose.yml ──────────────────────────────────────────────────
step "Writing docker-compose.yml"

cat > "$COMPOSE_FILE" << EOF
services:
  postgres:
    image: postgres:18-alpine
    restart: unless-stopped
    environment:
      POSTGRES_DB: tindra
      POSTGRES_USER: tindra
      POSTGRES_PASSWORD: "${DB_PASSWORD}"
    volumes:
      - postgres_data:/var/lib/postgresql
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U tindra -d tindra"]
      interval: 5s
      timeout: 5s
      retries: 5

  tindra:
    image: ghcr.io/blendbyte/tindra:latest
    restart: unless-stopped
    ports:
      - "${HOST_PORT}:8080"
    environment:
      DATABASE_URL: "postgres://tindra:${DB_PASSWORD}@postgres:5432/tindra?sslmode=disable"
      PUBLIC_URL: "${PUBLIC_URL}"
      BIND_ADDR: ":8080"
      DATA_DIR: /data
      LOG_FORMAT: json
      COOKIE_SECURE: "${COOKIE_SECURE}"
      RETENTION_DAYS: "90"
      # ── email alerts (optional) ───────────────────────────────────────────
      # EMAIL_PROVIDER: smtp          # smtp | postmark | brevo | lettermint | ahasend | cloudflare
      # EMAIL_FROM: alerts@example.com
      # SMTP_HOST: smtp.example.com
      # SMTP_PORT: "587"
      # SMTP_USERNAME: user
      # SMTP_PASSWORD: secret
    volumes:
      - tindra_data:/data
    depends_on:
      postgres:
        condition: service_healthy

volumes:
  postgres_data:
  tindra_data:
EOF

ok "Written to $COMPOSE_FILE"

# ── pull images ───────────────────────────────────────────────────────────────
step "Pulling images"
$DC -f "$COMPOSE_FILE" pull

# ── summary ───────────────────────────────────────────────────────────────────
printf "\n"
printf "   ${DIM}Public URL  ${RESET}  %s\n" "$PUBLIC_URL"
printf "   ${DIM}Host port   ${RESET}  %s\n" "$HOST_PORT"
printf "   ${DIM}First user  ${RESET}  %s\n" "$FIRST_EMAIL"
printf "   ${DIM}Data        ${RESET}  docker volumes (postgres_data, tindra_data)\n"
printf "   ${DIM}DB password ${RESET}  stored in %s\n" "$COMPOSE_FILE"

# ── start? ────────────────────────────────────────────────────────────────────
printf "\n"
ask_yn START "Start Tindra now?" "y"

if [ "$START" = "y" ]; then
  step "Starting"
  $DC -f "$COMPOSE_FILE" up -d

  step "Creating your account"
  printf "   ${DIM}Waiting for Tindra to be ready...${RESET}\n"

  CREATED=0
  for i in $(seq 1 20); do
    if $DC -f "$COMPOSE_FILE" exec -T tindra /tindra users create \
        --email "$FIRST_EMAIL" \
        --name "$FIRST_NAME" \
        --password "$FIRST_PASSWORD" >/dev/null 2>&1; then
      CREATED=1
      break
    fi
    sleep 3
  done

  if [ "$CREATED" = "1" ]; then
    ok "Account created: $FIRST_EMAIL"
    printf "\n"
    printf "   ${GREEN}${BOLD}Tindra is running.${RESET}\n"
    printf "   Open ${CYAN}%s${RESET} and log in.\n" "$PUBLIC_URL"
  else
    warn "Tindra took too long to start. Create your account once it's up:"
    printf "   ${CYAN}%s -f %s exec tindra /tindra users create --email '%s' --name '%s' --password 'yourpassword'${RESET}\n" \
      "$DC" "$COMPOSE_FILE" "$FIRST_EMAIL" "$FIRST_NAME"
  fi

  printf "\n"
  printf "   ${DIM}To enable email alerts, edit docker-compose.yml and set EMAIL_PROVIDER${RESET}\n"
  printf "   ${DIM}and the matching credentials. See ${RESET}${CYAN}https://tindra.sh/docs${RESET}${DIM} for provider options.${RESET}\n"
  printf "\n"
  printf "   ${DIM}Useful commands:${RESET}\n"
  printf "   ${DIM}  $DC -f %s logs -f tindra${RESET}\n" "$COMPOSE_FILE"
  printf "   ${DIM}  $DC -f %s restart tindra${RESET}\n" "$COMPOSE_FILE"
  printf "   ${DIM}  $DC -f %s down${RESET}\n" "$COMPOSE_FILE"
else
  printf "\n"
  printf "   ${BOLD}Ready.${RESET}  Start and create your account:\n"
  printf "\n"
  printf "   ${CYAN}$DC -f %s up -d${RESET}\n" "$COMPOSE_FILE"
  printf "   ${CYAN}$DC -f %s exec tindra /tindra users create --email '%s' --name '%s' --password 'yourpassword'${RESET}\n" \
    "$DC" "$COMPOSE_FILE" "$FIRST_EMAIL" "$FIRST_NAME"
fi

printf "\n"
