#!/bin/bash
# Cron script to check for PRs requesting review
# Add to crontab: */30 * * * * /path/to/check-reviews-cron.sh

set -euo pipefail

# Configuration
REPO_PATH="${HOME}/Nudgebee/nudgebee"
LOG_FILE="${HOME}/.claude/check-reviews.log"
LAST_NOTIFIED_FILE="${HOME}/.claude/last-review-notification"

# Navigate to repo
cd "$REPO_PATH" || exit 1

# Check for PRs requesting review
PR_COUNT=$(gh pr list --search "review-requested:@me state:open draft:false" --json number --jq '. | length' 2>/dev/null || echo "0")

# Log the check
echo "$(date '+%Y-%m-%d %H:%M:%S') - Found $PR_COUNT PRs requesting review" >> "$LOG_FILE"

# If no PRs, exit quietly
if [ "$PR_COUNT" -eq 0 ]; then
    # Clear last notified file if it exists
    rm -f "$LAST_NOTIFIED_FILE"
    exit 0
fi

# Check if we already notified about these PRs
LAST_NOTIFIED_COUNT=0
if [ -f "$LAST_NOTIFIED_FILE" ]; then
    LAST_NOTIFIED_COUNT=$(cat "$LAST_NOTIFIED_FILE")
fi

# Only notify if count changed
if [ "$PR_COUNT" -ne "$LAST_NOTIFIED_COUNT" ]; then
    # Fetch PR details for notification
    PR_DETAILS=$(gh pr list --search "review-requested:@me state:open draft:false" --json number,title,author,url --limit 10)

    # Create notification message
    MESSAGE="🔔 You have $PR_COUNT PR(s) requesting your review:

$PR_DETAILS

Run '/pr-backlog' in Claude Code to review them."

    # Send macOS notification
    osascript -e "display notification \"$PR_COUNT PR(s) need your review\" with title \"GitHub Review Request\" sound name \"Glass\""

    # Log notification
    echo "$(date '+%Y-%m-%d %H:%M:%S') - Notification sent for $PR_COUNT PRs" >> "$LOG_FILE"

    # Save current count
    echo "$PR_COUNT" > "$LAST_NOTIFIED_FILE"
fi

# Trim log file if it gets too large (keep last 100 lines)
if [ -f "$LOG_FILE" ]; then
    tail -n 100 "$LOG_FILE" > "${LOG_FILE}.tmp" && mv "${LOG_FILE}.tmp" "$LOG_FILE"
fi
