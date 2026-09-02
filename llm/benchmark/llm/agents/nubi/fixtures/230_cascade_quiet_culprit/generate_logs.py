#!/usr/bin/env python3
"""Emits the log stream for one role in the namespace-230 cascade.

ROLE=dependent services report their own timeouts and 5xx — loud, plentiful and
misleading. ROLE=cache reports steady evictions at maxmemory: the actual cause,
logged at INFO because from redis's point of view nothing is wrong.
"""

import json
import os
import random
import time
from datetime import datetime

ROLE = os.environ.get("ROLE", "dependent")
SERVICE = os.environ.get("SERVICE_NAME", "unknown")


def emit(level, message, **fields):
    entry = {
        "timestamp": datetime.utcnow().isoformat() + "Z",
        "level": level,
        "service": SERVICE,
        "message": message,
    }
    entry.update(fields)
    print(json.dumps(entry), flush=True)


def dependent_cycle():
    # Every one of these points at this service or at the network, never at the
    # cache being full. That is the trap.
    roll = random.random()
    if roll < 0.45:
        emit(
            "ERROR",
            "upstream request failed",
            status=503,
            error="context deadline exceeded",
            upstream="redis-cache:6379",
        )
    elif roll < 0.70:
        emit(
            "ERROR",
            "handler returned 500",
            status=500,
            latency_ms=random.randint(3000, 9000),
        )
    elif roll < 0.85:
        emit("WARN", "request latency above SLO", latency_ms=random.randint(1200, 4000))
    else:
        emit("INFO", "request served", status=200, latency_ms=random.randint(20, 90))


def cache_cycle():
    # Healthy-looking. No errors, no warnings above INFO, pod stays 2/2 Running.
    used = random.randint(1043, 1048)
    emit(
        "INFO",
        "background eviction cycle complete",
        used_memory_mb=used,
        maxmemory_mb=1048,
        evicted_keys=random.randint(180, 420),
        keyspace_hits=random.randint(40, 120),
        keyspace_misses=random.randint(900, 1400),
        policy="allkeys-lru",
    )


while True:
    if ROLE == "cache":
        cache_cycle()
        time.sleep(3)
    else:
        dependent_cycle()
        time.sleep(random.uniform(0.4, 1.2))
