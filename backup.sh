#!/usr/bin/env bash
# A full, compressed dump of the Flowa database (accounts, posts, comments, progress, catalogs,
# everything), kept for 14 days. The app's own daily backup covers progress documents only.
#   ./backup.sh                       dump to data/backups/flowa-DATE.dump
#   crontab: 17 3 * * * /home/pi/nunchaku/backup.sh >> /home/pi/nunchaku/data/backups/backup.log 2>&1
# Restore into an empty database (stop the app first):
#   docker exec -i nunchaku-db pg_restore -U flowa -d flowa --clean --if-exists < data/backups/flowa-DATE.dump
# CONTAINER, PGUSER, PGDB, OUT and KEEP can be set to point it elsewhere (the tests use a throwaway database).
set -euo pipefail
cd "$(dirname "$0")"
CONTAINER=${CONTAINER:-nunchaku-db} PGUSER=${PGUSER:-flowa} PGDB=${PGDB:-flowa} OUT=${OUT:-data/backups} KEEP=${KEEP:-14}
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
