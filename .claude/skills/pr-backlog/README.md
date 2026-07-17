# PR Backlog Skill

Automatically check for PRs requesting your review and review them selectively.

## Usage

### Manual Check (Recommended to start)

```bash
/pr-backlog
```

This will:
1. Fetch all open PRs where you're requested as a reviewer
2. Display them in a list
3. Let you select which PRs to review
4. Review each selected PR using the `review-pr` skill

### Quick Review All

```bash
/pr-backlog all
```

Reviews ALL PRs requesting your review without confirmation (use with caution).

## Cron Setup for Periodic Checks

To automatically check for new PR reviews every 30 minutes:

### Step 1: Make the script executable (already done)

```bash
chmod +x ~/.claude/skills/pr-backlog/check-reviews-cron.sh
```

### Step 2: Edit the script path in the cron script

Open the script and verify the `REPO_PATH` matches your repo location:

```bash
nano ~/Nudgebee/nudgebee/.claude/skills/pr-backlog/check-reviews-cron.sh
```

### Step 3: Add to crontab

```bash
crontab -e
```

Add this line (checks every 30 minutes):

```cron
*/30 * * * * /path/to/nudgebee/.claude/skills/pr-backlog/check-reviews-cron.sh >> ~/.claude/check-reviews-cron.log 2>&1
```

Or for every hour at minute 0:

```cron
0 * * * * /path/to/nudgebee/.claude/skills/pr-backlog/check-reviews-cron.sh >> ~/.claude/check-reviews-cron.log 2>&1
```

### Step 4: Verify cron is running

```bash
crontab -l
```

You should see your cron job listed.

### Step 5: Test the cron script manually

```bash
~/Nudgebee/nudgebee/.claude/skills/pr-backlog/check-reviews-cron.sh
```

If there are PRs, you should see a macOS notification.

## How It Works

### The Skill (`/pr-backlog`)
- Fetches PRs using `gh pr list --search "review-requested:@me state:open draft:false"`
- Displays them in a readable format
- Uses `AskUserQuestion` to let you select PRs
- Invokes `/review-pr` for each selected PR

### The Cron Job (`check-reviews-cron.sh`)
- Runs on a schedule (e.g., every 30 minutes)
- Checks for PRs requesting your review
- Sends a macOS notification if PRs are found
- Only notifies when the count changes (won't spam)
- Logs activity to `~/.claude/check-reviews.log`

## Logs

- **Cron output:** `~/.claude/check-reviews-cron.log`
- **Check history:** `~/.claude/check-reviews.log`
- **Last notification state:** `~/.claude/last-review-notification`

## Troubleshooting

### No notifications appearing

1. Check cron is running: `crontab -l`
2. Check cron logs: `cat ~/.claude/check-reviews-cron.log`
3. Test the script manually: `~/Nudgebee/nudgebee/.claude/skills/pr-backlog/check-reviews-cron.sh`
4. Verify `gh` CLI is authenticated: `gh auth status`

### Notifications too frequent

Increase the cron interval. Edit crontab:

```bash
crontab -e
```

Change `*/30` to `0 */2` for every 2 hours, or `0 9,17` for 9am and 5pm only.

### Stop notifications

Remove the cron job:

```bash
crontab -e
# Delete or comment out the line with check-reviews-cron.sh
```

Or disable temporarily:

```bash
crontab -l > ~/crontab-backup.txt
crontab -r  # Remove all cron jobs
# To restore later: crontab ~/crontab-backup.txt
```

## Customization

### Change notification sound

Edit `check-reviews-cron.sh` and change `sound name "Glass"` to one of:
- Basso, Blow, Bottle, Frog, Funk, Glass, Hero, Morse, Ping, Pop, Purr, Sosumi, Submarine, Tink

### Change check frequency

Edit the cron schedule:
- `*/15 * * * *` - Every 15 minutes
- `0 * * * *` - Every hour
- `0 9-17 * * 1-5` - Every hour from 9am-5pm, Monday-Friday

### Add Slack notification

Add to the script after the macOS notification:

```bash
# Send Slack notification (requires incoming webhook)
SLACK_WEBHOOK="https://hooks.slack.com/services/YOUR/WEBHOOK/URL"
curl -X POST -H 'Content-type: application/json' \
  --data "{\"text\":\"🔔 $PR_COUNT PR(s) need your review\"}" \
  "$SLACK_WEBHOOK"
```

## Integration with Other Skills

This skill works seamlessly with:
- `/review-pr <number>` - Review a specific PR
- `/pr-comments <number>` - View existing PR comments
- `/my-tickets` - Check your assigned tickets (often related to PRs)

## Example Workflow

1. **Cron job runs every 30 minutes** and checks for new review requests
2. **Notification pops up**: "3 PR(s) need your review"
3. **Open Claude Code** and run `/pr-backlog`
4. **Select PRs** from the list (or select all)
5. **PRs are reviewed** automatically using the `review-pr` skill
6. **Summary is displayed** with all reviewed PRs

## Requirements

- GitHub CLI (`gh`) installed and authenticated
- macOS (for notifications - script can be adapted for Linux)
- Git repository with proper remote setup
- Claude Code with the skill loaded
