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

# Resolve the output directory now: the script changes into COMPOSE_DIR below,
# and a relative path must still mean "relative to where it was run".
[ -d "$OUTPUT_DIR" ] || die "Output directory does not exist: $OUTPUT_DIR"
OUTPUT_DIR="$(cd "$OUTPUT_DIR" && pwd)"

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
  warn "Tindra container not running, so the data directory was skipped"
fi

# ── 3. manifest ───────────────────────────────────────────────────────────────
cat > "$WORK/manifest.txt" << EOF
Tindra backup
Created:   $TS

Contents:
  postgres.dump   pg_restore-format database dump
  data/           Tindra data directory (source maps, etc.)

Restore (run in the directory with docker-compose.yml; B is this folder):
  B=/path/to/$BACKUP_NAME
  # 1. Stop Tindra and make sure Postgres is up:
  docker compose stop tindra
  docker compose up -d postgres
  # 2. Replace the database with the backup. Recreating it (rather than
  #    pg_restore --clean) also removes tables added by newer versions, so
  #    Tindra can migrate the restored schema forward on start.
  docker compose exec -T postgres dropdb -U tindra --force --if-exists tindra
  docker compose exec -T postgres createdb -U tindra tindra
  docker compose exec -T postgres pg_restore -U tindra -d tindra --no-owner < "\$B/postgres.dump"
  # 3. Restore the data directory into Tindra's volume, owned by the
  #    non-root user the image runs as:
  docker compose up --no-start tindra
  docker run --rm --volumes-from "\$(docker compose ps -aq tindra)" -v "\$B/data:/restore:ro" \\
    alpine sh -c 'cp -a /restore/. /data/ && chown -R 65532:65532 /data'
  # 4. Start Tindra:
  docker compose up -d
EOF

# ── 4. pack ───────────────────────────────────────────────────────────────────
step "Packing archive"
ARCHIVE="$OUTPUT_DIR/$BACKUP_NAME.tar.gz"
tar -czf "$ARCHIVE" -C "$STAGE" "$BACKUP_NAME"
ok "$BACKUP_NAME.tar.gz  $(du -sh "$ARCHIVE" | cut -f1)"

printf "\n${GREEN}${BOLD}Backup complete.${RESET}\n\n"
printf "   ${DIM}Archive:${RESET}  %s\n\n" "$ARCHIVE"
