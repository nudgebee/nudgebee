#!/bin/bash
set -euo pipefail

LOCATION="${AZURE_LOCATION:-eastus}"
RG_PREFIX="azure-agent-test"
BOOTSTRAP_RG="${RG_PREFIX}-bootstrap"
SUBSCRIPTION="${AZURE_SUBSCRIPTION:-19e207a9-769d-4afd-b261-10bbed2d43e8}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

# Shared App Service Plan resource group (created by --bootstrap)
SHARED_APP_PLAN_RG="$BOOTSTRAP_RG"
SHARED_APP_PLAN_NAME="shared-app-plan"

usage() {
    echo "Usage:"
    echo "  $0 --bootstrap                 # Deploy shared resources (run once first)"
    echo "  $0 <scenario> <Broken|Fixed>   # Deploy a scenario"
    echo "  $0 --delete <scenario>         # Delete a scenario resource group"
    echo "  $0 --delete-bootstrap          # Delete bootstrap resource group"
    echo "  $0 --status <scenario>         # Check deployment status"
    echo ""
    echo "Examples:"
    echo "  $0 --bootstrap"
    echo "  $0 E01 Broken"
    echo "  $0 E01 Fixed"
    echo "  $0 --delete E01"
    echo "  $0 --status E01"
    echo ""
    echo "Environment:"
    echo "  AZURE_LOCATION      (default: eastus)"
    echo "  AZURE_SUBSCRIPTION  (default: Nudgebee subscription)"
    echo ""
    echo "Notes:"
    echo "  - SQL scenarios (E03, M*) may need: AZURE_LOCATION=eastus2"
    echo "  - Always run --bootstrap before deploying any scenarios"
}

bootstrap() {
    echo "==> Deploying bootstrap shared resources..."
    echo "    Resource Group: ${BOOTSTRAP_RG}"
    echo "    Location:       ${LOCATION}"

    az group create \
        --name "$BOOTSTRAP_RG" \
        --location "$LOCATION" \
        --subscription "$SUBSCRIPTION" \
        --tags managed-by=nudgebee-benchmark \
        --output none

    az deployment group create \
        --resource-group "$BOOTSTRAP_RG" \
        --template-file "${ROOT_DIR}/bootstrap.json" \
        --name "deploy-bootstrap" \
        --subscription "$SUBSCRIPTION" \
        --output table

    echo ""
    echo "==> Bootstrap complete."
    echo "    Shared App Service Plan: ${SHARED_APP_PLAN_NAME} (in ${BOOTSTRAP_RG})"
}

delete_scenario() {
    local SCENARIO="$1"
    local RG="${RG_PREFIX}-${SCENARIO}"
    echo "==> Deleting resource group: ${RG}"
    az group delete --name "$RG" --yes --no-wait \
        --subscription "$SUBSCRIPTION" 2>&1
    echo "==> Delete initiated (background). Check: az group show --name ${RG}"
}

delete_bootstrap() {
    echo "==> Deleting bootstrap resource group: ${BOOTSTRAP_RG}"
    az group delete --name "$BOOTSTRAP_RG" --yes --no-wait \
        --subscription "$SUBSCRIPTION" 2>&1
    echo "==> Delete initiated (background)."
}

status_scenario() {
    local SCENARIO="$1"
    local RG="${RG_PREFIX}-${SCENARIO}"
    echo "==> Status for: ${RG}"
    az deployment group list \
        --resource-group "$RG" \
        --subscription "$SUBSCRIPTION" \
        --query '[0].{Name:name,State:properties.provisioningState,Timestamp:properties.timestamp}' \
        --output table 2>&1 || echo "Resource group not found or no deployments."
}

deploy_scenario() {
    local SCENARIO="$1"
    local MODE="$2"
    local RG="${RG_PREFIX}-${SCENARIO}"

    # Find template
    local TEMPLATE=""
    for dir in easy medium hard; do
        local candidate="${ROOT_DIR}/scenarios/${dir}/${SCENARIO}.json"
        if [ -f "$candidate" ]; then
            TEMPLATE="$candidate"
            break
        fi
    done

    if [ -z "$TEMPLATE" ]; then
        echo "ERROR: Template not found for scenario ${SCENARIO}"
        exit 1
    fi

    echo "==> Deploying ${SCENARIO} in ${MODE} mode..."
    echo "    Resource Group: ${RG}"
    echo "    Template:       ${TEMPLATE}"
    echo "    Location:       ${LOCATION}"

    az group create \
        --name "$RG" \
        --location "$LOCATION" \
        --subscription "$SUBSCRIPTION" \
        --tags managed-by=nudgebee-benchmark \
        --output none

    az deployment group create \
        --resource-group "$RG" \
        --template-file "$TEMPLATE" \
        --parameters scenarioMode="$MODE" sharedAppPlanRg="$SHARED_APP_PLAN_RG" \
        --name "deploy-${SCENARIO}-${MODE}" \
        --subscription "$SUBSCRIPTION" \
        --output table

    echo ""
    echo "==> Deploy complete. Outputs:"
    az deployment group show \
        --resource-group "$RG" \
        --name "deploy-${SCENARIO}-${MODE}" \
        --subscription "$SUBSCRIPTION" \
        --query 'properties.outputs' \
        --output json
}

# --- Argument parsing ---
if [ $# -lt 1 ]; then
    usage
    exit 1
fi

case "$1" in
    --bootstrap)
        bootstrap
        ;;
    --delete-bootstrap)
        delete_bootstrap
        ;;
    --delete)
        if [ $# -lt 2 ]; then echo "ERROR: Missing scenario name"; exit 1; fi
        delete_scenario "$2"
        ;;
    --status)
        if [ $# -lt 2 ]; then echo "ERROR: Missing scenario name"; exit 1; fi
        status_scenario "$2"
        ;;
    --help|-h)
        usage
        ;;
    *)
        SCENARIO="$1"
        MODE="${2:-Broken}"
        if [[ "$MODE" != "Broken" && "$MODE" != "Fixed" ]]; then
            echo "ERROR: Mode must be 'Broken' or 'Fixed' (got: $MODE)"
            exit 1
        fi
        deploy_scenario "$SCENARIO" "$MODE"
        ;;
esac
