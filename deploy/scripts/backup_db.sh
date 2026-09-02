#!/bin/bash
# DRS Automated Nightly PostgreSQL Backup Script
set -e

BACKUP_DIR="/var/backups/drs"
DATE=$(date +%Y%m%d_%H%M%S)
BACKUP_FILE="${BACKUP_DIR}/drs_db_${DATE}.sql.gz"
RETENTION_DAYS=14

mkdir -p "$BACKUP_DIR"

echo "[$(date)] Starting DRS PostgreSQL database backup..."
docker exec -t drs_postgres pg_dump -U postgres drs_db | gzip > "$BACKUP_FILE"

echo "[$(date)] Backup completed successfully: $BACKUP_FILE ($(du -h "$BACKUP_FILE" | cut -f1))"

# Prune backups older than RETENTION_DAYS
find "$BACKUP_DIR" -type f -name "drs_db_*.sql.gz" -mtime +$RETENTION_DAYS -delete
echo "[$(date)] Pruned backups older than ${RETENTION_DAYS} days."
