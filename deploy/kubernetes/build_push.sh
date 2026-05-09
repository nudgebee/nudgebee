cd ./nudgebee

updated_version="0.59.0"
nudgebee_app_image="0.59.0-20250403094300-2e536ae82"
services_server_image="2025-04-03T05-37-15_5484dc3bf37df012e174029e2c916c4b3339dfce"
auto_pilot_image="2025-03-20T09-08-08_47ec975b2efb3a2c863245c4c15de15619e1af70"
nudgebee_k8s_collector_image="2025-03-31T08-04-11_ea558ac45631eb3ad3bc71496c3d5ad1a6f951db"
relay_server_image="2025-03-23T16-28-12_e7c5750d69799e03bac66882c6585240ed37c5ff"
notification_image="2025-03-27T10-39-47_53c507ccd02a694b23acd7da9832737e2554b0fb"
ticket_server_image="2025-03-27T10-33-53_53c507ccd02a694b23acd7da9832737e2554b0fb"
ml_server_image="2025-03-27T10-49-05_53c507ccd02a694b23acd7da9832737e2554b0fb_arm64"
llm_server_image="2025-04-02T14-26-18_b12995946425029ec676843eed05c76820fbc948"
rag_server_image="2025-04-01T17-11-52_188af3d531227db47a96122e87d3c90694e201c9"
hasura_migrations_image="2025-04-03T09-30-34_2e536ae827fdc537858e57374d6437f3d21fd918"

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

# Update chart.yaml with latest version
echo "current_version: $current_version, updated_version: $updated_version"
yq -i ".version=\"$updated_version\"" Chart.yaml
yq -i ".appVersion=\"$updated_version\"" Chart.yaml

cd ./..
# build & tag images
helm package nudgebee
aws eks update-kubeconfig --region us-east-1 --name nudgebee
aws ecr get-login-password --region us-east-1 | helm registry login --username AWS --password-stdin 740395098545.dkr.ecr.us-east-1.amazonaws.com

# helm push nudgebee-${updated_version}.tgz oci://740395098545.dkr.ecr.us-east-1.amazonaws.com/
