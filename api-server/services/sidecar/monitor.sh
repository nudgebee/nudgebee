#!/bin/sh
set -euo pipefail

THRESHOLD_PERCENTAGE=${THRESHOLD_PERCENTAGE:-85}

PPROF_HOST=${PPROF_HOST:-"localhost"}
PPROF_PORT=${PPROF_PORT:-"6060"}
OUTPUT_DIR=${OUTPUT_DIR:-"/root/profiles"}

VM_HOST=${VM_HOST:-""}
VM_PATH=${VM_PATH:-"/home/profile/pprof_profiles"}
POD_NAME=${POD_NAME:-"unknown-pod"}

ORIGINAL_SSH_KEY_PATH="/root/.ssh/id_rsa"
TMP_SSH_DIR="/tmp/.ssh"
SSH_KEY_PATH="${TMP_SSH_DIR}/id_rsa"
SSH_CONFIG_PATH="${TMP_SSH_DIR}/config"

if [ -z "${MEMORY_LIMIT_IN_BYTES:-}" ] || [ "${MEMORY_LIMIT_IN_BYTES}" -eq 0 ]; then
  echo "ERROR: MEMORY_LIMIT_IN_BYTES not set or is zero." >&2
  exit 1
fi

if [ -f "$ORIGINAL_SSH_KEY_PATH" ]; then
  mkdir -p "$TMP_SSH_DIR"
  cp "$ORIGINAL_SSH_KEY_PATH" "$SSH_KEY_PATH"
  chmod 600 "$SSH_KEY_PATH"

  echo "Creating SSH config in $SSH_CONFIG_PATH..."
  cat <<EOF > "$SSH_CONFIG_PATH"
Host *
    IdentityFile $SSH_KEY_PATH
    StrictHostKeyChecking no
    UserKnownHostsFile /dev/null
EOF
  chmod 600 "$SSH_CONFIG_PATH"

  export HOME="/tmp"
else
  echo "WARNING: SSH key not found at $ORIGINAL_SSH_KEY_PATH — remote upload will be skipped."
fi

if [ -f "/sys/fs/cgroup/memory/memory.usage_in_bytes" ]; then
    CGROUP_MEMORY_USAGE_PATH="/sys/fs/cgroup/memory/memory.usage_in_bytes"
    echo "cgroup v1 detected."
elif [ -f "/sys/fs/cgroup/memory.current" ]; then
    CGROUP_MEMORY_USAGE_PATH="/sys/fs/cgroup/memory.current"
    echo "cgroup v2 detected."
else
    echo "ERROR: Could not find memory usage file in /sys/fs/cgroup"
    exit 1
fi

echo "-------------------------------------"
echo "Monitoring pod memory..."
echo "Memory Limit: ${MEMORY_LIMIT_IN_BYTES} bytes"
echo "Threshold: ${THRESHOLD_PERCENTAGE}%"
echo "Reading usage from: ${CGROUP_MEMORY_USAGE_PATH}"
echo "Remote upload host: ${VM_HOST:-<none>}"
echo "SSH config path: ${SSH_CONFIG_PATH:-<none>}"
echo "-------------------------------------"

while true; do
  current_usage_bytes=$(cat "${CGROUP_MEMORY_USAGE_PATH}")
  usage_percentage=$(echo "scale=2; (${current_usage_bytes} * 100) / ${MEMORY_LIMIT_IN_BYTES}" | bc)
  is_over_threshold=$(echo "${usage_percentage} ${THRESHOLD_PERCENTAGE}" | awk '{if ($1 > $2) print 1; else print 0;}')

  echo "Current Usage: ${current_usage_bytes} bytes (${usage_percentage}%)"

  if [ "${is_over_threshold}" -eq 1 ]; then
    ts=$(date +%Y%m%d_%H%M%S)
    PROFILE_FILE_PATH="${OUTPUT_DIR}/heap-${POD_NAME}-${ts}.pprof"

    echo "Memory usage ${usage_percentage}% exceeds threshold (${THRESHOLD_PERCENTAGE}%)"
    echo "Capturing heap profile to ${PROFILE_FILE_PATH}..."

    if curl -fsS "http://${PPROF_HOST}:${PPROF_PORT}/debug/pprof/heap" > "${PROFILE_FILE_PATH}"; then
      echo "Profile captured successfully."

      if [ -n "${VM_HOST}" ] && [ -f "$SSH_KEY_PATH" ]; then
        echo "Creating remote directory if needed..."
        ssh -F "$SSH_CONFIG_PATH" "$VM_HOST" "mkdir -p '${VM_PATH}/${POD_NAME}'"

        echo "Uploading ${PROFILE_FILE_PATH} to ${VM_HOST}:${VM_PATH}/${POD_NAME}/ ..."
        scp -F "$SSH_CONFIG_PATH" "${PROFILE_FILE_PATH}" "${VM_HOST}:${VM_PATH}/${POD_NAME}/" && {
          echo "Upload complete. Cleaning up local file."
          rm -f "${PROFILE_FILE_PATH}"
        } || echo "⚠️ Upload failed, keeping local file."
      else
        echo "⚠️ Remote upload skipped — VM_HOST or SSH key missing."
      fi

      echo "Monitor exiting after successful capture."
      exit 0
    else
      echo "Failed to capture profile from ${PPROF_HOST}:${PPROF_PORT}"
    fi
  fi

  sleep 5
done
