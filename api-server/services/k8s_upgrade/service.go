package k8s_upgrade

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"nudgebee/services/cloud"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"regexp"
	"strings"
	"time"

	md "github.com/JohannesKaufmann/html-to-markdown/v2"
)

// Hoisted from fetchEKSReleaseNotes — avoids recompiling on every call.
var htmlH2Regex = regexp.MustCompile(`(?is)<h2[^>]*>`)

//go:embed template.json
var templateData []byte

//go:embed k8s_release_notes.json
var releaseNotesData []byte

func extractCurrentAndTargetVersion(version string) (string, string, error) {
	re := regexp.MustCompile(`^v?(\d+)\.(\d+)`)
	matches := re.FindStringSubmatch(version)
	if len(matches) < 3 {
		return "", "", fmt.Errorf("invalid version format: %s", version)
	}

	var major, minor int
	if _, err := fmt.Sscanf(matches[1]+"."+matches[2], "%d.%d", &major, &minor); err != nil {
		return "", "", fmt.Errorf("failed to parse version numbers: %w", err)
	}

	current := fmt.Sprintf("%d.%d", major, minor)
	target := fmt.Sprintf("%d.%d", major, minor+1)

	return current, target, nil
}

func CreateUpgradePlan(ctx *security.RequestContext, tenantId string, request UpgradePlanTemplate) (UpgradePlan, error) {
	if tenantId == "" {
		tenantId = request.TenantID
	}
	if tenantId == "" {
		return UpgradePlan{}, fmt.Errorf("tenant id required")
	}

	if request.AccountID == "" {
		return UpgradePlan{}, fmt.Errorf("account id required")
	}

	template := request
	if len(request.Steps) == 0 {
		if err := json.Unmarshal(templateData, &template); err != nil {
			return UpgradePlan{}, fmt.Errorf("failed to unmarshal template.json: %w", err)
		}
		// Restore request fields that json.Unmarshal may have overwritten with zero values
		template.AccountID = request.AccountID
		template.TenantID = request.TenantID
		template.Owner = request.Owner
		template.TargetVersion = request.TargetVersion
	}

	account, err := GetAccountDetails(request.AccountID)
	if err != nil {
		return UpgradePlan{}, fmt.Errorf("failed to get account details: %w", err)
	}

	if account.K8sVersion == "" {
		return UpgradePlan{}, fmt.Errorf("k8s version not available for account %s; the agent may not have reported it yet", request.AccountID)
	}

	currentVersion, defaultTarget, err := extractCurrentAndTargetVersion(account.K8sVersion)
	if err != nil {
		return UpgradePlan{}, fmt.Errorf("failed to extract current and target version: %w", err)
	}
	// C2: Support user-specified target version for multi-version upgrade plans.
	// If the request specifies a target version, use it; otherwise default to current+1.
	targetVersion := defaultTarget
	if request.TargetVersion != "" {
		targetVersion = request.TargetVersion
	}
	request.TargetVersion = targetVersion
	if request.TargetVersion != "" && len(template.Steps) > 0 {
		ctx.GetLogger().Debug("Fetching release notes", "provider", account.K8sProvider, "target_version", request.TargetVersion)
		err = fetchReleaseNotes(ctx, account.K8sProvider, request.TargetVersion, &template)
		if err != nil {
			return UpgradePlan{}, fmt.Errorf("failed to fetch release notes: %w", err)
		}

		ctx.GetLogger().Debug("Updated step 1 with release notes", "step_title", template.Steps[0].Title)

		for i := range template.Steps {
			for j := range template.Steps[i].Tasks {
				template.Steps[i].Tasks[j].Description = filterDescriptionForProvider(template.Steps[i].Tasks[j].Description, account.K8sProvider)
			}

			if template.Steps[i].Title == "Upgrade Kubernetes" {
				for j := range template.Steps[i].Tasks {
					switch template.Steps[i].Tasks[j].Title {
					case "Upgrade control plane":
						providerInstructions := generateProviderUpgradeInstructions(account.K8sProvider, targetVersion)
						template.Steps[i].Tasks[j].Description = providerInstructions
					case "Upgrade node pool":
						providerInstructions := generateProviderNodeUpgradeInstructions(account.K8sProvider, targetVersion)
						template.Steps[i].Tasks[j].Description = providerInstructions
					}
				}
			}

			if template.Steps[i].Title == "Rollback Plan" {
				for j := range template.Steps[i].Tasks {
					template.Steps[i].Tasks[j].Description = generateProviderRollbackInstructions(account.K8sProvider, currentVersion)
				}
			}
		}
	}

	template.CurrentVersion = currentVersion
	template.TargetVersion = targetVersion
	template.K8sProvider = account.K8sProvider
	err = StoreUpgradePlan(ctx, tenantId, template)
	if err != nil {
		return UpgradePlan{}, err
	}

	result := UpgradePlan{
		AccountID:      request.AccountID,
		TenantID:       tenantId,
		CurrentVersion: currentVersion,
		TargetVersion:  template.TargetVersion,
		K8sProvider:    account.K8sProvider,
		Steps:          template.Steps,
	}

	// C1: Auto-populate plan data asynchronously after creation.
	// This runs health checks via relay so the user sees ready results when they open the plan.
	go autoPopulatePlanData(request.AccountID, currentVersion, targetVersion)

	return result, nil
}

// autoPopulatePlanData runs lightweight health checks in the background after plan creation
// so the user sees pre-populated data when they open the plan (C1).
func autoPopulatePlanData(accountID, currentVersion, targetVersion string) {
	defer func() {
		if r := recover(); r != nil {
			// Log but don't crash - this is best-effort background work
			fmt.Printf("auto-populate recovered from panic for account %s: %v\n", accountID, r)
		}
	}()

	// Run each check independently so one failure doesn't block others.
	// These populate the recommendation store or are cached for future health check requests.

	// 1. API deprecation check
	if _, err := performAPIDeprecationCheck(accountID, currentVersion, targetVersion); err != nil {
		fmt.Printf("auto-populate: API deprecation check failed for %s: %v\n", accountID, err)
	}

	// 2. Helm compatibility check
	if _, err := performHelmCompatibilityCheck(accountID, targetVersion); err != nil {
		fmt.Printf("auto-populate: Helm compatibility check failed for %s: %v\n", accountID, err)
	}

	// 3. Add-on version scan
	if _, err := performAddOnVersionCheck(accountID); err != nil {
		fmt.Printf("auto-populate: add-on version check failed for %s: %v\n", accountID, err)
	}
}

func filterDescriptionForProvider(description, provider string) string {
	if description == "" {
		return description
	}

	lines := strings.Split(description, "\n")
	var filteredLines []string
	inProviderSection := false
	currentSectionProvider := ""

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		if strings.HasPrefix(trimmedLine, "**AWS EKS") {
			currentSectionProvider = "EKS"
			inProviderSection = provider == "EKS"
		} else if strings.HasPrefix(trimmedLine, "**Azure AKS") {
			currentSectionProvider = "AKS"
			inProviderSection = provider == "AKS"
		} else if strings.HasPrefix(trimmedLine, "**Google GKE") {
			currentSectionProvider = "GKE"
			inProviderSection = provider == "GKE"
		} else if strings.HasPrefix(trimmedLine, "**Self-Managed") || strings.HasPrefix(trimmedLine, "**For self-managed") {
			currentSectionProvider = "self-managed"
			inProviderSection = provider != "EKS" && provider != "AKS" && provider != "GKE"
		} else if strings.HasPrefix(trimmedLine, "**") && (strings.Contains(trimmedLine, "EKS") || strings.Contains(trimmedLine, "AKS") || strings.Contains(trimmedLine, "GKE")) {
			inProviderSection = false
		} else if strings.HasPrefix(trimmedLine, "**") && currentSectionProvider != "" && !strings.Contains(trimmedLine, "EKS") && !strings.Contains(trimmedLine, "AKS") && !strings.Contains(trimmedLine, "GKE") {
			inProviderSection = true
			currentSectionProvider = ""
		}

		if !inProviderSection && currentSectionProvider == "" {
			filteredLines = append(filteredLines, line)
		} else if inProviderSection {
			filteredLines = append(filteredLines, line)
		}
	}

	return strings.Join(filteredLines, "\n")
}

func GetUpgradePlan(ctx *security.RequestContext, tenantId string, request UpgradePlanTemplate) (UpgradePlan, error) {
	if tenantId == "" || request.AccountID == "" {
		return UpgradePlan{}, fmt.Errorf("tenant id and account id are required")
	}

	plan, err := FetchUpgradePlan(ctx, tenantId, request.AccountID)
	if err != nil {
		ctx.GetLogger().Error("Failed to fetch upgrade plan", "error", err, "account_id", request.AccountID)
		return UpgradePlan{}, fmt.Errorf("failed to fetch upgrade plan: %w", err)
	}

	plan.ProgressPercent = calculatePlanProgress(plan.Steps)
	return plan, nil
}

func GetAllUpgradePlans(ctx *security.RequestContext, tenantId string, request UpgradePlanTemplate) ([]UpgradePlan, error) {
	if tenantId == "" || request.AccountID == "" {
		return nil, fmt.Errorf("tenant id and account id are required")
	}

	plans, err := FetchAllUpgradePlans(ctx, tenantId, request.AccountID)
	if err != nil {
		ctx.GetLogger().Error("Failed to fetch all upgrade plans", "error", err, "account_id", request.AccountID)
		return nil, fmt.Errorf("failed to fetch all upgrade plans: %w", err)
	}

	for i := range plans {
		plans[i].ProgressPercent = calculatePlanProgress(plans[i].Steps)
	}

	return plans, nil
}

// calculatePlanProgress computes progress as (completed + skipped) / total * 100 (H5)
func calculatePlanProgress(steps []Step) int {
	total := 0
	completed := 0
	for _, step := range steps {
		for _, task := range step.Tasks {
			total++
			status := strings.ToLower(task.Status)
			if status == "completed" || status == "skipped" {
				completed++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return completed * 100 / total
}

// DeletePlan removes an upgrade plan and all associated steps/tasks (H6).
func DeletePlan(ctx *security.RequestContext, tenantID string, request DeleteUpgradePlanRequest) error {
	if tenantID == "" {
		return fmt.Errorf("tenant ID is required")
	}
	if request.AccountID == "" || request.PlanID == "" {
		return fmt.Errorf("account_id and plan_id are required")
	}

	return DeleteUpgradePlan(ctx, tenantID, request.AccountID, request.PlanID)
}

func fetchEKSReleaseNotes(ctx *security.RequestContext, targetVersion string) (string, error) {
	if targetVersion == "" {
		return "", fmt.Errorf("target version is required")
	}

	url := "https://docs.aws.amazon.com/eks/latest/userguide/kubernetes-versions-standard.html"
	urlEx := "https://docs.aws.amazon.com/eks/latest/userguide/kubernetes-versions-extended.html"

	// helper to fetch and extract release notes
	fetchAndExtract := func(fetchURL string) (string, error) {
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Get(fetchURL)
		if err != nil {
			return "", fmt.Errorf("failed to fetch release notes page: %w", err)
		}
		defer func(Body io.ReadCloser) {
			if Body != nil {
				if cerr := Body.Close(); cerr != nil {
					ctx.GetLogger().Error("error closing response body", "error", cerr)
				}
			}
		}(resp.Body)

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("failed to fetch release notes: status %d", resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("failed to read release notes: %w", err)
		}
		if len(body) == 0 {
			return "", fmt.Errorf("empty response body from %s", fetchURL)
		}
		html := string(body)

		if cut := strings.Index(html, "<awsdocs-copyright"); cut > 0 {
			html = html[:cut]
		}

		startRe, err := regexp.Compile(`(?is)<h2[^>]*>\s*Kubernetes\s+` + regexp.QuoteMeta(targetVersion) + `\s*</h2>`)
		if err != nil {
			return "", fmt.Errorf("failed to compile start regex: %w", err)
		}

		loc := startRe.FindStringIndex(html)
		if len(loc) < 2 {
			return "", nil // no notes in this URL
		}

		section := ""
		if loc[1] <= len(html) {
			section = html[loc[1]:]
		}
		if section == "" {
			return "", nil
		}

		endLoc := htmlH2Regex.FindStringIndex(section)
		if len(endLoc) > 0 && endLoc[0] <= len(section) {
			section = section[:endLoc[0]]
		}
		if section == "" {
			return "", nil
		}

		versionNotesMD, err := md.ConvertString(section)
		if err != nil {
			return "", fmt.Errorf("failed to convert HTML to markdown: %w", err)
		}
		if strings.TrimSpace(versionNotesMD) == "" {
			return "", nil
		}

		releaseNotes := fmt.Sprintf(
			"# AWS EKS Release Notes for Kubernetes %s\n\n"+
				"👉 [View full release notes](%s)\n\n"+
				"## Notes\n\n%s\n\n"+
				"## Key considerations\n"+
				"- Review supported AMI versions\n"+
				"- Check for any security updates\n"+
				"- Verify add-on compatibility\n"+
				"- Review breaking changes and deprecated features\n",
			targetVersion, fetchURL, versionNotesMD,
		)

		return releaseNotes, nil
	}

	if notes, err := fetchAndExtract(url); err == nil && notes != "" {
		ctx.GetLogger().Debug("Fetched EKS release notes from standard URL", "target_version", targetVersion)
		return notes, nil
	}

	if notes, err := fetchAndExtract(urlEx); err == nil && notes != "" {
		ctx.GetLogger().Debug("Fetched EKS release notes from extended URL", "target_version", targetVersion)
		return notes, nil
	}

	releaseNotes := fmt.Sprintf(
		"# AWS EKS Release Notes for Kubernetes %s\n\n"+
			"👉 [Standard release notes](%s)\n\n"+
			"👉 [Extended release notes](%s)\n\n"+
			"## Notes\n\n See the links for AWS Notes\n\n"+
			"## Key considerations\n"+
			"- Review supported AMI versions\n"+
			"- Check for any security updates\n"+
			"- Verify add-on compatibility\n"+
			"- Review breaking changes and deprecated features\n",
		targetVersion, url, urlEx,
	)
	return releaseNotes, nil
}

func generateProviderUpgradeInstructions(provider, targetVersion string) string {
	switch provider {
	case "EKS":
		return fmt.Sprintf(`# AWS EKS Control Plane Upgrade

## Prerequisites
- Ensure you have appropriate IAM permissions for EKS cluster management
- Verify addon compatibility with target version %s

## Upgrade Commands

### 1. Update Cluster Version
`+"```bash"+`
# Initiate cluster upgrade
aws eks update-cluster-version \
  --name <cluster-name> \
  --kubernetes-version %s

# The command will return an update ID - save this for monitoring
`+"```"+`

### 2. Monitor Upgrade Progress
`+"```bash"+`
# Check upgrade status (replace <update-id> with actual ID)
aws eks describe-update \
  --name <cluster-name> \
  --update-id <update-id>

# Watch upgrade progress
watch -n 30 'aws eks describe-update --name <cluster-name> --update-id <update-id> --query "update.status"'
`+"```"+`

### 3. Verify Control Plane Health
`+"```bash"+`
# Check cluster status
aws eks describe-cluster --name <cluster-name> --query "cluster.status"

# Verify API server connectivity
kubectl cluster-info

# Check cluster version
kubectl version --short
`+"```"+`

## Expected Timeline
- **Duration**: 10-15 minutes typically
- **Downtime**: Minimal (brief API server restarts)
- **Status**: Monitor until status shows "Successful"

## Important Notes
- Control plane upgrade does NOT upgrade worker nodes
- Existing workloads continue running during upgrade
- API server may have brief interruptions (< 1 minute)
- Ensure kubectl version is compatible with target version

## Next Steps After Completion
1. Verify all cluster addons are healthy
2. Update managed node groups separately
3. Test application connectivity`, targetVersion, targetVersion)

	case "GKE":
		return fmt.Sprintf(`# Google GKE Control Plane Upgrade

## Prerequisites
- Ensure you have appropriate IAM permissions (container.clusters.update)
- Verify addon compatibility with target version %s

## Upgrade Commands

### 1. Update Master Version
`+"```bash"+`
# Upgrade control plane only
gcloud container clusters upgrade <cluster-name> \
  --zone=<zone> \
  --master \
  --cluster-version=%s

# For regional clusters
gcloud container clusters upgrade <cluster-name> \
  --region=<region> \
  --master \
  --cluster-version=%s
`+"```"+`

### 2. Monitor Upgrade Progress
`+"```bash"+`
# Check operation status
gcloud container operations describe <operation-id> --zone=<zone>

# Watch cluster status
watch -n 30 'gcloud container clusters describe <cluster-name> --zone=<zone> --format="value(status)"'
`+"```"+`

### 3. Verify Control Plane Health
`+"```bash"+`
# Check cluster details
gcloud container clusters describe <cluster-name> --zone=<zone>

# Verify connectivity
kubectl cluster-info

# Check version
kubectl version --short
`+"```"+`

## Expected Timeline
- **Duration**: 5-10 minutes typically
- **Downtime**: Minimal API server restarts
- **Status**: Monitor until "RUNNING" status

## Important Notes
- Master upgrade is separate from node upgrades
- GKE automatically maintains HA during upgrade
- Applications continue running during upgrade`, targetVersion, targetVersion, targetVersion)

	case "AKS":
		return fmt.Sprintf(`# Azure AKS Control Plane Upgrade

## Prerequisites
- Ensure you have appropriate RBAC permissions (Azure Kubernetes Service Cluster Admin Role)
- Verify addon compatibility with target version %s

## Upgrade Commands

### 1. Update Control Plane Only
`+"```bash"+`
# Upgrade control plane only (recommended approach)
az aks upgrade \
  --resource-group <resource-group-name> \
  --name <cluster-name> \
  --kubernetes-version %s \
  --control-plane-only

# Check available versions first
az aks get-upgrades \
  --resource-group <resource-group-name> \
  --name <cluster-name> \
  --output table
`+"```"+`

### 2. Monitor Upgrade Progress
`+"```bash"+`
# Check upgrade status
az aks show \
  --resource-group <resource-group-name> \
  --name <cluster-name> \
  --query "kubernetesVersion"

# Monitor cluster provisioning state
watch -n 30 'az aks show --resource-group <rg-name> --name <cluster-name> --query "provisioningState"'
`+"```"+`

### 3. Verify Control Plane Health
`+"```bash"+`
# Check cluster details
az aks show \
  --resource-group <resource-group-name> \
  --name <cluster-name>

# Verify API connectivity
kubectl cluster-info
kubectl get nodes
`+"```"+`

## Expected Timeline
- **Duration**: 10-20 minutes typically
- **Downtime**: Brief API server restarts
- **Status**: Monitor until "Succeeded" provisioning state

## Important Notes
- Using --control-plane-only prevents automatic node pool upgrades
- Node pools must be upgraded separately after control plane
- Workloads remain running during control plane upgrade`, targetVersion, targetVersion)

	default:
		return fmt.Sprintf(`# Self-Managed Kubernetes Control Plane Upgrade

## Prerequisites
- Backup etcd database before proceeding
- Ensure all control plane nodes are healthy
- Verify target version %s compatibility with your current setup

## Upgrade Commands

### 1. Upgrade Control Plane Nodes (One by One)
`+"```bash"+`
# First, plan the upgrade
sudo kubeadm upgrade plan

# On the first control plane node
sudo kubeadm upgrade apply %s

# On additional control plane nodes
sudo kubeadm upgrade node

# Update kubelet and kubectl
sudo apt-get update
sudo apt-get install -y kubelet=%s-* kubectl=%s-*
sudo systemctl daemon-reload
sudo systemctl restart kubelet
`+"```"+`

### 2. Monitor Upgrade Progress
`+"```bash"+`
# Check node status
kubectl get nodes -o wide

# Verify cluster components
kubectl get componentstatuses

# Check cluster version
kubectl version --short
`+"```"+`

### 3. Verify Control Plane Health
`+"```bash"+`
# Check all pods in kube-system
kubectl get pods -n kube-system

# Verify etcd health
kubectl get pods -n kube-system -l component=etcd

# Test cluster functionality
kubectl get nodes
kubectl get pods --all-namespaces
`+"```"+`

## Expected Timeline
- **Duration**: 15-30 minutes per node
- **Downtime**: Brief interruptions during each node upgrade
- **Coordination**: Upgrade one control plane node at a time

## Important Notes
- Always upgrade control plane before worker nodes
- Maintain odd number of control plane nodes for quorum
- Test thoroughly in staging environment first
- Keep etcd backups available for rollback scenarios`, targetVersion, targetVersion, targetVersion, targetVersion)
	}
}

func generateProviderNodeUpgradeInstructions(provider, targetVersion string) string {
	switch provider {
	case "EKS":
		return fmt.Sprintf(`# AWS EKS Node Group Upgrade

## Prerequisites
- Control plane must already be upgraded to %s
- Verify that workloads are running with redundancy

## Upgrade Commands

### 1. List Node Groups
`+"```bash"+`
aws eks list-nodegroups --cluster-name <cluster-name>
`+"```"+`

### 2. Upgrade Managed Node Group
`+"```bash"+`
aws eks update-nodegroup-version \
  --cluster-name <cluster-name> \
  --nodegroup-name <node-group-name> \
  --kubernetes-version %s

# Optionally specify a new AMI type or launch template version
aws eks update-nodegroup-version \
  --cluster-name <cluster-name> \
  --nodegroup-name <node-group-name> \
  --launch-template name=<lt-name>,version=<version>
`+"```"+`

### 3. Monitor Progress
`+"```bash"+`
aws eks describe-update \
  --name <cluster-name> \
  --nodegroup-name <node-group-name> \
  --update-id <update-id>
`+"```"+`

### 4. Verify
`+"```bash"+`
kubectl get nodes -o wide
kubectl get pods -A -o wide
`+"```"+`

## Important Notes
- Rolling upgrade replaces nodes gradually
- Use PodDisruptionBudgets (PDBs) for safety
- Upgrade one node group at a time`, targetVersion, targetVersion)

	case "GKE":
		return fmt.Sprintf(`# Google GKE Node Pool Upgrade

## Prerequisites
- Control plane must be upgraded to %s
- Ensure workloads are spread across multiple nodes/pools

## Upgrade Commands

### 1. List Node Pools
`+"```bash"+`
gcloud container node-pools list --cluster <cluster-name> --zone <zone>
`+"```"+`

### 2. Upgrade Node Pool
`+"```bash"+`
gcloud container clusters upgrade <cluster-name> \
  --zone <zone> \
  --node-pool <node-pool-name> \
  --cluster-version %s

# Regional cluster example
gcloud container clusters upgrade <cluster-name> \
  --region <region> \
  --node-pool <node-pool-name> \
  --cluster-version %s
`+"```"+`

### 3. Monitor Progress
`+"```bash"+`
gcloud container operations list --zone <zone>
gcloud container operations describe <operation-id> --zone <zone>
`+"```"+`

### 4. Verify
`+"```bash"+`
kubectl get nodes
kubectl get pods -A -o wide
`+"```"+`

## Important Notes
- GKE performs rolling upgrades
- You can use `+"`--workload-pool-upgrade-strategy`"+` flags for surge vs. blue/green`, targetVersion, targetVersion, targetVersion)

	case "AKS":
		return fmt.Sprintf(`# Azure AKS Node Pool Upgrade

## Prerequisites
- Control plane already upgraded to %s
- Node pools must be upgraded individually

## Upgrade Commands

### 1. List Node Pools
`+"```bash"+`
az aks nodepool list \
  --resource-group <resource-group-name> \
  --cluster-name <cluster-name> \
  --output table
`+"```"+`

### 2. Upgrade Node Pool
`+"```bash"+`
az aks nodepool upgrade \
  --resource-group <resource-group-name> \
  --cluster-name <cluster-name> \
  --name <node-pool-name> \
  --kubernetes-version %s
`+"```"+`

### 3. Monitor Progress
`+"```bash"+`
watch -n 30 'az aks nodepool show \
  --resource-group <rg> \
  --cluster-name <cluster-name> \
  --name <node-pool-name> \
  --query "provisioningState"'
`+"```"+`

### 4. Verify
`+"```bash"+`
kubectl get nodes -o wide
kubectl get pods -A
`+"```"+`

## Important Notes
- AKS performs rolling node replacement
- Consider scaling up temporarily to maintain capacity
- System pools should be upgraded before user pools`, targetVersion, targetVersion)

	default:
		return fmt.Sprintf(`# Self-Managed Kubernetes Worker Node Upgrade

## Prerequisites
- Control plane already upgraded to %s
- Backup workloads and ensure workloads have redundancy

## Upgrade Commands

### 1. Drain Node
`+"```bash"+`
kubectl drain <node-name> --ignore-daemonsets --delete-emptydir-data
`+"```"+`

### 2. Upgrade Kubelet & Kubectl
`+"```bash"+`
# Update packages (Ubuntu/Debian example)
sudo apt-get update
sudo apt-get install -y kubelet=%s-* kubectl=%s-*
sudo systemctl daemon-reload
sudo systemctl restart kubelet
`+"```"+`

### 3. Uncordon Node
`+"```bash"+`
kubectl uncordon <node-name>
`+"```"+`

### 4. Verify
`+"```bash"+`
kubectl get nodes -o wide
`+"```"+`

## Important Notes
- Upgrade worker nodes one at a time
- Always drain workloads first
- Respect PodDisruptionBudgets to avoid downtime`, targetVersion, targetVersion, targetVersion)
	}
}

func generateProviderRollbackInstructions(provider, currentVersion string) string {
	switch provider {
	case "EKS":
		return fmt.Sprintf(`# AWS EKS Rollback Procedure

## Important: Kubernetes Control Plane Downgrades
EKS does **not** support downgrading the control plane version. The rollback strategy focuses on restoring from backups and reverting node groups.

## When to Trigger Rollback
- Critical workloads are down and cannot be recovered
- API server is unresponsive after upgrade
- Node upgrades are stuck or failing repeatedly
- Data corruption detected in etcd

## Step 1: Restore etcd from Backup
EKS manages the control plane, but if you have Velero or similar backup tools:

`+"```bash"+`
# List available backups
velero backup get

# Restore from backup
velero restore create --from-backup <backup-name>
`+"```"+`

## Step 2: Roll Back Node Groups
Create a new node group with the previous Kubernetes version and migrate workloads:

`+"```bash"+`
# Create a node group with the previous version
aws eks create-nodegroup \
  --cluster-name <cluster-name> \
  --nodegroup-name <rollback-nodegroup> \
  --kubernetes-version %s \
  --node-role <node-role-arn> \
  --subnets <subnet-ids> \
  --instance-types <instance-type> \
  --scaling-config minSize=<min>,maxSize=<max>,desiredSize=<desired>

# Wait for new node group to be active
aws eks describe-nodegroup --cluster-name <cluster-name> --nodegroup-name <rollback-nodegroup>

# Cordon upgraded nodes
kubectl cordon <upgraded-node-name>

# Drain workloads from upgraded nodes
kubectl drain <upgraded-node-name> --ignore-daemonsets --delete-emptydir-data

# Delete the upgraded node group once workloads are migrated
aws eks delete-nodegroup --cluster-name <cluster-name> --nodegroup-name <upgraded-nodegroup>
`+"```"+`

## Step 3: Verify Rollback
`+"```bash"+`
kubectl get nodes -o wide
kubectl get pods -A -o wide
kubectl get events --sort-by='.lastTimestamp' | tail -20
`+"```"+`

## Step 4: Revert Application Changes
If any manifests were modified for the new version (e.g., updated API versions), revert them:
`+"```bash"+`
# Apply previous manifests from version control
git checkout <previous-commit> -- k8s/
kubectl apply -f k8s/
`+"```"+`

## Notes
- EKS control plane cannot be downgraded — focus on node rollback and workload recovery
- If you used launch templates, reference the previous template version
- Consider creating a new EKS cluster from backup as a last resort`, currentVersion)

	case "GKE":
		return fmt.Sprintf(`# Google GKE Rollback Procedure

## Important: Kubernetes Control Plane Downgrades
GKE does **not** support downgrading the control plane. The rollback strategy focuses on node pool rollback and workload restoration.

## When to Trigger Rollback
- Critical workloads are down and cannot be recovered
- API server is unresponsive after upgrade
- Node upgrades are stuck or failing repeatedly
- Data corruption detected

## Step 1: Roll Back Node Pools
Create new node pools at the previous version and migrate workloads:

`+"```bash"+`
# Create a new node pool at the previous version
gcloud container node-pools create <rollback-pool> \
  --cluster <cluster-name> \
  --zone <zone> \
  --node-version %s \
  --num-nodes <count> \
  --machine-type <machine-type>

# Cordon upgraded node pool
for node in $(kubectl get nodes -l cloud.google.com/gke-nodepool=<upgraded-pool> -o name); do
  kubectl cordon $node
done

# Drain workloads from upgraded nodes
for node in $(kubectl get nodes -l cloud.google.com/gke-nodepool=<upgraded-pool> -o name); do
  kubectl drain $node --ignore-daemonsets --delete-emptydir-data
done

# Delete the upgraded node pool
gcloud container node-pools delete <upgraded-pool> \
  --cluster <cluster-name> \
  --zone <zone>
`+"```"+`

## Step 2: Restore from Backup (if needed)
`+"```bash"+`
# Using Velero
velero backup get
velero restore create --from-backup <backup-name>

# Using gcloud native backup
gcloud container backup-restore restores create <restore-name> \
  --project=<project> \
  --location=<region> \
  --restore-plan=<restore-plan> \
  --backup=<backup-path>
`+"```"+`

## Step 3: Verify Rollback
`+"```bash"+`
kubectl get nodes -o wide
kubectl get pods -A -o wide
gcloud container clusters describe <cluster-name> --zone <zone>
`+"```"+`

## Notes
- GKE control plane cannot be downgraded
- Use GKE Backup for GKE for cluster-level restore
- For regional clusters, replace --zone with --region`, currentVersion)

	case "AKS":
		return fmt.Sprintf(`# Azure AKS Rollback Procedure

## Important: Kubernetes Control Plane Downgrades
AKS does **not** support downgrading the control plane version. The rollback strategy focuses on node pool rollback and workload restoration.

## When to Trigger Rollback
- Critical workloads are down and cannot be recovered
- API server is unresponsive after upgrade
- Node upgrades are stuck or failing repeatedly
- Data corruption detected

## Step 1: Roll Back Node Pools
Add a new node pool at the previous version and migrate workloads:

`+"```bash"+`
# Add a new node pool at the previous version
az aks nodepool add \
  --resource-group <rg-name> \
  --cluster-name <cluster-name> \
  --name rollback \
  --kubernetes-version %s \
  --node-count <count> \
  --node-vm-size <vm-size>

# Cordon upgraded node pool nodes
kubectl cordon -l agentpool=<upgraded-pool>

# Drain workloads from upgraded nodes
for node in $(kubectl get nodes -l agentpool=<upgraded-pool> -o name); do
  kubectl drain $node --ignore-daemonsets --delete-emptydir-data
done

# Delete the upgraded node pool
az aks nodepool delete \
  --resource-group <rg-name> \
  --cluster-name <cluster-name> \
  --name <upgraded-pool>
`+"```"+`

## Step 2: Restore from Backup (if needed)
`+"```bash"+`
# Using Velero
velero backup get
velero restore create --from-backup <backup-name>

# Using Azure Backup for AKS
az dataprotection backup-instance restore initialize-for-item-recovery \
  --datasource-type AzureKubernetesService \
  --restore-location <region> \
  --source-datastore OperationalStore \
  --backup-instance-id <backup-instance-id> \
  --recovery-point-id <recovery-point-id>
`+"```"+`

## Step 3: Verify Rollback
`+"```bash"+`
kubectl get nodes -o wide
kubectl get pods -A -o wide
az aks show --resource-group <rg-name> --name <cluster-name>
`+"```"+`

## Notes
- AKS control plane cannot be downgraded
- System node pools cannot be deleted — add a new system pool first if rolling back the system pool
- Use Azure Backup for AKS for cluster-level restore`, currentVersion)

	default:
		return fmt.Sprintf(`# Self-Managed Kubernetes Rollback Procedure

## Important: Control Plane Rollback
Self-managed clusters allow etcd restore, which can effectively roll back the control plane state. However, binary downgrades can be risky and should be carefully tested.

## When to Trigger Rollback
- Critical workloads are down and cannot be recovered
- API server is unresponsive after upgrade
- Kubelet upgrades are failing across nodes
- Data corruption detected in etcd

## Step 1: Restore etcd from Snapshot
`+"```bash"+`
# Stop the API server
sudo systemctl stop kube-apiserver

# Restore etcd from pre-upgrade snapshot
ETCDCTL_API=3 etcdctl snapshot restore <snapshot-file> \
  --data-dir /var/lib/etcd-restored \
  --name <node-name> \
  --initial-cluster <node-name>=https://<node-ip>:2380 \
  --initial-advertise-peer-urls https://<node-ip>:2380

# Replace etcd data directory
sudo mv /var/lib/etcd /var/lib/etcd.bak
sudo mv /var/lib/etcd-restored /var/lib/etcd

# Restart etcd and API server
sudo systemctl restart etcd
sudo systemctl restart kube-apiserver
`+"```"+`

## Step 2: Downgrade Kubelet on Nodes
`+"```bash"+`
# On each worker node:
sudo systemctl stop kubelet

# Downgrade kubelet and kubectl (Ubuntu/Debian)
sudo apt-get install -y kubelet=%s-* kubectl=%s-*
sudo systemctl daemon-reload
sudo systemctl restart kubelet

# Verify
kubectl get nodes -o wide
`+"```"+`

## Step 3: Downgrade Control Plane Components
`+"```bash"+`
# Downgrade kubeadm
sudo apt-get install -y kubeadm=%s-*

# Apply the downgrade (use with caution)
sudo kubeadm upgrade apply v%s --force

# Restart control plane services
sudo systemctl restart kubelet
`+"```"+`

## Step 4: Verify Rollback
`+"```bash"+`
kubectl get nodes -o wide
kubectl get pods -A -o wide
kubectl get cs
kubectl get events --sort-by='.lastTimestamp' | tail -20
`+"```"+`

## Step 5: Revert Application Changes
`+"```bash"+`
# Apply previous manifests
git checkout <previous-commit> -- k8s/
kubectl apply -f k8s/
`+"```"+`

## Notes
- Always test rollback procedures in a staging environment first
- etcd restore is the most reliable rollback mechanism for self-managed clusters
- Binary downgrades (kubeadm, kubelet) may have compatibility issues — test thoroughly
- Consider having a parallel cluster ready as a failover`, currentVersion, currentVersion, currentVersion, currentVersion)
	}
}

func UpdatePlannedTask(ctx *security.RequestContext, tenantID string, request TaskUpsertRequest) (Task, error) {
	if tenantID == "" {
		return Task{}, fmt.Errorf("tenant ID is required")
	}

	if request.TaskID == "" {
		if request.StepID == "" || request.Title == "" || request.Description == "" {
			return Task{}, fmt.Errorf("step_id, title, and description are required for new tasks")
		}
		if request.Status == "" {
			request.Status = "Pending"
		}
		ctx.GetLogger().Debug("Creating new task", "step_id", request.StepID)
	}

	ctx.GetLogger().Debug("Upserting task", "task_id", request.TaskID, "step_id", request.StepID, "status", request.Status, "owner", request.Owner)

	if err := UpsertTask(ctx, tenantID, request); err != nil {
		return Task{}, fmt.Errorf("failed to upsert task: %w", err)
	}

	return Task{
		ID:          request.TaskID,
		StepID:      request.StepID,
		Sequence:    request.Sequence,
		Title:       request.Title,
		Description: request.Description,
		Status:      request.Status,
		Owner:       request.Owner,
	}, nil
}

func fetchKubernetesReleaseNotes(ctx *security.RequestContext, targetVersion string) (string, error) {
	if targetVersion == "" {
		return "", fmt.Errorf("target version is required")
	}

	var releaseData ReleaseNotesData
	if err := json.Unmarshal(releaseNotesData, &releaseData); err != nil {
		return "", fmt.Errorf("failed to unmarshal k8s_release_notes.json: %w", err)
	}

	cleanTargetVersion := strings.TrimPrefix(targetVersion, "v")
	if dotIndex := strings.Index(cleanTargetVersion, "."); dotIndex != -1 {
		if secondDotIndex := strings.Index(cleanTargetVersion[dotIndex+1:], "."); secondDotIndex != -1 {
			cleanTargetVersion = cleanTargetVersion[:dotIndex+1+secondDotIndex]
		}
	}

	for _, release := range releaseData.Releases {
		if release.Version == cleanTargetVersion {
			ctx.GetLogger().Info("Found release notes for version", "target_version", targetVersion, "matched_version", release.Version)
			return release.Description, nil
		}
	}

	// M1: Try fetching from GitHub API before falling back to static content.
	ctx.GetLogger().Info("Release notes not in embedded data, trying GitHub API", "target_version", targetVersion)
	githubNotes, err := fetchKubernetesReleaseNotesFromGitHub(cleanTargetVersion)
	if err == nil && githubNotes != "" {
		ctx.GetLogger().Info("Fetched release notes from GitHub API", "target_version", targetVersion)
		return githubNotes, nil
	}
	if err != nil {
		ctx.GetLogger().Warn("GitHub API fetch failed, using fallback", "error", err)
	}

	fallbackNotes := fmt.Sprintf(
		"# Kubernetes %s Release Notes\n\n"+
			"Detailed release notes for this version are not available.\n\n"+
			"👉 [Kubernetes GitHub Releases](https://github.com/kubernetes/kubernetes/releases/tag/v%s.0)\n"+
			"👉 [Kubernetes Blog](https://kubernetes.io/blog/)\n\n"+
			"## Notes\n\nSee the links\n\n"+
			"## Important areas to review:\n"+
			"- API changes and deprecations\n"+
			"- Breaking changes\n"+
			"- New features and enhancements\n"+
			"- Bug fixes and security updates\n"+
			"- Upgrade considerations\n\n"+
			"## Recommended actions:\n"+
			"- Review the official release notes link above\n"+
			"- Check for any deprecated APIs in your workloads\n"+
			"- Test upgrades in a staging environment first",
		targetVersion, cleanTargetVersion,
	)

	ctx.GetLogger().Warn("No Kubernetes release notes found, returning fallback",
		"target_version", targetVersion,
	)
	return fallbackNotes, nil
}

// fetchKubernetesReleaseNotesFromGitHub fetches release notes from the GitHub API (M1).
// Looks for the latest patch release of the given minor version (e.g., 1.30 → v1.30.x).
func fetchKubernetesReleaseNotesFromGitHub(minorVersion string) (string, error) {
	// First try the .0 release, which is the initial release of the minor version
	tag := fmt.Sprintf("v%s.0", minorVersion)
	url := fmt.Sprintf("https://api.github.com/repos/kubernetes/kubernetes/releases/tags/%s", tag)

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "NudgeBee-UpgradePlanner")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch from GitHub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("failed to decode GitHub response: %w", err)
	}

	if release.Body == "" {
		return "", fmt.Errorf("empty release body for %s", tag)
	}

	// Trim excessively long release notes (GitHub changelogs can be huge)
	body := release.Body
	if len(body) > 10000 {
		body = body[:10000] + "\n\n... (truncated, see full notes on GitHub)"
	}

	notes := fmt.Sprintf(
		"# Kubernetes %s Release Notes\n\n"+
			"👉 [View full release notes on GitHub](%s)\n\n"+
			"%s",
		release.TagName, release.HTMLURL, body,
	)
	return notes, nil
}

func fetchReleaseNotes(ctx *security.RequestContext, provider, targetVersion string, template *UpgradePlanTemplate) error {
	if provider == "EKS" {
		eksNotes, err := fetchEKSReleaseNotes(ctx, targetVersion)
		if err != nil {
			ctx.GetLogger().Error("Failed to fetch EKS release notes", "error", err, "target_version", targetVersion)
			return err
		}
		template.Steps[0].Tasks[0].Description = eksNotes
	} else {
		template.Steps[0].Tasks[0].Title = "Review " + provider + " Release Notes"
		template.Steps[0].Tasks[0].Description = provider + " Release Notes\n## Key considerations\n- Review supported AMI versions\n- Check for any security updates\n- Verify add-on compatibility\n- Review breaking changes and deprecated features\n"
	}

	k8sNotes, err := fetchKubernetesReleaseNotes(ctx, targetVersion)
	if err != nil {
		ctx.GetLogger().Error("Failed to fetch Kubernetes release notes", "error", err, "target_version", targetVersion)
		return err
	}
	template.Steps[0].Tasks[1].Description = k8sNotes

	return nil
}

func PerformHealthCheck(ctx *security.RequestContext, request HealthCheckRequest) (HealthCheck, error) {
	ctx.GetLogger().Info("Starting resource health check", "account_id", request.AccountID, "resource_type", request.ResourceType)

	if request.ResourceType == "" {
		return HealthCheck{}, fmt.Errorf("resource type is required")
	}

	var response HealthCheck
	response.AccountID = request.AccountID

	// M3: Namespace filtering — if namespaces are specified, use them; otherwise scan all namespaces.
	namespaces := request.Namespaces
	allNamespaces := len(namespaces) == 0

	switch request.ResourceType {
	case "nodes":
		nodes, err := performNodeHealthCheck(request.AccountID)
		if err != nil {
			return HealthCheck{}, fmt.Errorf("failed to fetch resources: %w", err)
		}
		response.Nodes = nodes
	case "workloads":
		workloads, err := performWorkloadHealthCheck(request.AccountID, namespaces, allNamespaces)
		if err != nil {
			return HealthCheck{}, fmt.Errorf("failed to fetch resources: %w", err)
		}
		response.Workloads = workloads
	case "persistentvolumes":
		pvs, err := performPersistentVolumeCheck(request.AccountID)
		if err != nil {
			return HealthCheck{}, fmt.Errorf("failed to fetch resources: %w", err)
		}
		response.PersistentVolumes = pvs
	case "services":
		services, err := performServiceHealthCheck(request.AccountID, namespaces, allNamespaces)
		if err != nil {
			return HealthCheck{}, fmt.Errorf("failed to fetch resources: %w", err)
		}
		response.Services = services
	case "load_balancer":
		loadBalancers, err := performLoadBalancerHealthCheck(ctx, request)
		if err != nil {
			return HealthCheck{}, fmt.Errorf("failed to fetch resources: %w", err)
		}
		response.LoadBalancers = loadBalancers
	case "node_groups":
		nodeConfigs, err := performNodeGroupConfigurationCheck(ctx, request)
		if err != nil {
			return HealthCheck{}, fmt.Errorf("failed to fetch resources: %w", err)
		}
		response.NodeGroups = nodeConfigs
	case "api_deprecations":
		currentVersion := request.CurrentVersion
		targetVersion := request.TargetVersion
		if currentVersion == "" || targetVersion == "" {
			// Try to get versions from account details
			account, err := GetAccountDetails(request.AccountID)
			if err != nil {
				return HealthCheck{}, fmt.Errorf("failed to get account details for version info: %w", err)
			}
			cv, tv, err := extractCurrentAndTargetVersion(account.K8sVersion)
			if err != nil {
				return HealthCheck{}, fmt.Errorf("failed to extract versions: %w", err)
			}
			if currentVersion == "" {
				currentVersion = cv
			}
			if targetVersion == "" {
				targetVersion = tv
			}
		}
		deprecations, err := performAPIDeprecationCheck(request.AccountID, currentVersion, targetVersion)
		if err != nil {
			return HealthCheck{}, fmt.Errorf("failed to check API deprecations: %w", err)
		}
		response.APIDeprecations = deprecations
	case "helm_compatibility":
		targetVersion := request.TargetVersion
		if targetVersion == "" {
			account, err := GetAccountDetails(request.AccountID)
			if err != nil {
				return HealthCheck{}, fmt.Errorf("failed to get account details for version info: %w", err)
			}
			_, tv, err := extractCurrentAndTargetVersion(account.K8sVersion)
			if err != nil {
				return HealthCheck{}, fmt.Errorf("failed to extract versions: %w", err)
			}
			targetVersion = tv
		}
		helmCompat, err := performHelmCompatibilityCheck(request.AccountID, targetVersion)
		if err != nil {
			return HealthCheck{}, fmt.Errorf("failed to check Helm compatibility: %w", err)
		}
		response.HelmCompatibility = helmCompat
	case "add_on_versions":
		addOns, err := performAddOnVersionCheck(request.AccountID)
		if err != nil {
			return HealthCheck{}, fmt.Errorf("failed to check add-on versions: %w", err)
		}
		response.AddOnVersions = addOns
	case "daemonsets":
		ds, err := performDaemonSetHealthCheck(request.AccountID, namespaces, allNamespaces)
		if err != nil {
			return HealthCheck{}, fmt.Errorf("failed to fetch daemonsets: %w", err)
		}
		response.DaemonSets = ds
	case "jobs":
		jobs, err := performJobHealthCheck(request.AccountID, namespaces, allNamespaces)
		if err != nil {
			return HealthCheck{}, fmt.Errorf("failed to fetch jobs: %w", err)
		}
		response.Jobs = jobs
	case "crds":
		crds, err := performCRDCompatibilityCheck(request.AccountID)
		if err != nil {
			return HealthCheck{}, fmt.Errorf("failed to check CRDs: %w", err)
		}
		response.CRDs = crds
	case "ingresses":
		ingresses, err := performIngressCheck(request.AccountID, namespaces, allNamespaces)
		if err != nil {
			return HealthCheck{}, fmt.Errorf("failed to check ingresses: %w", err)
		}
		response.Ingresses = ingresses
	case "network_policies":
		netpols, err := performNetworkPolicyCheck(request.AccountID, namespaces, allNamespaces)
		if err != nil {
			return HealthCheck{}, fmt.Errorf("failed to check network policies: %w", err)
		}
		response.NetworkPolicies = netpols
	default:
		return HealthCheck{}, fmt.Errorf("unsupported resource type: %s", request.ResourceType)
	}

	ctx.GetLogger().Debug("Resource health check completed",
		"account_id", request.AccountID,
		"resource_type", request.ResourceType)

	return response, nil
}

func PerformPostFlightCheck(ctx *security.RequestContext, request HealthCheckRequest) (map[string]interface{}, error) {
	ctx.GetLogger().Info("Starting post-flight health check", "account_id", request.AccountID)

	// Execute post-flight health check
	currentHealthCheck, err := DoPostFlightCheck(ctx, request.AccountID)
	if err != nil {
		ctx.GetLogger().Error("Post-flight health check failed", "error", err, "account_id", request.AccountID)
		return nil, fmt.Errorf("post-flight health check failed: %w", err)
	}

	// Build simplified response with current health check results
	healthCheckMap := map[string]interface{}{
		"nodes_count":              len(currentHealthCheck.Nodes),
		"workloads_count":          len(currentHealthCheck.Workloads),
		"services_count":           len(currentHealthCheck.Services),
		"persistent_volumes_count": len(currentHealthCheck.PersistentVolumes),
		"nodes":                    currentHealthCheck.Nodes,
		"workloads":                currentHealthCheck.Workloads,
		"services":                 currentHealthCheck.Services,
		"persistent_volumes":       currentHealthCheck.PersistentVolumes,
	}

	// H3: Include expanded checks in post-flight response
	if len(currentHealthCheck.DaemonSets) > 0 {
		healthCheckMap["daemonsets_count"] = len(currentHealthCheck.DaemonSets)
		healthCheckMap["daemonsets"] = currentHealthCheck.DaemonSets
	}
	if len(currentHealthCheck.LoadBalancers) > 0 {
		healthCheckMap["load_balancers_count"] = len(currentHealthCheck.LoadBalancers)
		healthCheckMap["load_balancers"] = currentHealthCheck.LoadBalancers
	}
	if len(currentHealthCheck.NodeGroups) > 0 {
		healthCheckMap["node_groups_count"] = len(currentHealthCheck.NodeGroups)
		healthCheckMap["node_groups"] = currentHealthCheck.NodeGroups
	}

	response := map[string]interface{}{
		"id":           fmt.Sprintf("post_flight_%d", time.Now().Unix()),
		"account_id":   request.AccountID,
		"status":       "completed",
		"health_check": healthCheckMap,
	}

	ctx.GetLogger().Info("Post-flight health check completed",
		"account_id", request.AccountID,
		"nodes_count", len(currentHealthCheck.Nodes),
		"workloads_count", len(currentHealthCheck.Workloads),
		"services_count", len(currentHealthCheck.Services))

	return response, nil
}

func ExecuteCommand(ctx *security.RequestContext, request ExecuteCommandRequest) (any, error) {
	ctx.GetLogger().Info("received execute command request", "account_id", request.AccountID, "type", request.CommandType)

	if request.CommandType == "" || request.Command == "" {
		return HealthCheck{}, fmt.Errorf("command and/or it's type is required")
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()
	var response ExecuteCommandResponse
	var commandOutput string
	var commandSuccess bool

	switch request.CommandType {
	case "kubectl":
		result, err := executeKubectlCommand(request)
		if err != nil {
			response.Success = false
			response.Error = err.Error()
			commandSuccess = false
			commandOutput = err.Error()

			if auditErr := RecordCommandExecution(ctx, tenantID, request, commandOutput, commandSuccess); auditErr != nil {
				ctx.GetLogger().Error("Failed to record command execution audit", "error", auditErr)
			}

			return nil, fmt.Errorf("failed to execute command: %w", err)
		}
		response.Success = true
		response.Output = result
		commandSuccess = true
		commandOutput = result

	case "aws":
		result, err := executeAwsCommand(ctx, request)
		if err != nil {
			response.Success = false
			response.Error = err.Error()
			commandSuccess = false
			commandOutput = err.Error()

			if auditErr := RecordCommandExecution(ctx, tenantID, request, commandOutput, commandSuccess); auditErr != nil {
				ctx.GetLogger().Error("Failed to record command execution audit", "error", auditErr)
			}

			return nil, fmt.Errorf("failed to execute command: %w", err)
		}
		response.Success = true
		response.Output = result
		commandSuccess = true
		commandOutput = result

	default:
		response.Success = false
		response.Error = fmt.Sprintf("unsupported command type: %s", request.CommandType)
		return nil, fmt.Errorf("unsupported command type: %s", request.CommandType)
	}

	if err := RecordCommandExecution(ctx, tenantID, request, commandOutput, commandSuccess); err != nil {
		ctx.GetLogger().Error("Failed to record command execution audit", "error", err)
	}

	ctx.GetLogger().Debug("completed execute command request", "account_id", request.AccountID, "type", request.CommandType)
	return response, nil
}

var kubectlAllowedCommands = map[string]bool{
	"get":          true,
	"describe":     true,
	"logs":         true,
	"top":          true,
	"version":      true,
	"cluster-info": true,
	"scale":        true,
	"patch":        true,
	"rollout":      true,
	"cordon":       true,
	"uncordon":     true,
	"drain":        true,
}

func validateAndSanitizeKubectlCommand(command string) ([]string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, fmt.Errorf("command cannot be empty")
	}

	parts := strings.Fields(command)
	if len(parts) == 0 {
		return nil, fmt.Errorf("invalid command format")
	}

	if parts[0] != "kubectl" {
		return nil, fmt.Errorf("command must start with 'kubectl', got: %s", parts[0])
	}

	if len(parts) < 2 {
		return nil, fmt.Errorf("kubectl command missing subcommand")
	}

	subcommand := parts[1]
	if !kubectlAllowedCommands[subcommand] {
		return nil, fmt.Errorf("kubectl subcommand '%s' is not allowed. Permitted commands: get, describe, logs, top, version, cluster-info, scale, patch, rollout, cordon, uncordon, drain", subcommand)
	}

	dangerousChars := []string{";", "&", "|", "`", "$", "(", ")", "<", ">", "\n", "\r"}
	for _, part := range parts {
		for _, char := range dangerousChars {
			if strings.Contains(part, char) {
				return nil, fmt.Errorf("command contains forbidden character '%s' which may indicate command injection attempt", char)
			}
		}
	}

	for i := 2; i < len(parts); i++ {
		arg := parts[i]

		if strings.HasPrefix(arg, "-") {
			if strings.Contains(arg, "=") {
				eqParts := strings.SplitN(arg, "=", 2)
				if len(eqParts) == 2 {
					for _, char := range dangerousChars {
						if strings.Contains(eqParts[1], char) {
							return nil, fmt.Errorf("flag value contains forbidden character: %s", arg)
						}
					}
				}
			}
		}
	}

	return parts, nil
}

func executeKubectlCommand(request ExecuteCommandRequest) (string, error) {
	commandParts, err := validateAndSanitizeKubectlCommand(request.Command)
	if err != nil {
		return "", fmt.Errorf("command validation failed: %w", err)
	}

	validatedCommand := strings.Join(commandParts, " ")

	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  request.AccountID,
			ActionName: "kubectl_command_executor",
			ActionParams: map[string]any{
				"command": validatedCommand,
			},
			Origin: "services-server",
		},
		NoSinks: true,
		Cache:   false,
	}

	relayResponse, _, err := relay.ExecuteAndExtractResponse(relayRequest)
	if err != nil {
		return "", fmt.Errorf("failed to execute kubectl_command_executor relay request: %w", err)
	}

	var relayData struct {
		Stdout string `json:"stdout"`
	}
	dataStr, ok := relayResponse["data"].(string)
	if !ok {
		return "", fmt.Errorf("unexpected data format: %T", relayResponse["data"])
	}
	if err := json.Unmarshal([]byte(dataStr), &relayData); err != nil {
		return "", fmt.Errorf("failed to unmarshal relay data: %w", err)
	}

	if relayData.Stdout == "" {
		return "", nil
	}

	return relayData.Stdout, nil
}

var awsAllowedCommands = map[string]map[string]bool{
	"eks": {
		"describe-cluster":        true,
		"list-clusters":           true,
		"describe-update":         true,
		"list-updates":            true,
		"describe-nodegroup":      true,
		"list-nodegroups":         true,
		"update-nodegroup-config": true,
		"get-token":               true,
		"describe-addon-versions": true,
		"describe-addon":          true,
		"list-addons":             true,
	},
}

func validateAndSanitizeAwsCommand(command string) ([]string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, fmt.Errorf("command cannot be empty")
	}

	parts := strings.Fields(command)
	if len(parts) == 0 {
		return nil, fmt.Errorf("invalid command format")
	}

	if parts[0] != "aws" {
		return nil, fmt.Errorf("command must start with 'aws', got: %s", parts[0])
	}

	if len(parts) < 3 {
		return nil, fmt.Errorf("aws command must have format: aws <service> <subcommand>")
	}

	service := parts[1]
	allowedSubcommands, serviceAllowed := awsAllowedCommands[service]
	if !serviceAllowed {
		return nil, fmt.Errorf("AWS service '%s' is not allowed. Permitted services: eks", service)
	}

	subcommand := parts[2]
	if !allowedSubcommands[subcommand] {
		allowedList := make([]string, 0, len(allowedSubcommands))
		for cmd := range allowedSubcommands {
			allowedList = append(allowedList, cmd)
		}
		return nil, fmt.Errorf("AWS %s subcommand '%s' is not allowed. Permitted commands: %v", service, subcommand, allowedList)
	}

	dangerousChars := []string{";", "&", "|", "`", "$", "(", ")", "<", ">", "\n", "\r"}
	for _, part := range parts {
		for _, char := range dangerousChars {
			if strings.Contains(part, char) {
				return nil, fmt.Errorf("command contains forbidden character '%s' which may indicate command injection attempt", char)
			}
		}
	}

	for i := 3; i < len(parts); i++ {
		arg := parts[i]

		if strings.HasPrefix(arg, "--") || strings.HasPrefix(arg, "-") {
			if strings.Contains(arg, "=") {
				eqParts := strings.SplitN(arg, "=", 2)
				if len(eqParts) == 2 {
					for _, char := range dangerousChars {
						if strings.Contains(eqParts[1], char) {
							return nil, fmt.Errorf("flag value contains forbidden character: %s", arg)
						}
					}
				}
			}
		}
	}

	return parts, nil
}

func executeAwsCommand(ctx *security.RequestContext, request ExecuteCommandRequest) (string, error) {
	commandParts, err := validateAndSanitizeAwsCommand(request.Command)
	if err != nil {
		return "", fmt.Errorf("command validation failed: %w", err)
	}

	validatedCommand := strings.Join(commandParts, " ")

	response, err := cloud.ExecuteCli(ctx, cloud.CloudExecuteCliCommandRequest{
		AccountID: request.AccountID,
		Command:   validatedCommand,
	})
	if err != nil {
		return "", err
	}

	if data, ok := response["data"].(string); ok {
		return data, nil
	}
	return "", fmt.Errorf("unexpected response format from cloud CLI")
}

// PerformPreFlightCheckWithStorage executes pre-flight health check and stores it in DB
func PerformPreFlightCheckWithStorage(ctx *security.RequestContext, request PreFlightCheckRequest) (map[string]interface{}, error) {
	ctx.GetLogger().Info("Starting pre-flight health check with storage",
		"account_id", request.AccountID,
		"plan_id", request.PlanID)

	// Execute pre-flight health check
	healthCheck, err := DoPreFlightCheck(ctx, request.AccountID)
	if err != nil {
		ctx.GetLogger().Error("Pre-flight health check failed", "error", err, "account_id", request.AccountID)
		return nil, fmt.Errorf("pre-flight health check failed: %w", err)
	}

	// Store health check with plan_id
	tenantID := ctx.GetSecurityContext().GetTenantId()
	err = StoreHealthCheckWithPlanID(ctx, &healthCheck, tenantID, request.AccountID, request.PlanID, PreFlightCheck)
	if err != nil {
		ctx.GetLogger().Error("Failed to store pre-flight health check", "error", err)
		return nil, fmt.Errorf("failed to store pre-flight health check: %w", err)
	}

	// Build response
	response := map[string]interface{}{
		"id":         fmt.Sprintf("pre_flight_%d", time.Now().Unix()),
		"account_id": request.AccountID,
		"plan_id":    request.PlanID,
		"status":     "completed",
		"health_check": map[string]interface{}{
			"nodes_count":              len(healthCheck.Nodes),
			"workloads_count":          len(healthCheck.Workloads),
			"services_count":           len(healthCheck.Services),
			"persistent_volumes_count": len(healthCheck.PersistentVolumes),
			"nodes":                    healthCheck.Nodes,
			"workloads":                healthCheck.Workloads,
			"services":                 healthCheck.Services,
			"persistent_volumes":       healthCheck.PersistentVolumes,
		},
	}

	ctx.GetLogger().Info("Pre-flight health check completed and stored",
		"account_id", request.AccountID,
		"plan_id", request.PlanID,
		"nodes_count", len(healthCheck.Nodes),
		"workloads_count", len(healthCheck.Workloads))

	return response, nil
}

// PerformPostFlightCheckWithComparison executes post-flight check, retrieves pre-flight, and compares
func PerformPostFlightCheckWithComparison(ctx *security.RequestContext, request PostFlightCheckRequest) (map[string]interface{}, error) {
	ctx.GetLogger().Info("Starting post-flight health check with comparison",
		"account_id", request.AccountID,
		"plan_id", request.PlanID)

	// Execute post-flight health check
	postHealthCheck, err := DoPostFlightCheck(ctx, request.AccountID)
	if err != nil {
		ctx.GetLogger().Error("Post-flight health check failed", "error", err, "account_id", request.AccountID)
		return nil, fmt.Errorf("post-flight health check failed: %w", err)
	}

	// Retrieve pre-flight health check from DB
	preHealthCheck, err := GetLatestHealthCheckByPlanID(ctx, request.AccountID, request.PlanID, PreFlightCheck)
	if err != nil {
		ctx.GetLogger().Warn("Failed to retrieve pre-flight health check for comparison",
			"error", err,
			"account_id", request.AccountID,
			"plan_id", request.PlanID)

		// Return post-flight results without comparison
		response := map[string]interface{}{
			"id":         fmt.Sprintf("post_flight_%d", time.Now().Unix()),
			"account_id": request.AccountID,
			"plan_id":    request.PlanID,
			"status":     "completed",
			"health_check": map[string]interface{}{
				"nodes_count":              len(postHealthCheck.Nodes),
				"workloads_count":          len(postHealthCheck.Workloads),
				"services_count":           len(postHealthCheck.Services),
				"persistent_volumes_count": len(postHealthCheck.PersistentVolumes),
				"nodes":                    postHealthCheck.Nodes,
				"workloads":                postHealthCheck.Workloads,
				"services":                 postHealthCheck.Services,
				"persistent_volumes":       postHealthCheck.PersistentVolumes,
			},
		}
		return response, nil
	}

	// Compare pre-flight and post-flight health checks
	comparison := CompareHealthCheckResults(preHealthCheck, &postHealthCheck)

	// Store post-flight health check
	tenantID := ctx.GetSecurityContext().GetTenantId()
	err = StoreHealthCheckWithPlanID(ctx, &postHealthCheck, tenantID, request.AccountID, request.PlanID, PostFlightCheck)
	if err != nil {
		ctx.GetLogger().Error("Failed to store post-flight health check", "error", err)
	}

	response := map[string]interface{}{
		"id":         fmt.Sprintf("post_flight_%d", time.Now().Unix()),
		"account_id": request.AccountID,
		"plan_id":    request.PlanID,
		"status":     "completed",
		"comparison": comparison,
		"pre_flight_summary": map[string]interface{}{
			"nodes_count":              len(preHealthCheck.Nodes),
			"workloads_count":          len(preHealthCheck.Workloads),
			"services_count":           len(preHealthCheck.Services),
			"persistent_volumes_count": len(preHealthCheck.PersistentVolumes),
		},
		"health_check": map[string]interface{}{
			"nodes_count":              len(postHealthCheck.Nodes),
			"workloads_count":          len(postHealthCheck.Workloads),
			"services_count":           len(postHealthCheck.Services),
			"persistent_volumes_count": len(postHealthCheck.PersistentVolumes),
			"nodes":                    postHealthCheck.Nodes,
			"workloads":                postHealthCheck.Workloads,
			"services":                 postHealthCheck.Services,
			"persistent_volumes":       postHealthCheck.PersistentVolumes,
		},
	}

	ctx.GetLogger().Info("Post-flight health check completed with comparison",
		"account_id", request.AccountID,
		"plan_id", request.PlanID,
		"nodes_changed", func() int {
			if nodesComp, ok := comparison["nodes_comparison"].(map[string]interface{}); ok {
				if changed, ok := nodesComp["changed"].([]map[string]interface{}); ok {
					return len(changed)
				}
			}
			return 0
		}(),
		"workloads_changed", func() int {
			if workloadsComp, ok := comparison["workloads_comparison"].(map[string]interface{}); ok {
				if changed, ok := workloadsComp["changed"].([]map[string]interface{}); ok {
					return len(changed)
				}
			}
			return 0
		}())

	return response, nil
}
