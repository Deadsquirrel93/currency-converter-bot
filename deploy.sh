#!/usr/bin/env sh
set -eu

cd "$(dirname "$0")"

echo "Updating repository..."
git pull --ff-only

echo "Rebuilding and restarting Docker Compose services..."
docker compose up -d --build --force-recreate --remove-orphans

echo "Current service status:"
docker compose ps
