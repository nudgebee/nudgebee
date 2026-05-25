#!/bin/bash
set -euo pipefail

REGION="${AWS_REGION:-us-east-1}"
STACK_PREFIX="aws-agent-test"
WAIT_TIMEOUT="${NUKE_WAIT_TIMEOUT:-300}"  # max seconds to wait per stack (default 5 min)

# Wait for a stack to be deleted, with timeout and early exit if stack is already gone.
wait_for_stack_delete() {
    local stack_name="$1"
    local elapsed=0
    local interval=10

    while (( elapsed < WAIT_TIMEOUT )); do
        STATUS=$(aws cloudformation describe-stacks \
            --stack-name "$stack_name" \
            --region "$REGION" \
            --query 'Stacks[0].StackStatus' \
            --output text 2>/dev/null) || {
            # Stack not found — already fully deleted
            echo "    $stack_name deleted."
            return 0
        }

        if [[ "$STATUS" == "DELETE_COMPLETE" ]]; then
            echo "    $stack_name deleted."
            return 0
        elif [[ "$STATUS" == "DELETE_FAILED" ]]; then
            echo "    WARNING: $stack_name DELETE_FAILED"
            return 1
        fi

        echo "    $stack_name: $STATUS (${elapsed}s/${WAIT_TIMEOUT}s)"
        sleep "$interval"
        elapsed=$((elapsed + interval))
    done

    echo "    WARNING: $stack_name timed out after ${WAIT_TIMEOUT}s (last status: ${STATUS:-unknown})"
    return 1
}

echo "==> Finding all aws-agent-test stacks..."
STACKS=$(aws cloudformation list-stacks \
    --region "$REGION" \
    --stack-status-filter CREATE_COMPLETE UPDATE_COMPLETE ROLLBACK_COMPLETE UPDATE_ROLLBACK_COMPLETE CREATE_FAILED \
    --query "StackSummaries[?starts_with(StackName, '${STACK_PREFIX}-')].StackName" \
    --output text)

if [[ -z "$STACKS" ]]; then
    echo "    No stacks found."
    exit 0
fi

echo "Found stacks:"
echo "$STACKS" | tr '\t' '\n' | sed 's/^/    /'

read -p "Delete ALL of these? (y/N) " confirm
if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
    echo "Aborted."
    exit 0
fi

# Delete scenario stacks first, bootstrap last
BOOTSTRAP_STACK=""
for STACK in $STACKS; do
    if [[ "$STACK" == "${STACK_PREFIX}-bootstrap" ]]; then
        BOOTSTRAP_STACK="$STACK"
        continue
    fi
    echo "==> Deleting $STACK..."
    aws cloudformation delete-stack --stack-name "$STACK" --region "$REGION"
done

# Wait for scenario stacks
for STACK in $STACKS; do
    if [[ "$STACK" == "${STACK_PREFIX}-bootstrap" ]]; then
        continue
    fi
    echo "    Waiting for $STACK..."
    wait_for_stack_delete "$STACK" || true
done

# Delete bootstrap last (has the S3 bucket)
if [[ -n "$BOOTSTRAP_STACK" ]]; then
    echo "==> Emptying artifact bucket before deleting bootstrap stack..."
    BUCKET=$(aws cloudformation describe-stacks \
        --stack-name "$BOOTSTRAP_STACK" \
        --region "$REGION" \
        --query 'Stacks[0].Outputs[?OutputKey==`ArtifactBucketName`].OutputValue' \
        --output text 2>/dev/null || echo "")
    if [[ -n "$BUCKET" ]]; then
        aws s3 rm "s3://$BUCKET" --recursive || true
    fi
    echo "==> Deleting $BOOTSTRAP_STACK..."
    aws cloudformation delete-stack --stack-name "$BOOTSTRAP_STACK" --region "$REGION"
    wait_for_stack_delete "$BOOTSTRAP_STACK" || true
fi

echo "==> All stacks deleted."
