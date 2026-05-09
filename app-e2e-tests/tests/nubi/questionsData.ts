export const questions: string[] = [
  "@visualizer Create a simple line chart showing monthly electricity consumption (kWh) for a household: Jan 320, Feb 300, Mar 280, Apr 260, May 240, Jun 230, Jul 250, Aug 270, Sep 290, Oct 310, Nov 330, Dec 350. X-axis: Month, Y-axis: Consumption",
  "@docs how to configure slack in nudgebee?",
  "give me the list of running pods in nudgebee namespace",
  "@tickets give me the list of 20 tickets whose status is ToDo in project Integrations",
  "show me the list of slo violation in last 24Hrs",
  "@docs give me more detail about Vertical Rightsize",
  "@tickets in nudgebee/ nudgebee, add 'all good' comment on bug 15847",
  "give me the numbers, how many anomaly detects in last 24 hrs",
  "give me the details of anomaly detects in last 24 hrs",
  "give me the list of top 3 critical CVEs",
  "@security Get the number of open CIS security issues",
  "@tickets in jira project Integrations, add all good comment on ticket number IN-582",
  "list me the top 5 recommendations for optimization",
  "investigate services server errors in last 24 hrs",
  "@gcp Which GCP projects do I have access to?",
  "investigate services server",
  "Visualize the architecture of llm-server deployment in nudgebee namespace using a diagram",

  // --- DevOps Cluster Monitoring — Cluster & Node Health ---
  "give me a quick health check — is everything in the cluster looking okay right now?",
  "anything broken or degraded across the cluster I should know about before I start my day?",
  "show me a summary — how many pods are running, how many are failing, how many are pending?",
  "are all nodes up and Ready? show me anything that's NotReady or has conditions flagged",
  "which node is the most loaded right now — CPU and memory both",

  // --- DevOps Cluster Monitoring — Workload & Pod Status ---
  "show me all pods that are not in Running state right now — crash loops, pending, evicted, whatever",
  "which deployments aren't at their desired replica count right now?",
  "my api-server pod keeps restarting — how many times has it restarted today and what's killing it?",
  "show me all pods that have been restarting more than 5 times in the last hour across the whole cluster",
  "which pods are currently in Terminating and have been stuck there for more than a few minutes?",

  // --- DevOps Cluster Monitoring — Events & Anomalies ---
  "what happened in the cluster in the last hour? show me all events sorted by time",
  "show me all Warning level events in the cluster right now — I want to see what's complaining",
  "show me all OOMKill events from the last 24 hours — which pods are getting memory killed?",
  "did anyone push a config change or deploy something in the last 2 hours? who did it?",
  "are there any recurring warning events I should be worried about — stuff that keeps repeating?",

  // --- DevOps Cluster Monitoring — Networking & Connectivity ---
  "which services currently have zero endpoints? that usually means something's broken",
  "is the ingress for nudgebee-api healthy? traffic doesn't seem to be hitting the right pods",
  "are any of our TLS certs close to expiring? I don't want a cert to catch me off guard",
  "nudgebee-api and llm-server are in different namespaces — can they talk to each other right now?",

  // --- DevOps Cluster Monitoring — Resources & Autoscaling ---
  "how much headroom do I have left on the cluster? if I need to deploy something big, is there room?",
  "which HPAs are currently active and are any of them at their max replica count already?",
  "which PVCs are close to running out of disk space?",

  // --- DevOps Cluster Monitoring — CI/CD & Deployments ---
  "what got deployed in the last 24 hours and by whom? I want a deployment log",
  "something broke after the last deploy to nudgebee namespace — can you show me what changed and when?",
  "are there any failed jobs or cronjobs in nudgebee-test namespace from the last 24 hours?",

  // --- DevOps Cluster Monitoring — Security ---
  "are there any pods running as root or with privileged: true in the cluster right now?",

  // --- Cloud Accounts — GCP ---
  "@gcp show me all running compute instances across my GCP projects right now",
  "@gcp which GCP project is consuming the most resources this month?",
  "@gcp are there any GCP services throwing errors or alerts in the last 24 hours?",
  "@gcp are there any firewall rules in my GCP projects that allow unrestricted inbound traffic?",
  "@gcp show me all GKE clusters across my GCP projects and their current health status",

  // --- Cloud Accounts — AWS ---
  "@aws show me all EC2 instances that are currently running across all regions and accounts",
  "@aws which AWS account has the highest spend this month?",
  "@aws are there any S3 buckets with public access enabled?",
  "@aws show me all RDS instances across my accounts — which ones are up and which are stopped?",
  "@aws are there any IAM users with admin access and no MFA enabled? that's a security risk",

  // --- Cloud Accounts — Azure ---
  "@azure show me all virtual machines currently running across my Azure subscriptions",
  "@azure which Azure subscription is spending the most this month?",
  "@azure are there any Azure storage accounts with public blob access turned on?",
  "@azure show me all AKS clusters across my subscriptions and their health status",
  "@azure are there any Azure resources with no tags assigned? I need everything tagged for cost attribution",
];
