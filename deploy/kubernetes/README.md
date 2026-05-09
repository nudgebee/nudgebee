# K8s Deployment

## Handling Secrets

Ref - https://github.com/getsops/sops

Encryption -

```
sops --encrypt --kms  arn:aws:kms:us-east-1:864186153326:key/7784cb99-51c4-437a-aa9e-f1fbc2b0024c --encrypted-regex 'PASSWORD|TOKEN|SECRET|PRIVATE|DB|DATABASE' secret-dev.yaml > secret-dev-enc.yaml
```

Decryption -

```
sops --decrypt secret-dev-enc.yaml > secret-dev.yaml
```

## Nginx Ingress

Ref - https://kubernetes.github.io/ingress-nginx/deploy/#quick-start

```
helm upgrade --install ingress-nginx ingress-nginx --repo https://kubernetes.github.io/ingress-nginx --namespace ingress-nginx --create-namespace
```

## Cert Manager

Ref - https://cert-manager.io/docs/installation/helm/

```
helm repo add jetstack https://charts.jetstack.io

helm repo update

helm install cert-manager jetstack/cert-manager --namespace cert-manager --create-namespace --version v1.11.0 --set installCRDs=true --kube-context 'arn:aws:eks:us-east-1:864186153326:cluster/nudgebee-dev'
```

## Metrics

Ref - https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/Container-Insights-setup-metrics.html

```
kubectl apply -f https://raw.githubusercontent.com/aws-samples/amazon-cloudwatch-container-insights/latest/k8s-deployment-manifest-templates/deployment-mode/daemonset/container-insights-monitoring/cwagent/cwagent-serviceaccount.yaml
```

Create SA

```
curl -O https://raw.githubusercontent.com/aws-samples/amazon-cloudwatch-container-insights/latest/k8s-deployment-manifest-templates/deployment-mode/daemonset/container-insights-monitoring/cwagent/cwagent-configmap.yaml
```

Deploy Deamon With StatsD Enabled

```
curl -O  https://raw.githubusercontent.com/aws-samples/amazon-cloudwatch-container-insights/latest/k8s-deployment-manifest-templates/deployment-mode/daemonset/container-insights-monitoring/cwagent/cwagent-daemonset.yaml
```

## Logging

Ref - https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/Container-Insights-setup-logs-FluentBit.html

```
kubectl apply -f https://raw.githubusercontent.com/aws-samples/amazon-cloudwatch-container-insights/latest/k8s-deployment-manifest-templates/deployment-mode/daemonset/container-insights-monitoring/cloudwatch-namespace.yaml
```

```
ClusterName=nudgebee-dev
RegionName=us-east-1
FluentBitHttpPort='2020'
FluentBitReadFromHead='Off'
[[ ${FluentBitReadFromHead} = 'On' ]] && FluentBitReadFromTail='Off'|| FluentBitReadFromTail='On'
[[ -z ${FluentBitHttpPort} ]] && FluentBitHttpServer='Off' || FluentBitHttpServer='On'
kubectl create configmap fluent-bit-cluster-info \
--from-literal=cluster.name=${ClusterName} \
--from-literal=http.server=${FluentBitHttpServer} \
--from-literal=http.port=${FluentBitHttpPort} \
--from-literal=read.head=${FluentBitReadFromHead} \
--from-literal=read.tail=${FluentBitReadFromTail} \
--from-literal=logs.region=${RegionName} -n amazon-cloudwatch \
--context 'arn:aws:eks:us-east-1:864186153326:cluster/nudgebee-dev'
```

```
nudgebee_k8s apply -f https://raw.githubusercontent.com/aws-samples/amazon-cloudwatch-container-insights/latest/k8s-deployment-manifest-templates/deployment-mode/daemonset/container-insights-monitoring/fluent-bit/fluent-bit.yaml
```

## App

Create common secret

```
kubectl create secret generic nudgebee --from-file=deploy/kubernetes/nudgebee.properties  --namespace nudgebee
```

### Hasura

```
helm install hasura -f hasura/values.yaml ./hasura --timeout 3600s  --namespace nudgebee --kube-context 'arn:aws:eks:us-east-1:864186153326:cluster/nudgebee-dev'

```

### Actions

```
helm install hasura-actions -f hasura-actions/values.yaml ./hasura-actions --timeout 3600s  --namespace nudgebee --kube-context 'arn:aws:eks:us-east-1:864186153326:cluster/nudgebee-dev'

```

### UI

```
helm install app -f app/values.yaml ./app --timeout 3600s  --namespace nudgebee --kube-context 'arn:aws:eks:us-east-1:864186153326:cluster/nudgebee-dev'
```

```
helm upgrade app -f app/values.yaml  ./app --timeout 3600s  --namespace nudgebee --kube-context 'arn:aws:eks:us-east-1:864186153326:cluster/nudgebee-dev'
```

## Optscale

### EtcD UI

```
nudgebee_k8s apply -f optscale/etcd-browser.yaml
```

## MongoDB

```
helm repo add mongodb https://mongodb.github.io/helm-charts
```

```
helm install community-operator mongodb/community-operator --namespace mongodb --create-namespace --context arn:aws:eks:us-east-1:864186153326:cluster/nudgebee-dev
```

```
 kubectl --namespace mongodb apply -f deploy/kubernetes/mongodb/mogodb_prometheus.yaml
```

## Karpenter

```
Ref - https://catalog.workshops.aws/eks-immersionday/en-US/autoscaling/karpenter
```

```
helm upgrade --install karpenter oci://public.ecr.aws/karpenter/karpenter --version v0.27.1 --namespace karpenter --create-namespace \
  --set serviceAccount.annotations."eks\.amazonaws\.com/role-arn"=arn:aws:iam::864186153326:role/nudgebee-dev-karpenter\
  --set settings.aws.clusterName=nudgebee-dev \
  --set settings.aws.clusterEndpoint=https://B8FE89D827D7D6E14DB21C3FB0A95CA7.gr7.us-east-1.eks.amazonaws.com \
  --set settings.aws.defaultInstanceProfile=KarpenterNodeInstanceProfilenudgebee-dev \
  --set settings.aws.interruptionQueueName=nudgebee-dev  \
  --wait
```
