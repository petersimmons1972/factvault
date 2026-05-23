#!/bin/bash
#
# Manage cron entry for Claude heartbeat watchdog.
# Usage: install-watchdog-cron.sh [--uninstall]
#

set -euo pipefail

CRON_CMD="*/5 * * * * cd /home/psimmons/projects/factvault && python3 .agent-comms/bin/watchdog.py >> .agent-comms/bin/watchdog.log 2>&1"
CRON_MARKER=".agent-comms watchdog"

case "${1:-}" in
  --uninstall)
    echo "Removing watchdog cron entry..."
    crontab -l 2>/dev/null | grep -v "$CRON_MARKER" | crontab - 2>/dev/null || true
    echo "Watchdog cron removed."
    ;;
  *)
    echo "Installing watchdog cron entry..."
    if crontab -l 2>/dev/null | grep -q "$CRON_MARKER"; then
      echo "Watchdog cron already installed."
      exit 0
    fi
    (crontab -l 2>/dev/null || true; echo "# $CRON_MARKER") | crontab -
    (crontab -l 2>/dev/null; echo "$CRON_CMD") | crontab -
    echo "Watchdog cron installed."
    ;;
esac
