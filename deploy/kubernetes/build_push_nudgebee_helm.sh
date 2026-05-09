#!/usr/bin/env bash

set -e
set -u
set -o pipefail


cd ./nudgebee

readonly updated_version="0.62.1"
readonly AWS_REGION="us-east-1"
readonly AWS_ACCOUNT_ID="740395098545"
readonly HELM_REPO_URL="${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com"

readonly nudgebee_app_image="0.62.0-20250419081200-f34e7235f"
readonly services_server_image="2025-04-17T07-35-08_a362bab596a543923c08907723f0bd2881e6486b"
readonly auto_pilot_image="2025-04-17T07-36-43_a362bab596a543923c08907723f0bd2881e6486b"
readonly nudgebee_k8s_collector_image="2025-04-17T07-34-10_a362bab596a543923c08907723f0bd2881e6486b"
readonly relay_server_image="2025-04-17T07-38-36_a362bab596a543923c08907723f0bd2881e6486b"
readonly notification_image="2025-04-17T07-47-20_a362bab596a543923c08907723f0bd2881e6486b"
readonly ticket_server_image="2025-04-17T07-34-13_a362bab596a543923c08907723f0bd2881e6486b"
readonly ml_server_image="2025-04-24T02-15-17_67b6f92da03e90fff330c2d3d30f10613f6fad30"
readonly llm_server_image="2025-04-24T07-48-29_8ff645385558536a92a728e9cc801f38cefbb89e"
readonly rag_server_image="2025-04-22T05-50-25_387d0075ed13170e26693000c17dbf90a7218e30"
readonly hasura_migrations_image="2025-04-17T07-34-08_a362bab596a543923c08907723f0bd2881e6486b"
readonly cloud_collector_image="2025-04-08T10-12-28_ceb47e7df1d3a5b760dba2d8b2d97b530ba0dfb9"

echo "nudgebee_app_image: $nudgebee_app_image"
yq -i ".app.image.tag=\"$nudgebee_app_image\"" values.yaml

echo "services_server_image: $services_server_image"
yq -i ".services-server.image.tag=\"$services_server_image\"" values.yaml

echo "auto_pilot_image: $auto_pilot_image"
yq -i ".auto-pilot.image.tag=\"$auto_pilot_image\"" values.yaml

echo "nudgebee_k8s_collector_image: $nudgebee_k8s_collector_image"
yq -i ".k8s-collector.image.tag=\"$nudgebee_k8s_collector_image\"" values.yaml

echo "relay_server_image: $relay_server_image"
yq -i ".relay-server.image.tag=\"$relay_server_image\"" values.yaml

echo "notification_image: $notification_image"
yq -i ".notifications.image.tag=\"$notification_image\"" values.yaml

echo "ticket_server_image: $ticket_server_image"
yq -i ".ticket-server.image.tag=\"$ticket_server_image\"" values.yaml

echo "ml_server_image: $ml_server_image"
yq -i ".ml-k8s-server.image.tag=\"$ml_server_image\"" values.yaml

echo "llm_server_image: $llm_server_image"
yq -i ".llm-server.image.tag=\"$llm_server_image\"" values.yaml

echo "rag_server_image: $rag_server_image"
yq -i ".rag-server.image.tag=\"$rag_server_image\"" values.yaml

echo "hasura_migrations_image: $hasura_migrations_image"
yq -i ".postgres_migrations.image.tag=\"$hasura_migrations_image\"" values.yaml

echo "cloud_collector_image: $cloud_collector_image"
yq -i ".cloud-collector-server.image.tag=$cloud_collector_image" values.yaml


# remove internal files before packaging
rm -rf ../app/secret-*.yaml
rm -rf ../app/values-*.yaml
rm -rf ../auto-pilot/values-*.yaml
rm -rf ../hasura/values-*.yaml
rm -rf ../k8s-collector/values-*.yaml
rm -rf ../relay-server/values-*.yaml
rm -rf ../services-server/values-*.yaml
rm -rf ../ml-k8s-server/values-*.yaml
rm -rf ../ticket-server/values-*.yaml
rm -rf ../notifications/values-*.yaml
rm -rf ../llm-server/values-*.yaml
rm -rf ../rag-server/values-*.yaml
rm -rf ../cloud-collector-server/values-*.yaml

# Update chart bundles
helm package ../app -d ./charts/
helm package ../auto-pilot -d ./charts/
helm package ../hasura -d ./charts/
helm package ../k8s-collector -d ./charts/
helm package ../relay-server -d ./charts/
helm package ../services-server -d ./charts/
helm package ../ml-k8s-server -d ./charts/
helm package ../ticket-server -d ./charts/
helm package ../notifications -d ./charts/
helm package ../llm-server -d ./charts/
helm package ../rag-server -d ./charts/
helm package ../cloud-collector-server -d ./charts/


# Update chart.yaml with latest version
echo "current_version: $current_version, updated_version: $updated_version"
yq -i ".version=\"$updated_version\"" Chart.yaml
yq -i ".appVersion=\"$updated_version\"" Chart.yaml

cd ./..

# build & tag images
echo "packaging nudgebee helm chart"
helm package nudgebee

echo "doing aws/helm login"
aws ecr get-login-password --region us-east-1 | helm registry login --username AWS --password-stdin "${HELM_REPO_URL}"

echo "pushing image to helm registry"
helm push nudgebee-${updated_version}.tgz "oci://${HELM_REPO_URL}/"

echo "done!!"
