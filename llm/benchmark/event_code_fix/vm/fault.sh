#!/usr/bin/env bash
# Inject or restore one benchmark fault on a VM over SSH.
#
#   fault.sh <fault> <inject|restore> <ssh target> [argument]
#
# <ssh target> is whatever `ssh` accepts (user@host, or a Host from ~/.ssh/config).
#
# VM accounts run systemd services only, so every fault is a transient systemd
# unit: it shows up in `systemctl status` and `journalctl -u`, and node-agent
# sees it as /system.slice/<unit>.service, the way a real service looks. Every
# unit has RuntimeMaxSec, so a failed restore cannot leave a host broken.
set -euo pipefail

fault=${1:?fault}
action=${2:?inject|restore}
target=${3:?ssh target}
arg=${4:-}

remote() { ssh -o BatchMode=yes -o ConnectTimeout=15 "$target" "$@"; }

case "$fault:$action" in
  # nb-bench-logger.service writes one error line over and over into
  # /var/log/nb-bench/app.log until the file is `arg` MB. promtail/alloy must
  # ship /var/log/nb-bench/*.log for the logs signal to see it.
  disk_runaway_log:inject)
    [[ $arg =~ ^[0-9]+$ ]] && ((arg >= 1 && arg <= 4096)) \
      || { echo "disk_runaway_log needs a size in MB between 1 and 4096" >&2; exit 2; }
    remote bash -s <<EOF
set -e
sudo mkdir -p /var/log/nb-bench
sudo systemd-run --quiet --unit=nb-bench-logger -p RuntimeMaxSec=1800 \\
  sh -c 'yes "level=error msg=\\"upstream request timed out\\" upstream=billing-api retries=3" | head -c ${arg}M >> /var/log/nb-bench/app.log'
EOF
    ;;
  disk_runaway_log:restore)
    remote "sudo systemctl stop nb-bench-logger.service 2>/dev/null || true; sudo systemctl reset-failed nb-bench-logger.service 2>/dev/null || true; sudo rm -rf /var/log/nb-bench"
    ;;

  # nb-bench-spin.service: one shell loop pinning a core.
  cpu_spin:inject)
    remote "sudo systemd-run --quiet --unit=nb-bench-spin -p RuntimeMaxSec=1800 sh -c 'while :; do :; done'"
    ;;
  cpu_spin:restore)
    remote "sudo systemctl stop nb-bench-spin.service 2>/dev/null || true; sudo systemctl reset-failed nb-bench-spin.service 2>/dev/null || true"
    ;;

  # nb-bench-oom.service grows ~2 MB/s until its own MemoryMax (`arg` MB) is
  # hit and the kernel OOM-kills it, leaving the unit failed with
  # Result=oom-kill. The host itself has plenty of free memory: the limit, not
  # host pressure, is the cause. Needs cgroup v2 (MemoryMax, MemorySwapMax).
  memory_oom:inject)
    [[ $arg =~ ^[0-9]+$ ]] && ((arg >= 16 && arg <= 1024)) \
      || { echo "memory_oom needs a MemoryMax in MB between 16 and 1024" >&2; exit 2; }
    remote bash -s <<EOF
set -e
sudo systemd-run --quiet --unit=nb-bench-oom -p MemoryMax=${arg}M -p MemorySwapMax=0 \\
  -p RuntimeMaxSec=1800 \\
  bash -c 'x=; while :; do x+=\$(head -c 4000000 /dev/zero | tr "\\0" a); sleep 2; done'
EOF
    ;;
  memory_oom:restore)
    remote "sudo systemctl stop nb-bench-oom.service 2>/dev/null || true; sudo systemctl reset-failed nb-bench-oom.service 2>/dev/null || true"
    ;;

  # memory-leak-oom.service: the nb-benchmark-fixtures memory_leak_oom worker,
  # installed with its own deploy/install.sh. `arg` is a local checkout of that
  # fixture directory (the fixtures repo is private, so the host cannot clone
  # it); it is streamed over ssh and installed from a temporary directory. The
  # cache never evicts, so the unit is OOM-killed at its MemoryMax and restarts.
  fixture_memory_leak:inject)
    [[ -f $arg/deploy/install.sh && -d $arg/src ]] \
      || { echo "fixture_memory_leak needs the memory_leak_oom checkout as its argument" >&2; exit 2; }
    tar -C "$arg" -c src deploy | remote 'set -e; d=$(mktemp -d); tar -C "$d" -x; cd "$d"; sudo ./deploy/install.sh; cd /; rm -rf "$d"'
    ;;
  fixture_memory_leak:restore)
    remote "sudo systemctl disable --now memory-leak-oom.service 2>/dev/null || true; sudo rm -f /etc/systemd/system/memory-leak-oom.service; sudo systemctl daemon-reload; sudo systemctl reset-failed memory-leak-oom.service 2>/dev/null || true; sudo rm -rf /opt/nb-benchmark-fixtures/memory_leak_oom"
    ;;

  *)
    echo "unknown fault/action: $fault:$action" >&2
    exit 2
    ;;
esac
