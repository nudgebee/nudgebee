#!/bin/sh

# entrypoint.sh: This script starts the correct process based on the first argument.

# Exit immediately if a command exits with a non-zero status.
set -e

# The first argument determines which service to run.
SERVICE="$1"

if [ "$SERVICE" = "web" ]; then
  # Start the Uvicorn web server (FastAPI).
  echo "Starting Uvicorn web server..."

  # Single worker for Qdrant persistent mode (avoids file locking issues)
  WORKERS="${UVICORN_WORKERS:-1}"
  PORT="${RAGSERVER_PORT:-9999}"
  TIMEOUT="${UVICORN_TIMEOUT:-180}"
  LOG_CONFIG="${LOGGING_CONFIG_FILE:-/app/config/logging.json}"

  echo "Using logging config: $LOG_CONFIG"

  exec uvicorn server:app \
    --host 0.0.0.0 \
    --port "$PORT" \
    --workers "$WORKERS" \
    --timeout-keep-alive "$TIMEOUT" \
    --log-config "$LOG_CONFIG"
elif [ "$SERVICE" = "search" ]; then
  # DEPRECATED: Search service is no longer needed. Searches now run in-process.
  echo "WARNING: 'search' service is deprecated and no longer needed."
  echo "All searches now run directly in the main web server process."
  echo "Please update your deployment to remove the search service sidecar."
  echo "Exiting..."
  exit 1
else
  # If no valid service is specified, show an error.
  echo "Error: Unknown service '$SERVICE'"
  echo "Valid service: 'web'"
  exit 1
fi
