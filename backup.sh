#!/usr/bin/env bash
# A full, compressed dump of the Flowa database (accounts, posts, comments, progress, catalogs,
# everything), kept for 14 days. The app's own daily backup covers progress documents only.
#   ./backup.sh                       dump to data/backups/flowa-DATE.dump
#   ./backup.sh --restore FILE        put that dump back (stop the app first: docker stop nunchaku)
#   crontab: 17 3 * * * /home/pi/nunchaku/backup.sh >> /home/pi/nunchaku/data/backups/backup.log 2>&1
# A restore replaces what the dump holds, in one transaction: if anything in it fails, the database is
# left exactly as it was. It refuses to start while the app is running, because the app would be
# reading tables as they are replaced. Afterwards start the app again (docker start nunchaku); a dump
# from an older version is brought up to date by the app's own migrations at start.
# The restore drill (test/restore.sh, weekly in CI) runs both halves of this script against a
# throwaway database and compares every row.
# CONTAINER, APP, PGUSER, PGDB, OUT and KEEP can be set to point it elsewhere (the drill does).
set -euo pipefail
cd "$(dirname "$0")"
CONTAINER=${CONTAINER:-nunchaku-db} APP=${APP:-nunchaku} PGUSER=${PGUSER:-flowa} PGDB=${PGDB:-flowa} OUT=${OUT:-data/backups} KEEP=${KEEP:-14}

if [ "${1:-}" = --restore ]; then
  file=${2:-}
  [ -n "$file" ] && [ -f "$file" ] || { echo "usage: ./backup.sh --restore FILE (a dump this script made)" >&2; exit 2; }
  if [ "$(docker inspect "$APP" --format '{{.State.Running}}' 2>/dev/null)" = true ]; then
    echo "the app ($APP) is running: stop it first (docker stop $APP), restore, then start it again" >&2
    exit 1
  fi
  # A file that is not a dump, or one cut short, is turned down before anything is touched.
  docker exec -i "$CONTAINER" pg_restore --list < "$file" > /dev/null || { echo "$file is not a dump pg_restore can read" >&2; exit 1; }
  docker exec -i "$CONTAINER" pg_restore -U "$PGUSER" -d "$PGDB" --clean --if-exists --no-owner --single-transaction --exit-on-error < "$file"
  echo "$(date -Is) restored: $file into $PGDB on $CONTAINER"
  exit 0
fi
[ $# -eq 0 ] || { echo "usage: ./backup.sh [--restore FILE]" >&2; exit 2; }

mkdir -p "$OUT"
file="$OUT/flowa-$(date +%F).dump" tmp="$OUT/.flowa-$$.tmp"
trap 'rm -f "$tmp"' EXIT
docker exec "$CONTAINER" pg_dump -U "$PGUSER" -d "$PGDB" -Fc > "$tmp"
# A dump that pg_restore cannot list is not kept.
docker exec -i "$CONTAINER" pg_restore --list < "$tmp" > /dev/null
chmod 600 "$tmp"
mv "$tmp" "$file"
ls -1 "$OUT"/flowa-*.dump | sort | head -n -"$KEEP" | xargs -r rm -f
echo "$(date -Is) backup: $file, $(du -h "$file" | cut -f1)"
