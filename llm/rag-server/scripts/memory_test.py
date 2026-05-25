"""
API Load Generator and Local Memory Test Script for RAG Server.

This script serves three primary purposes, controlled by command-line flags:

1.  **API Load Generation (Default Mode):**
    Sends a high volume of concurrent requests to the RAG server's API endpoint.
    This is the primary mode for performance testing.
    Usage: `python tools/memory_test.py`

2.  **Combined Load Test & Memory Monitoring (`--monitor` mode):**
    Runs the API load test while simultaneously monitoring the memory usage (RSS)
    of local Python processes (e.g., Gunicorn workers). This is useful for
    diagnosing memory leaks under load in a local environment.
    Usage: `python tools/memory_test.py --monitor`

3.  **Memory Monitoring Only (`--monitor --skip-load-test` mode):**
    Only monitors the memory of local Python processes without generating any
    API load. This allows you to observe memory usage while manually sending
    requests or using other testing tools.
    Usage: `python tools/memory_test.py --monitor --skip-load-test`
"""

import argparse
import datetime
import json
import logging
import os
import threading
import time
from concurrent.futures import ThreadPoolExecutor, as_completed

import psutil
import requests

# --- Configuration ---
# The full URL of the API endpoint to test
API_ENDPOINT = "http://127.0.0.1:9988/get_matching_doc"
# Total number of requests to send
TOTAL_REQUESTS = 1000
# Number of concurrent requests to send at a time
CONCURRENCY = 10
# Log file for memory usage (only used in --monitor mode)
MEMORY_LOG_FILE = "memory_log.csv"
# Interval in seconds for memory logging (only used in --monitor mode)
LOG_INTERVAL = 2

# --- Setup Logging ---
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s - %(levelname)s - %(message)s",
    handlers=[logging.StreamHandler()],
)


def get_python_worker_pids(retries=5, delay=2):
    """
    Finds all python processes that are not this script.
    """
    for attempt in range(retries):
        pids = []
        try:
            my_pid = os.getpid()
            for proc in psutil.process_iter(["pid", "name", "cmdline"]):
                if "python" in proc.info["name"] and proc.info["pid"] != my_pid:
                    cmdline = proc.info["cmdline"]
                    if cmdline and "tools/memory_test.py" not in " ".join(cmdline):
                        pids.append(proc.info["pid"])

            if pids:
                return pids
            else:
                logging.debug(f"Attempt {attempt + 1}: No other python processes found. Retrying in {delay}s...")
                time.sleep(delay)

        except (psutil.NoSuchProcess, psutil.AccessDenied) as e:
            logging.error(f"Error finding python processes: {e}")
            time.sleep(delay)

    logging.warning("No other python processes found after multiple retries.")
    return []


def monitor_memory(pids, stop_event):
    """
    Monitors and logs the memory usage of specified PIDs to a CSV file.
    """
    logging.info(f"Starting memory monitor for PIDs: {pids}")
    with open(MEMORY_LOG_FILE, "w") as f:
        header = "timestamp," + ",".join([f"pid_{pid}_rss_mb" for pid in pids]) + "\n"
        f.write(header)

        while not stop_event.is_set():
            try:
                memory_usage = []
                for pid in pids:
                    try:
                        process = psutil.Process(pid)
                        rss_mb = process.memory_info().rss / (1024 * 1024)
                        memory_usage.append(f"{rss_mb:.2f}")
                    except psutil.NoSuchProcess:
                        memory_usage.append("N/A")

                timestamp = datetime.datetime.now().isoformat()
                log_line = f"{timestamp},{','.join(memory_usage)}\n"
                f.write(log_line)
                f.flush()

            except Exception as e:
                logging.error(f"Error during memory monitoring: {e}")

            time.sleep(LOG_INTERVAL)

    logging.info("Memory monitor stopped.")


def send_request(session, request_id):
    """
    Sends a single POST request to the API endpoint.
    """
    payload = {
        "query": "cluster cpu usage",
        "k": 1,
        "account_id": "496fe460-d542-498b-8cab-a7cb9cce8eb2",
        "module": "prometheus",
    }
    headers = {"Content-Type": "application/json"}

    try:
        response = session.post(API_ENDPOINT, data=json.dumps(payload), headers=headers, timeout=120)
        return response.status_code, response.text
    except requests.exceptions.RequestException as e:
        logging.error(f"Request {request_id} failed: {e}")
        return None, str(e)


def run_load_test():
    """
    Runs the main load test, sending requests concurrently.
    """
    logging.info(
        f"Starting load test: {TOTAL_REQUESTS} requests with {CONCURRENCY} concurrent workers on {API_ENDPOINT}."
    )
    success_count = 0
    failure_count = 0

    with requests.Session() as session:
        with ThreadPoolExecutor(max_workers=CONCURRENCY) as executor:
            futures = [executor.submit(send_request, session, i) for i in range(TOTAL_REQUESTS)]

            for i, future in enumerate(as_completed(futures)):
                status_code, _ = future.result()
                if status_code == 200:
                    success_count += 1
                else:
                    failure_count += 1

                if (i + 1) % 50 == 0:
                    logging.info(f"Completed {i + 1}/{TOTAL_REQUESTS} requests...")

    logging.info("Load test finished.")
    logging.info(f"Results: {success_count} successful, {failure_count} failed.")


def main():
    """
    Main function to orchestrate the memory monitoring and load testing.
    """
    parser = argparse.ArgumentParser(description="RAG Server API Load Generator and Memory Test")
    parser.add_argument("--monitor", action="store_true", help="Enable local process memory monitoring and logging.")
    parser.add_argument(
        "--skip-load-test", action="store_true", help="Only run memory monitoring (requires --monitor)."
    )
    args = parser.parse_args()

    monitor_thread = None
    stop_event = threading.Event()

    # --- Step 1: Setup Memory Monitoring if --monitor is specified ---
    if args.monitor:
        worker_pids = get_python_worker_pids()
        if not worker_pids:
            logging.error("Could not find any local Python processes to monitor. Is the server running?")
            return

        logging.info(f"Found local Python worker PIDs: {worker_pids}")
        monitor_thread = threading.Thread(target=monitor_memory, args=(worker_pids, stop_event))
        monitor_thread.daemon = True
        monitor_thread.start()

    # --- Step 2: Run Load Test unless skipped ---
    try:
        if not args.skip_load_test:
            run_load_test()
        elif args.monitor:
            # This is the "monitor only" mode
            logging.info("Skipping load test. Running memory monitor only. Press Ctrl+C to stop.")
            while True:
                time.sleep(1)
        else:
            logging.warning("No action taken. --skip-load-test requires --monitor to run the monitor.")

    except KeyboardInterrupt:
        logging.info("Process interrupted by user.")

    finally:
        # --- Step 3: Cleanup ---
        if monitor_thread and monitor_thread.is_alive():
            logging.info("Stopping memory monitor...")
            stop_event.set()
            monitor_thread.join()
            logging.info(f"Memory usage logged to {MEMORY_LOG_FILE}")

    logging.info("Script finished.")


if __name__ == "__main__":
    main()
