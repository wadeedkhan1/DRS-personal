#!/bin/bash
# DRS Single-VPS Global Deployment Script
# Supports Ubuntu 22.04 / 24.04 LTS & Debian 12
set -e

echo "=================================================="
echo "🚀 DRS Platform — VPS Provisioning & Deployment"
echo "=================================================="

# 1. Update and install base packages
echo "📦 Updating OS packages..."
apt-get update && apt-get install -y apt-transport-https ca-certificates curl gnupg lsb-release ufw gzip

# 2. Install Docker & Docker Compose if not present
if ! command -v docker &> /dev/null; then
    echo "🐳 Installing Docker Engine..."
    curl -fsSL https://get.docker.com -o get-docker.sh
    sh get-docker.sh
    rm get-docker.sh
fi

# 3. Configure UFW Firewall
echo "🛡️ Configuring Firewall (UFW)..."
ufw allow 22/tcp
ufw allow 80/tcp
ufw allow 443/tcp
ufw --force enable

# 4. Setup nightly database backup cron
echo "⏰ Setting up automated database backup cron..."
chmod +x /opt/drs/deploy/scripts/backup_db.sh || true
(crontab -l 2>/dev/null | grep -v "backup_db.sh" ; echo "0 2 * * * /opt/drs/deploy/scripts/backup_db.sh >> /var/log/drs_backup.log 2>&1") | crontab -

# 5. Build and launch Docker Compose stack
echo "🚀 Launching DRS Stack (Nginx + Go Backend + PostgreSQL 16)..."
cd /opt/drs/deploy
docker compose down || true
docker compose up -d --build

echo "=================================================="
echo "✅ DRS Platform is live and running globally!"
echo "👉 Web Portal: http://$(curl -s ifconfig.me)"
echo "👉 Health check: http://$(curl -s ifconfig.me)/health"
echo "=================================================="
