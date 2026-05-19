#!/usr/bin/env bash
# Usage: bash backup.sh [compose-dir] [output-dir]
#   compose-dir  directory containing docker-compose.yml  (default: current dir)
#   output-dir   where to write the .tar.gz               (default: current dir)

set -euo pipefail

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

COMPOSE_DIR="${1:-.}"
OUTPUT_DIR="${2:-.}"

# ── locate docker compose ─────────────────────────────────────────────────────
if docker compose version >/dev/null 2>&1; then
  DC="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
  DC="docker-compose"
else
  die "docker compose not found"
fi

[ -f "$COMPOSE_DIR/docker-compose.yml" ] \
  || die "No docker-compose.yml found in $COMPOSE_DIR"

cd "$COMPOSE_DIR"

# ── check postgres is up ──────────────────────────────────────────────────────
$DC exec -T postgres pg_isready -U tindra -q \
  || die "Postgres is not running. Start Tindra first with: $DC up -d"

# ── prepare staging dir ───────────────────────────────────────────────────────
TS=$(date +%Y%m%d-%H%M%S)
BACKUP_NAME="tindra-backup-$TS"
STAGE=$(mktemp -d)
WORK="$STAGE/$BACKUP_NAME"
mkdir -p "$WORK"
trap 'rm -rf "$STAGE"' EXIT

# ── 1. database ───────────────────────────────────────────────────────────────
step "Dumping database"
$DC exec -T postgres pg_dump -U tindra --format=custom tindra \
  > "$WORK/postgres.dump"
ok "postgres.dump  $(du -sh "$WORK/postgres.dump" | cut -f1)"

# ── 2. data volume (source maps, attachments) ─────────────────────────────────
step "Copying data volume"
TINDRA_CID=$($DC ps -q tindra 2>/dev/null | head -1)
if [ -n "$TINDRA_CID" ]; then
  docker cp "$TINDRA_CID:/data" "$WORK/data"
  ok "data/  $(du -sh "$WORK/data" | cut -f1)"
else
  warn "Tindra container not running — data directory skipped"
fi

# ── 3. manifest ───────────────────────────────────────────────────────────────
cat > "$WORK/manifest.txt" << EOF
Tindra backup
Created:   $TS

Contents:
  postgres.dump   pg_restore-format database dump
  data/           Tindra data directory (source maps, etc.)

Restore:
  # 1. Start a fresh Tindra stack and wait for postgres:
  #    docker compose up -d postgres
  # 2. Restore the database:
  #    docker compose exec -T postgres pg_restore -U tindra -d tindra --clean < postgres.dump
  # 3. Restore data:
  #    docker compose cp data/. tindra:/data
  # 4. Restart:
  #    docker compose restart tindra
EOF

# ── 4. pack ───────────────────────────────────────────────────────────────────
step "Packing archive"
ARCHIVE="$(cd "$OUTPUT_DIR" && pwd)/$BACKUP_NAME.tar.gz"
tar -czf "$ARCHIVE" -C "$STAGE" "$BACKUP_NAME"
ok "$BACKUP_NAME.tar.gz  $(du -sh "$ARCHIVE" | cut -f1)"

printf "\n${GREEN}${BOLD}Backup complete.${RESET}\n\n"
printf "   ${DIM}Archive:${RESET}  %s\n\n" "$ARCHIVE"
