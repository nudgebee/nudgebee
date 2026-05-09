#!/bin/bash
set -euo pipefail

REGION="${AWS_REGION:-us-east-1}"
STACK_PREFIX="aws-agent-test"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

usage() {
    echo "Usage:"
    echo "  $0 --bootstrap                    # Create shared VPC networking + S3 bucket"
    echo "  $0 <scenario> <Broken|Fixed>      # Deploy a single scenario"
    echo "  $0 --status <scenario>            # Check stack status"
    echo "  $0 --delete <scenario>            # Delete a single scenario stack"
    echo ""
    echo "Examples:"
    echo "  $0 --bootstrap"
    echo "  $0 E01 Broken"
    echo "  $0 E01 Fixed"
    echo "  $0 --status E01"
    echo "  $0 --delete E01"
    echo ""
    echo "Environment:"
    echo "  AWS_REGION    (default: us-east-1)"
}

bootstrap() {
    echo "==> Bootstrapping: creating shared VPC networking and artifact S3 bucket..."
    aws cloudformation deploy \
        --template-file "$ROOT_DIR/bootstrap.yaml" \
        --stack-name "${STACK_PREFIX}-bootstrap" \
        --region "$REGION" \
        --tags managed-by=nudgebee-benchmark \
        --no-fail-on-empty-changeset
    echo "==> Bootstrap complete."

    BUCKET=$(aws cloudformation describe-stacks \
        --stack-name "${STACK_PREFIX}-bootstrap" \
        --region "$REGION" \
        --query 'Stacks[0].Outputs[?OutputKey==`ArtifactBucketName`].OutputValue' \
        --output text)
    echo "    Artifact bucket: $BUCKET"

    VPC_ID=$(aws cloudformation describe-stacks \
        --stack-name "${STACK_PREFIX}-bootstrap" \
        --region "$REGION" \
        --query 'Stacks[0].Outputs[?OutputKey==`VpcId`].OutputValue' \
        --output text)
    echo "    Shared VPC:      $VPC_ID"
}

find_template() {
    local scenario="$1"
    # Determine difficulty from prefix
    local prefix="${scenario:0:1}"
    local dir=""
    case "$prefix" in
        E|e) dir="easy" ;;
        M|m) dir="medium" ;;
        H|h) dir="hard" ;;
        *) echo "ERROR: Scenario must start with E, M, or H (got: $scenario)" >&2; exit 1 ;;
    esac

    # Normalize to uppercase
    scenario=$(echo "$scenario" | tr '[:lower:]' '[:upper:]')

    local template="$ROOT_DIR/scenarios/$dir/$scenario.yaml"
    if [[ ! -f "$template" ]]; then
        echo "ERROR: Template not found: $template" >&2
        exit 1
    fi
    echo "$template"
}

deploy_scenario() {
    local scenario="$1"
    local mode="$2"

    scenario=$(echo "$scenario" | tr '[:lower:]' '[:upper:]')

    if [[ "$mode" != "Broken" && "$mode" != "Fixed" ]]; then
        echo "ERROR: Mode must be 'Broken' or 'Fixed' (got: $mode)" >&2
        exit 1
    fi

    local template
    template=$(find_template "$scenario")
    local stack_name="${STACK_PREFIX}-${scenario}"

    # Verify bootstrap stack exists (provides shared VPC networking)
    local bootstrap_status
    bootstrap_status=$(aws cloudformation describe-stacks \
        --stack-name "${STACK_PREFIX}-bootstrap" \
        --region "$REGION" \
        --query 'Stacks[0].StackStatus' \
        --output text 2>/dev/null || echo "DOES_NOT_EXIST")

    if [[ "$bootstrap_status" == "DOES_NOT_EXIST" ]]; then
        echo "ERROR: Bootstrap stack not found. Run '$0 --bootstrap' first." >&2
        exit 1
    fi

    echo "==> Deploying $scenario in $mode mode..."
    echo "    Template: $template"
    echo "    Stack:    $stack_name"
    echo "    Region:   $REGION"

    # Check if stack exists
    local stack_status=""
    stack_status=$(aws cloudformation describe-stacks \
        --stack-name "$stack_name" \
        --region "$REGION" \
        --query 'Stacks[0].StackStatus' \
        --output text 2>/dev/null || echo "DOES_NOT_EXIST")

    if [[ "$stack_status" == *"ROLLBACK_COMPLETE"* ]]; then
        echo "    Stack in ROLLBACK_COMPLETE state. Deleting first..."
        aws cloudformation delete-stack --stack-name "$stack_name" --region "$REGION"
        aws cloudformation wait stack-delete-complete --stack-name "$stack_name" --region "$REGION"
    fi

    aws cloudformation deploy \
        --template-file "$template" \
        --stack-name "$stack_name" \
        --region "$REGION" \
        --parameter-overrides "ScenarioMode=$mode" \
        --capabilities CAPABILITY_NAMED_IAM CAPABILITY_IAM \
        --tags managed-by=nudgebee-benchmark \
        --no-fail-on-empty-changeset

    echo "==> Deploy complete. Checking outputs..."
    aws cloudformation describe-stacks \
        --stack-name "$stack_name" \
        --region "$REGION" \
        --query 'Stacks[0].Outputs' \
        --output table 2>/dev/null || echo "    (no outputs)"
}

check_status() {
    local scenario="$1"
    scenario=$(echo "$scenario" | tr '[:lower:]' '[:upper:]')
    local stack_name="${STACK_PREFIX}-${scenario}"

    aws cloudformation describe-stacks \
        --stack-name "$stack_name" \
        --region "$REGION" \
        --query 'Stacks[0].{Status:StackStatus,Reason:StackStatusReason,Outputs:Outputs}' \
        --output table 2>/dev/null || echo "Stack does not exist: $stack_name"
}

delete_scenario() {
    local scenario="$1"
    scenario=$(echo "$scenario" | tr '[:lower:]' '[:upper:]')
    local stack_name="${STACK_PREFIX}-${scenario}"

    echo "==> Deleting stack: $stack_name"
    aws cloudformation delete-stack --stack-name "$stack_name" --region "$REGION"
    echo "    Waiting for deletion..."
    aws cloudformation wait stack-delete-complete --stack-name "$stack_name" --region "$REGION"
    echo "==> Deleted."
}

# --- Main ---
if [[ $# -lt 1 ]]; then
    usage
    exit 1
fi

case "$1" in
    --bootstrap)
        bootstrap
        ;;
    --status)
        [[ $# -lt 2 ]] && { echo "Usage: $0 --status <scenario>"; exit 1; }
        check_status "$2"
        ;;
    --delete)
        [[ $# -lt 2 ]] && { echo "Usage: $0 --delete <scenario>"; exit 1; }
        delete_scenario "$2"
        ;;
    --help|-h)
        usage
        ;;
    *)
        [[ $# -lt 2 ]] && { echo "Usage: $0 <scenario> <Broken|Fixed>"; exit 1; }
        deploy_scenario "$1" "$2"
        ;;
esac
