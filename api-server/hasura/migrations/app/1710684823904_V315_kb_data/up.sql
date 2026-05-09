
INSERT INTO knowledge_base  (description, impact, diagnosis, mitigation, rule_name) VALUES ('## Meaning

PersistentVolume is having issues with provisioning.
', '## Impact

Volue may be unavailable or have data erors (corrupted storage).

Service degradation, data loss.
', '## Diagnosis

  * Check PV events via `kubectl describe pv $PV`.
  * Check storage provider for logs.
  * Check storage quotas in the cloud.


', '## Mitigation

In happy scenario storage is just not provisioned as fast as expected. In worst scenario there is data corruption or data loss. Restore from backup.
', 'KubePersistentVolumeErrors'), ('## Meaning

Different semantic versions of Kubernetes components running. Usually happens during kubernetes cluster upgrade process.

Full context

Kubernetes control plane nodes or worker nodes use different versions. This usually happens when kubernetes cluster is upgraded between minor and major version.
', '## Impact

Incompatible API versions between kubernetes components may have very broad range of issues, influencing single containers, through app stability, ending at whole cluster stability.
', '## Diagnosis

  * Check existing kubernetes versions via `kubectl get nodes` and see VERSION column
  * Check if there is ongoing kubernetes upgrade - especially in managed services in the cloud


', '## Mitigation

  * Drain affected nodes, then upgrade or replace them with newer ones, see [Safely drain node](https://kubernetes.io/docs/tasks/administer-cluster/safely-drain-node/)

  * Ensure to set proper control plane version and node pool versions when creating clusters.

  * Ensure auto cluster updates for control plane and node pools.
  * Set proper maintenance windows for the clusters.


', 'KubeVersionMismatch'), ('## Meaning

Cluster reached allowed limits for given namespace.
', '## Impact

New app installations may not be possible.
', '## Diagnosis

  * Check resource usage for the namespace in given time span


', '## Mitigation

  * Review existing quota for given namespace and adjust it accordingly.
  * Review resources used by the quota and fine tune them.
  * Continue with standard capacity planning procedures.
  * See [Quotas](https://kubernetes.io/docs/concepts/policy/resource-quotas/)


', 'KubeQuotaFullyUsed'), ('## Meaning

Kubernetes aggregated API has reported errors. It has appeared unavailable X times averaged over the past 10m.
', '## Impact

From minor such as inability to see cluster metrics to more severe such as unable to use custom metrics to scale or even unable to use cluster.
', '## Diagnosis

  * Check networking on the node.
  * Check firewall on the node.
  * Investigate additional API logs.
  * Investigate NetworkPolicies if kubeApi - additional API was not filtered out.
  * Investigate NetworkPolicies if prometheus/additional api was not filtered out.


', '## Mitigation

TODO

See [APIServer aggregation](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/apiserver-aggregation/)
', 'KubeAggregatedAPIDown'), ('## Meaning

Cluster has overcommitted CPU resource requests for Namespaces and cannot tolerate node failure.
', '## Impact

In the event of a node failure, some Pods will be in `Pending` state due to a lack of available CPU resources.
', '## Diagnosis

  * Check if CPU resource requests are adjusted to the app usage
  * Check if some nodes are available and not cordoned
  * Check if cluster-autoscaler has issues with adding new nodes
  * Check if the given namespace usage grows in time more than expected


', '## Mitigation

  * Review existing quota for given namespace and adjust it accordingly.

  * Add more nodes to the cluster - usually it is better to have more smaller nodes, than few bigger.

  * Add different node pools with different instance types to avoid problem when using only one instance type in the cloud.

  * Use pod priorities to avoid important services from losing performance, see [pod priority and preemption](https://kubernetes.io/docs/concepts/scheduling-eviction/pod-priority-preemption/)

  * Fine tune settings for special pods used with [cluster-autoscaler](https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/FAQ.md#how-does-cluster-autoscaler-work-with-pod-priority-and-preemption)

  * Prepare performance tests for the expected workload, plan cluster capacity accordingly.



', 'KubeCPUQuotaOvercommit'), ('## Meaning

StatefulSet has not matched the expected number of replicas.

Full context

Kubernetes StatefulSet resource does not have number of replicas which were declared to be in operation. For example statefulset is expected to have 3 replicas, but it has less than that for a noticeable period of time.

In rare occasions there may be more replicas than it should and system did not clean it up. 
', '## Impact

Service degradation or unavailability.
', '## Diagnosis

  * Check statefulset via `kubectl -n $NAMESPACE describe statefulset $NAME`.
  * Check how many replicas are there declared.
  * Check the status of the pods which belong to the replica sets under the statefulset.
  * Check pod template parameters such as: 
    * pod priority - maybe it was evicted by other more importand pods
    * resources - maybe it tries to use unavailabe resource, such as GPU but there is limited number of nodes with GPU
    * affinity rules - maybe due to affinities and not enough nodes it is not possible to schedule pods
    * pod termination grace period - if too long then pods may be for too long in terminating state
  * Check if there are issues with attaching disks to statefulset - for example disk was in Zone A, but pod is scheduled in Zone B.
  * Check if Horizontal Pod Autoscaler (HPA) is not triggered due to untested values (requests values).
  * Check if cluster-autoscaler is able to create new nodes - see its logs or cluster-autoscaler status configmap.


', '## Mitigation

Depending on the conditions usually adding new nodes solves the issue.

Set proper affinity rules to schedule pods in the same zone to avoid issues with volumes.

See [Debugging Pods](https://kubernetes.io/docs/tasks/debug-application-cluster/debug-application/#debugging-pods)
', 'KubeStatefulSetReplicasMismatch'), ('## Meaning

Container in pod is in Waiting state for too long.
', '## Impact

Service degradation or unavailability.
', '## Diagnosis

  * Check pod events via `kubectl -n $NAMESPACE describe pod $POD`.
  * Check pod logs via `kubectl -n $NAMESPACE logs $POD -c $CONTAINER`
  * Check for missing files such as configmaps/secrets/volumes
  * Check for pod requests, especially special ones such as GPU.
  * Check for node taints and capabilities.


', '## Mitigation

See [Container waiting](https://kubernetes.io/docs/tasks/debug-application-cluster/debug-application/#my-pod-stays-waiting)
', 'KubeContainerWaiting'), ('## Meaning

The alert fires when a specific node is running >95% of its capacity of pods (110 by default).

Full context

Kubelets have a configuration that limits how many Pods they can run. The default value of this is 110 Pods per Kubelet, but it is configurable (and this alert takes that configuration into account with the `kube_node_status_capacity_pods` metric).
', '## Impact

Running many pods (more than 110) on a single node places a strain on the Container Runtime Interface (CRI), Container Network Interface (CNI), and the operating system itself. Approaching that limit may affect performance and availability of that node.
', '## Diagnosis

Check the number of pods on a given node by running:

`shell kubectl get pods --all-namespaces --field-selector spec.nodeName=<node> `
', '## Mitigation

Since Kubernetes only officially supports [110 pods per node](https://kubernetes.io/docs/setup/best-practices/cluster-large/), you should preferably move pods onto other nodes or expand your cluster with more worker nodes.

If you''re certain the node can handle more pods, you can raise the max pods per node limit by changing `maxPods` in your [KubeletConfiguration](https://kubernetes.io/docs/reference/config-api/kubelet-config.v1beta1/) (for kubeadm-based clusters) or changing the setting in your cloud provider''s dashboard (if supported).
', 'KubeletTooManyPods'), ('## Meaning

Deployment generation mismatch due to possible roll-back.
', '## Impact

Service degradation or unavailability.
', '## Diagnosis

See [Kubernetes Docs - Failed Deployment](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#failed-deployment)

  * Check out rollout history `kubectl -n $NAMESPACE rollout history deployment $NAME`
  * Check rollout status if it is not paused
  * Check deployment status via `kubectl -n $NAMESPACE describe deployment $NAME`.
  * Check how many replicas are there declared.
  * Investigate if new pods are not crashing.
  * Check the status of the pods which belong to the replica sets under the deployment.
  * Check pod template parameters such as: 
    * pod priority - maybe it was evicted by other more important pods
    * resources - maybe it tries to use unavailable resource, such as GPU but there is limited number of nodes with GPU
    * affinity rules - maybe due to affinities and not enough nodes it is not possible to schedule pods
    * pod termination grace period - if too long then pods may be for too long in terminating state
  * Check if Horizontal Pod Autoscaler (HPA) is not triggered due to untested values (requests values).
  * Check if cluster-autoscaler is able to create new nodes - see its logs or cluster-autoscaler status configmap.


', '## Mitigation

Depending on the conditions usually adding new nodes solves the issue.

Otherwise probably deployment or HPA definition needs to be fixed. If you can not add nodes then you can change rolling update strategy to `Recreate`. Sometimes manually deleting pod helps :)

In rare cases roll back to previous version - see [Kubernetes Docs - Rolling Back](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#rolling-back-to-a-previous-revision)

In extremely rare situations scale oldest ReplicaSets to 0 and delete them.

See [Debugging Pods](https://kubernetes.io/docs/tasks/debug-application-cluster/debug-application/#debugging-pods)
', 'KubeDeploymentGenerationMismatch'), ('## Meaning

Kube Scheduler has disappeared from Prometheus target discovery.
', '## Impact

This is a critical alert. The cluster may partially or fully non-functional.
', '## Diagnosis

To be added.
', '## Mitigation

See old CoreOS docs in [Web Archive](http://web.archive.org/web/20201026205154/https://coreos.com/tectonic/docs/latest/troubleshooting/controller-recovery.html)
', 'KubeSchedulerDown'), ('## Meaning

The apiserver has terminated over 20% of its incoming requests.
', '## Impact

Client will not be able to interact with the cluster. Some in-cluster services this may degrade or make service unavailable.
', '## Diagnosis

Use the `apiserver_flowcontrol_rejected_requests_total` metric to determine which flow schema is throttling the traffic to the API Server. The flow schema also provides information on the affected resources and subjects.
', '## Mitigation

TODO
', 'KubeAPITerminatedRequests'), ('## Meaning

The `KubeAPIDown` alert is triggered when all Kubernetes API servers have not been reachable by the monitoring system for more than 15 minutes.
', '## Impact

This is a critical alert. The Kubernetes API is not responding. The cluster may partially or fully non-functional.

Applications, which do not use kubernetes API directly, will continue to work. Changing kubernetes resources is not possible. in the cluster.

Services using Kubernetes API directly will start to behave erratically.
', '## Diagnosis

Check the status of the API server targets in the Prometheus UI.

Then, confirm whether the API is also unresponsive for you:

`shell $ kubectl cluster-info `

If you can still reach the API server, there may be a network issue between the Prometheus instances and the API server pods. Check the status of the API server pods.

`shell $ kubectl -n kube-system get pods $ kubectl -n kube-system logs -l ''component=kube-apiserver'' `

  * Check networking on the node.
  * Check firewall on the node.
  * Investigate kube proxy logs.
  * Investigate NetworkPolicies if prometheus/kubeApi was not filtered out.


', '## Mitigation

If you can still reach the API server intermittently, you may be able treat this like any other failing deployment. If not, it''s possible you may have to refer to the disaster recovery documentation.
', 'KubeAPIDown'), ('## Meaning

Pod is in CrashLoop which means the app dies or is unresponsive and kubernetes tries to restart it automatically.
', '## Impact

Service degradation or unavailability. Inability to do rolling upgrades. Certain apps will not perform required tasks such as data migrations.
', '## Diagnosis

  * Check template via `kubectl -n $NAMESPACE get pod $POD`.
  * Check pod events via `kubectl -n $NAMESPACE describe pod $POD`.
  * Check pod logs via `kubectl -n $NAMESPACE logs $POD -c $CONTAINER`
  * Check pod template parameters such as: 
    * pod priority
    * resources - maybe it tries to use unavailable resource, such as GPU but there is limited number of nodes with GPU
    * readiness and liveness probes may be incorrect - wrong port or command, check is failing too fast due to short timeout for response



Other things to check:

  * app responding extremely slow due to resource constraints such as memory too low, not enough CPU which is required on start
  * app waits for other services to start, such as database
  * misconfiguration causing app crash on start
  * missing files such as configmaps/secrets/volumes
  * read only filesystem
  * wrong user permissions in container
  * lack of special container capabilities (securityContext)
  * app is executed in different directory than expected (for example WORKDIR from Docerkfile is not used in OpenShift)


', '## Mitigation

Talk with developers or read documentation about the app, ensure to define sane default values to start the app.

See [Debugging Pods](https://kubernetes.io/docs/tasks/debug-application-cluster/debug-application/#debugging-pods)
', 'KubePodCrashLooping'), ('## Meaning

Client certificate for Kubelet on node expires soon or already expired.
', '## Impact

Node will not be able to be used within the cluster.
', '## Diagnosis

Check when certificate was issued and when it expires.
', '## Mitigation

Update certificates in the cluster control nodes and the worker nodes. Refer to the documentation of the tool used to create cluster.

Another option is to delete node if it affects only one,

In extreme situations recreate cluster.
', 'KubeletClientCertificateExpiration'), ('## Meaning

Cluster reaches to the allowed hard limits for given namespace.
', '## Impact

Inability to create resources in kubernetes.
', '## Diagnosis

  * Check resource usage for the namespace in given time span


', '## Mitigation

  * Review existing quota for given namespace and adjust it accordingly.
  * Review resources used by the quota and fine tune them.
  * Continue with standard capacity planning procedures.
  * See [Quotas](https://kubernetes.io/docs/concepts/policy/resource-quotas/)


', 'KubeQuotaExceeded'), ('
', '## Impact

The overall availability of your Kubernetes cluster isn''t guaranteed any more. There may be **too many errors** returned by the APIServer and/or **responses take too long** for guarantee proper reconciliation.

**This is always important; the only deciding factor is how urgent it is at the current rate**

Full context

This alert essentially means that a higher-than-expected percentage of the operations kube-apiserver is performing are erroring. Since random errors are inevitable, kube-apiserver has a "budget" of errors that it is allowed to make before triggering this alert.

Learn more about Multiple Burn Rate Alerts in the [SRE Workbook Chapter 5](https://sre.google/workbook/alerting-on-slos/#recommended_time_windows_and_burn_rates_f).
', '
', '
', 'KubeAPIErrorBudgetBurn'), ('## Meaning

Deployment has not matched the expected number of replicas.

Full context

Kubernetes Deployment resource does not have number of replicas which were declared to be in operation. For example deployment is expected to have 3 replicas, but it has less than that for a noticeable period of time.

In rare occasions there may be more replicas than it should and system did not clean it up. 
', '## Impact

Service degradation or unavailability.
', '## Diagnosis

  * Check deployment status via `kubectl -n $NAMESPACE describe deployment $NAME`.
  * Check how many replicas are there declared.
  * Check the status of the pods which belong to the replica sets under the deployment.
  * Check pod template parameters such as: 
    * pod priority - maybe it was evicted by other more important pods
    * resources - maybe it tries to use unavailable resource, such as GPU but there is limited number of nodes with GPU
    * affinity rules - maybe due to affinities and not enough nodes it is not possible to schedule pods
    * pod termination grace period - if too long then pods may be for too long in terminating state
  * Check if Horizontal Pod Autoscaler (HPA) is not triggered due to untested values (requests values).
  * Check if cluster-autoscaler is able to create new nodes - see its logs or cluster-autoscaler status configmap.


', '## Mitigation

Depending on the conditions usually adding new nodes solves the issue.

Otherwise probably deployment or HPA definition needs to be fixed.

See [Debugging Pods](https://kubernetes.io/docs/tasks/debug-application-cluster/debug-application/#debugging-pods)
', 'KubeDeploymentReplicasMismatch'), ('## Meaning

DaemonSet update is stuck waiting for replaced pod.
', '## Impact

Service degradation or unavailability.
', '## Diagnosis

  * Check daemonset status via `kubectl -n $NAMESPACE describe daemonset $NAME`.
  * Check [DaemonSet update strategy](https://kubernetes.io/docs/tasks/manage-daemon/update-daemon-set/)
  * Check the status of the pods which belong to the replica sets under the deployment.
  * Check pod template parameters such as: 
    * pod priority - maybe it was evicted by other more important pods
    * resources - maybe it tries to use unavailable resource, such as GPU but there is limited number of nodes with GPU
    * affinity rules - maybe due to affinities and not enough nodes it is not possible to schedule pods
    * pod termination grace period - if too long then pods may be for too long in terminating state
  * Check if Horizontal Pod Autoscaler (HPA) is not triggered due to untested values (requests values).
  * Check if cluster-autoscaler is able to create new nodes - see its logs or cluster-autoscaler status configmap.


', '## Mitigation

See [DaemonSet rolling update is stuck](https://kubernetes.io/docs/tasks/manage-daemon/update-daemon-set/#daemonset-rolling-update-is-stuck)

In some rare cases you may need to change node affinities or delete pod manually if this is special daemonset which has pod priority class system-cluster-critical and is limited to only 1 replica (so it runs on specific node only)

See [Debugging Pods](https://kubernetes.io/docs/tasks/debug-application-cluster/debug-application/#debugging-pods)
', 'KubeDaemonSetRolloutStuck'), ('## Meaning

The readiness status of node has changed few times in the last 15 minutes.
', '## Impact

The performance of the cluster deployments is affected, depending on the overall workload and the type of the node.
', '## Diagnosis

The notification details should list the node that''s not reachable. For Example:

`txt - alertname = KubeNodeUnreachable ... - node = node1.example.com ... `

Login to the cluster. Check the status of that node:

`shell $ kubectl get node $NODE -o yaml `

The output should describe why the node is not reachable.

Common failure scenarios:

  * disruptive software upgrades
  * network patitioning due to hardware failures
  * firewall rules
  * virtual machines suspended due to storage area network problems
  * system crashes / freezes due to software or hardware malfunctions


', '## Mitigation

In case of maintenance ensure to [cordon and drain node](https://kubernetes.io/docs/tasks/administer-cluster/safely-drain-node/).

In other cases ensure storage and networking redundancy if applicable.

See [KubeNode](https://kubernetes.io/docs/concepts/architecture/nodes/#condition) See [node problem detector](https://github.com/kubernetes/node-problem-detector) See [Watchdog timer](https://en.wikipedia.org/wiki/Watchdog_timer)
', 'KubeNodeReadinessFlapping'), ('## Meaning

A client certificate used to authenticate to the apiserver is expiring in less than 7 days (warning alert) or 24 hours (critical alert).
', '## Impact

Client will not be able to interact with the cluster. In cluster services communicating with Kubernetes API may degrade or become unavailable.
', '## Diagnosis

Check when certificate was issued and when it expires. Check serviceAccounts and service account tokens.
', '## Mitigation

Update client certificate.

For in-cluster clients recreate pods.
', 'KubeClientCertificateExpiration'), ('## Meaning

Kubelet Pod startup 99th percentile latency is XX seconds on node.
', '## Impact

Slow pod starts.
', '## Diagnosis

Usually exhaused IOPS for node storage.
', '## Mitigation

[Cordon and drain node](https://kubernetes.io/docs/tasks/administer-cluster/safely-drain-node/) and delete it. If issue persists look into the node logs.
', 'KubeletPodStartUpLatencyHigh'), ('## Meaning

StatefulSet update has not been rolled out.
', '## Impact

Service degradation or unavailability.
', '## Diagnosis

  * Check statefulset via `kubectl -n $NAMESPACE describe statefulset $NAME`.
  * Check if statefuls update was not paused manually (see status)
  * Check how many replicas are there declared.
  * Check the status of the pods which belong to the replica sets under the statefulset.
  * Check pod template parameters such as: 
    * pod priority - maybe it was evicted by other more importand pods
    * resources - maybe it tries to use unavailabe resource, such as GPU but there is limited number of nodes with GPU
    * affinity rules - maybe due to affinities and not enough nodes it is not possible to schedule pods
    * pod termination grace period - if too long then pods may be for too long in terminating state
  * Check if there are issues with attaching disks to statefulset - for example disk was in Zone A, but pod is scheduled in Zone B.
  * Check if Horizontal Pod Autoscaler (HPA) is not triggered due to untested values (requests values).
  * Check if cluster-autoscaler is able to create new nodes - see its logs or cluster-autoscaler status configmap.


', '## Mitigation

TODO

See [Debugging Pods](https://kubernetes.io/docs/tasks/debug-application-cluster/debug-application/#debugging-pods)
', 'KubeStatefulSetUpdateNotRolledOut'), ('## Meaning

Kubernetes node is unreachable and some workloads may be rescheduled.
', '## Impact

The performance of the cluster deployments is affected, depending on the overall workload and the type of the node.
', '## Diagnosis

The notification details should list the node that''s not reachable. For Example:

`txt - alertname = KubeNodeUnreachable ... - node = node1.example.com ... `

Login to the cluster. Check the status of that node:

`shell $ kubectl get node $NODE -o yaml `

The output should describe why the node is not reachable.

Common failure scenarios:

  * disruptive software upgrades
  * network patitioning due to hardware failures
  * firewall rules
  * virtual machines suspended due to storage area network problems
  * system crashes / freezes due to software or hardware malfunctions


', '## Mitigation

In case of maintenance ensure to [cordon and drain node](https://kubernetes.io/docs/tasks/administer-cluster/safely-drain-node/).

In other cases ensure storage and networking redundancy if applicable.

See [KubeNode](https://kubernetes.io/docs/concepts/architecture/nodes/#condition) See [node problem detector](https://github.com/kubernetes/node-problem-detector) See [Watchdog timer](https://en.wikipedia.org/wiki/Watchdog_timer)
', 'KubeNodeUnreachable'), ('## Meaning

A number of pods of daemonset are running where they are not supposed to run.
', '## Impact

Service degradation or unavailability. Excessive resource usage where they could be used by other apps.
', '## Diagnosis

Usually happens when specifying wrong pod nodeSelector/taints/affinities or node (node pools) were tainted and existing pods were not scheduled for eviction.

  * Check daemonset status via `kubectl -n $NAMESPACE describe daemonset $NAME`.
  * Check [DaemonSet update strategy](https://kubernetes.io/docs/tasks/manage-daemon/update-daemon-set/)
  * Check the status of the pods which belong to the replica sets under the deployment.
  * Check pod template parameters such as: 
    * pod priority - maybe it was evicted by other more important pods
    * affinity rules - maybe due to affinities and not enough nodes it is not possible to schedule pods
  * Check node taints and labels
  * Check logs for [node-feature-discovery](https://kubernetes-sigs.github.io/node-feature-discovery/master/get-started/index.html) and other supporting tools such as gpu-feature-discovery


', '## Mitigation

Update DaemonSet and apply change, delete pods manually.
', 'KubeDaemonSetMisScheduled'), ('## Meaning

Server certificate for Kubelet on node expires soon or already expired.
', '## Impact

**Critical** \- Cluster will be in inoperable state.
', '## Diagnosis

Check when certificate was issued and when it expires.
', '## Mitigation

Update certificates in the cluster control nodes and the worker nodes. Refer to the documentation of the tool used to create cluster.

Another option is to delete node if it affects only one,

In extreme situations recreate cluster.
', 'KubeletServerCertificateExpiration'), ('## Meaning

Horizontal Pod Autoscaler has not matched the desired number of replicas for longer than 15 minutes.
', '## Impact

HPA was unable to schedule desired number of pods.
', '## Diagnosis

Check why HPA was unable to scale:

  * not enough nodes in the cluster
  * hitting resource quotas in the cluster
  * pods evicted due to pod priority


', '## Mitigation

In case of cluster-autoscaler you may need to set up preemtive pod pools to ensure nodes are created on time.
', 'KubeHpaReplicasMismatch'), ('## Meaning

StatefulSet generation mismatch due to possible roll-back.
', '## Impact

Service degradation or unavailability.
', '## Diagnosis

See [Kubernetes Docs - Failed Deployment](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#failed-deployment) which can be also applied to StatefulSets to some extent

  * Check out rollout history `kubectl -n $NAMESPACE rollout history statefulset $NAME`
  * Check rollout status if it is not paused
  * Check deployment status via `kubectl -n $NAMESPACE describe statefulset $NAME`.
  * Check how many replicas are there declared.
  * Investigate if new pods are not crashing.
  * Look at the issues with PersistentVolumes attached to StatefulSets.
  * Check the status of the pods which belong to the replica sets under the deployment.
  * Check pod template parameters such as: 
    * pod priority - maybe it was evicted by other more important pods
    * resources - maybe it tries to use unavailable resource, such as GPU but there is limited number of nodes with GPU
    * affinity rules - maybe due to affinities and not enough nodes it is not possible to schedule pods
    * pod termination grace period - if too long then pods may be for too long in terminating state
  * Check if Horizontal Pod Autoscaler (HPA) is not triggered due to untested values (requests values).
  * Check if cluster-autoscaler is able to create new nodes - see its logs or cluster-autoscaler status configmap.


', '## Mitigation

Statefulsets are quite specific, and usually have special scripts on pod termination. See if there are special commands executed such as data migration, which may significantly slow down the progress.

In case of scale out usually adding new nodes solves the issue.

Otherwise probably statefulset definition needs to be fixed.

In rare cases roll back to previous version - see [Kubernetes Docs - Rolling Back](https://kubernetes.io/docs/concepts/workloads/controllers/statefulset/#rolling-updates)

In extremely rare situations it may be better to delete problematic pods.

See [Debugging Pods](https://kubernetes.io/docs/tasks/debug-application-cluster/debug-application/#debugging-pods)
', 'KubeStatefulSetGenerationMismatch'), ('## Meaning

A number of pods of daemonset are not scheduled.
', '## Impact

Service degradation or unavailability.
', '## Diagnosis

Usually happens when specifying wrong pod taints/affinities or lack of resources on the nodes.

  * Check daemonset status via `kubectl -n $NAMESPACE describe daemonset $NAME`.
  * Check [DaemonSet update strategy](https://kubernetes.io/docs/tasks/manage-daemon/update-daemon-set/)
  * Check the status of the pods which belong to the replica sets under the deployment.
  * Check pod template parameters such as: 
    * pod priority - maybe it was evicted by other more important pods
    * resources - maybe it tries to use unavailable resource, such as GPU but there is limited number of nodes with GPU
    * affinity rules - maybe due to affinities and not enough nodes it is not possible to schedule pods
  * Check if Horizontal Pod Autoscaler (HPA) is not triggered due to untested values (requests values).
  * Check if cluster-autoscaler is able to create new nodes - see its logs or cluster-autoscaler status configmap.


', '## Mitigation

Set proper priority class for important dameonsets to system-node-critical.

See [DaemonSet rolling update is stuck](https://kubernetes.io/docs/tasks/manage-daemon/update-daemon-set/#daemonset-rolling-update-is-stuck)

In some rare cases you may need to change node affinities or delete pod manually if this is special daemonset which has specific pod priority class and is limited to only 1 replica (so it runs on specific node only)

See [Debugging Pods](https://kubernetes.io/docs/tasks/debug-application-cluster/debug-application/#debugging-pods)
', 'KubeDaemonSetNotScheduled'), ('## Meaning

KubeControllerManager has disappeared from Prometheus target discovery.
', '## Impact

The cluster is not functional and Kubernetes resources cannot be reconciled.

Full context

More about kube-controller-manager function can be found at https://kubernetes.io/docs/reference/command-line-tools-reference/kube-controller-manager/
', '## Diagnosis

TODO
', '## Mitigation

See old CoreOS docs in [Web Archive](http://web.archive.org/web/20201026205154/https://coreos.com/tectonic/docs/latest/troubleshooting/controller-recovery.html)
', 'KubeControllerManagerDown'), ('## Meaning

There can be various reasons why a volume is filling up. This runbook does not cover application specific reasons, only mitigations for volumes that are legitimately filling.

As always refer to recommended scenarios for given service.
', '## Impact

Service degradation, switching to read only mode.
', '## Diagnosis

Check app usage in time. Check if there are any configurations such as snapshotting, automatic data retention.
', '## Mitigation

### Data retention

Deleting no longer needed data is the fastest and the cheapest solution.

Ask the service owner if specific old data can be deleted. Enable data retention especially for snapshots, if possible.

### Data export

If data is not needed in the service but needs to be processed later then send it to somewhere else, for example to S3 bucket.

### Data rebalance in the cluster

Some services automatically rebalance data on the cluster when one node fills up. Some allow to rebalance data across existing nodes, the other may require adding new nodes. If this is supported then increase number of replicas and wait for data migration or trigger it manually.

Example services that support this:

  * cassandra
  * ceph
  * elasticsearch/opensearch
  * gluster
  * hadoop
  * kafka
  * minio



**Notice** : some services may require special scaling conditions such as adding twice more nodes than exist now.

### Direct Volume resizing

If volume resizing is available, it''s easiest to increase the capacity of the volume.

To check if volume expansion is available, run this with your namespace and PVC-name replaced.

`shell $ kubectl get storageclass `kubectl -n <my-namespace> get pvc <my-pvc> -ojson | jq -r ''.spec.storageClassName''` NAME PROVISIONER RECLAIMPOLICY VOLUMEBINDINGMODE ALLOWVOLUMEEXPANSION AGE standard (default) kubernetes.io/gce-pd Delete Immediate true 28d `

In this case `ALLOWVOLUMEEXPANSION` is true, so we can make use of the feature.

To resize the volume run:

`shell $ kubectl -n <my-namespace> edit pvc <my-pvc> `

And edit `.spec.resources.requests.storage` to the new desired storage size. Eventually the PVC status will say "Waiting for user to (re-)start a pod to finish file system resize of volume on node."

You can check this with:

`shell $ kubectl -n <my-namespace> get pvc <my-pvc> `

Once the PVC status says to restart the respective pod, run this to restart it (this automatically finds the pod that mounts the PVC and deletes it, if you know the pod name, you can also just simply delete that pod):

`shell $ kubectl -n <my-namespace> delete pod `kubectl -n <my-namespace> get pod -ojson | jq -r ''.items[] | select(.spec.volumes[] .persistentVolumeClaim.claimName=="<my-pvc>") | .metadata.name''` `

### Migrate data to a new, larger volume

When resizing is not available and the data is not safe to be deleted, then the only way is to create a larger volume and migrate the data.

TODO

### Purge volume

When the data is ephemeral and volume expansion is not available, it may be best to purge the volume.

**WARNING/DANGER** : This will permanently delete the data on the volume. Performing these steps is your responsibility.

TODO

### Migrate data to new, larger instance pool in the same cluster

In very specific scenarios it is better to schedule data migration in the same cluster but to a new instances. This is sometimes hard to accomplish due to the way how certain resources are managed in kubernetes.

In general procedure is like this:

  * add new nodes with bigger capacity than existing cluster
  * trigger data migration
  * scale in to 0 old instance pool and after that delete it.



### Migrate data to new, larger cluster

This is most common scenario, but is much more expensive and may be a bit time consuming. Also sometimes this causes split brain issues when writing.

In general procedure is like this, this is only a suggestion, though:

  * create data snapshot on existing cluster
  * add new cluster with bigger capacity than existing cluster
  * start data restore on new cluster based on the snapshot
  * switch old cluster to read only mode
  * reconfigure networking to point to new cluster
  * trigger data migration from old cluster to new cluster to sync difference between snapshot and latest writes
  * remove old cluster


', 'KubePersistentVolumeFillingUp'), ('## Meaning

Pod has been in a non-ready state for more than 15 minutes.

State Running but not ready means readiness probe fails. State Pending means pod can not be created for specific namespace and node.

Full context

Pod failed to reach reay state, depending on the readiness/liveness probes. See [pod-lifecycle](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/)
', '## Impact

Service degradation or unavailability. Pod not attached to service, thus not getting any traffic.
', '## Diagnosis

  * Check template via `kubectl -n $NAMESPACE get pod $POD`.
  * Check pod events via `kubectl -n $NAMESPACE describe pod $POD`.
  * Check pod logs via `kubectl -n $NAMESPACE logs $POD -c $CONTAINER`
  * Check pod template parameters such as: 
    * pod priority
    * resources - maybe it tries to use unavailable resource, such as GPU but there is limited number of nodes with GPU
    * readiness and liveness probes may be incorrect - wrong port or command, check is failing too fast due to short timeout for response
    * stuck or long running init containers



Other things to check:

  * app responding extremely slow due to resource constraints such as memory too low, not enough CPU which is required on start
  * app waits for other services to start, such as database
  * misconfiguration causing app crash on start
  * missing files such as configmaps/secrets/volumes
  * read only filesystem
  * wrong user permissions in container
  * lack of special container capabilities (securityContext)
  * app is executed in different directory than expected (for example WORKDIR from Docerkfile is not used in OpenShift)


', '## Mitigation

Talk with developers or read documentation about the app, ensure to define sane default values to start the app.

See [Debugging Pods](https://kubernetes.io/docs/tasks/debug-application-cluster/debug-application/#debugging-pods)
', 'KubePodNotReady'), ('## Meaning

KubeNodeNotReady alert is fired when a Kubernetes node is not in `Ready` state for a certain period. In this case, the node is not able to host any new pods as described [here][KubeNode].
', '## Impact

The performance of the cluster deployments is affected, depending on the overall workload and the type of the node.
', '## Diagnosis

The notification details should list the node that''s not ready. For Example:

`txt - alertname = KubeNodeNotReady ... - node = node1.example.com ... `

Login to the cluster. Check the status of that node:

`shell $ kubectl get node $NODE -o yaml `

The output should describe why the node isn''t ready (e.g.: timeouts reaching the API or kubelet).
', '## Mitigation

Once, the problem was resolved that prevented node from being replaced, the instance should be terminated.

See [KubeNode](https://kubernetes.io/docs/concepts/architecture/nodes/#condition) See [node problem detector](https://github.com/kubernetes/node-problem-detector)
', 'KubeNodeNotReady'), ('## Meaning

Cluster has overcommitted memory resource requests for Namespaces.
', '## Impact

Various services degradation or unavailability in case of single node failure.
', '## Diagnosis

  * Check if Memory resource requests are adjusted to the app usage
  * Check if some nodes are available and not cordoned
  * Check if cluster-autoscaler has issues with adding new nodes
  * Check if the given namespace usage grows in time more than expected


', '## Mitigation

  * Review existing quota for given namespace and adjust it accordingly.

  * Add more nodes to the cluster - usually it is better to have more smaller nodes, than few bigger.

  * Add different node pools with different instance types to avoid problem when using only one instance type in the cloud.

  * Use pod priorities to avoid important services from losing performance, see [pod priority and preemption](https://kubernetes.io/docs/concepts/scheduling-eviction/pod-priority-preemption/)

  * Fine tune settings for special pods used with [cluster-autoscaler](https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/FAQ.md#how-does-cluster-autoscaler-work-with-pod-priority-and-preemption)

  * Prepare performance tests for the expected workload, plan cluster capacity accordingly.



', 'KubeMemoryQuotaOvercommit'), ('## Meaning

Cluster reaches to the allowed limits for given namespace.
', '## Impact

In the future deployments may not be possbile.
', '## Diagnosis

  * Check resource usage for the namespace in given time span


', '## Mitigation

  * Review existing quota for given namespace and adjust it accordingly.
  * Review resources used by the quota and fine tune them.
  * Continue with standard capacity planning procedures.
  * See [Quotas](https://kubernetes.io/docs/concepts/policy/resource-quotas/)


', 'KubeQuotaAlmostFull'), ('## Meaning

Job failed complete.
', '## Impact

Failure of processing of a scheduled task.
', '## Diagnosis

  * Check job via `kubectl -n $NAMESPACE describe jobs $JOB`.
  * Check pod events via `kubectl -n $NAMESPACE describe pod $POD_FROM_JOB`.
  * Check pod logs via `kubectl -n $NAMESPACE log pod $POD_FROM_JOB`.


', '## Mitigation

  * See [Debugging Pods](https://kubernetes.io/docs/tasks/debug-application-cluster/debug-application/#debugging-pods)
  * See [Job patterns](https://kubernetes.io/docs/tasks/job/)
  * redesign job so that it is idempotent (can be re-run many times which will always produce the same output even if input differs)


', 'KubeJobFailed'), ('## Meaning

Cluster has overcommitted CPU resource requests for Pods and cannot tolerate node failure.

Full context

Total number of CPU requests for pods exceeds cluster capacity. In case of node failure some pods will not fit in the remaining nodes.
', '## Impact

The cluster cannot tolerate node failure. In the event of a node failure, some Pods will be in `Pending` state.
', '## Diagnosis

  * Check if CPU resource requests are adjusted to the app usage
  * Check if some nodes are available and not cordoned
  * Check if cluster-autoscaler has issues with adding new nodes


', '## Mitigation

  * Add more nodes to the cluster - usually it is better to have more smaller nodes, than few bigger.

  * Add different node pools with different instance types to avoid problem when using only one instance type in the cloud.

  * Use pod priorities to avoid important services from losing performance, see [pod priority and preemption](https://kubernetes.io/docs/concepts/scheduling-eviction/pod-priority-preemption/)

  * Fine tune settings for special pods used with [cluster-autoscaler](https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/FAQ.md#how-does-cluster-autoscaler-work-with-pod-priority-and-preemption)

  * Prepare performance tests for the expected workload, plan cluster capacity accordingly.



', 'KubeCPUOvercommit'), ('## Meaning

Kubelet on node has failed to renew its server certificate (XX errors in the last 5 minutes)
', '## Impact

**Critical** \- Cluster will be in inoperable state.
', '## Diagnosis

Check when certificate was issued and when it expires.
', '## Mitigation

Update certificates in the cluster control nodes and the worker nodes. Refer to the documentation of the tool used to create cluster.

Another option is to delete node if it affects only one,

In extreme situations recreate cluster.
', 'KubeletServerCertificateRenewalErrors'), ('## Meaning

This alert is triggered when the monitoring system has not been able to reach any of the cluster''s Kubelets for more than 15 minutes.
', '## Impact

This alert represents a critical threat to the cluster''s stability. Excluding the possibility of a network issue preventing the monitoring system from scraping Kubelet metrics, multiple nodes in the cluster are likely unable to respond to configuration changes for pods and other resources, and some debugging tools are likely not functional, e.g. `kubectl exec` and `kubectl logs`.
', '## Diagnosis

Check the status of nodes and for recent events on `Node` objects, or for recent events in general:

`shell $ kubectl get nodes $ kubectl describe node $NODE_NAME $ kubectl get events --field-selector ''involvedObject.kind=Node'' $ kubectl get events `

If you have SSH access to the nodes, access the logs for the Kubelet directly:

`shell $ journalctl -b -f -u kubelet.service `
', '## Mitigation

The mitigation depends on what is causing the Kubelets to become unresponsive. Check for wide-spread networking issues, or node level configuration issues.

See [Kubernetes Docs - kubelet](https://kubernetes.io/docs/reference/command-line-tools-reference/kubelet/)
', 'KubeletDown'), ('## Meaning

Kubernetes API server client is experiencing over 1% error rate in the last 15 minutes.
', '## Impact

Specific kubernetes client may malfunction. Service degradation.
', '## Diagnosis

Usual issues:

  * networking errors
  * too low resources to perform given API calls (usually too low CPU/memory requests)
  * wrong api client (old libraries)
  * investigate if the app does not request more data than it really requires from kubernetes API, for example it has too wide permissions and scans for resources in all namespaces.



Check logs from client side (sometimes app logs).
', '## Mitigation

TODO
', 'KubeClientErrors'), ('## Meaning

Kubernetes aggregated API has reported errors. It has appeared unavailable over 4 times averaged over the past 10m.
', '## Impact

From minor such as inability to see cluster metrics to more severe such as unable to use custom metrics to scale or even unable to use cluster.
', '## Diagnosis

  * Check networking on the node.
  * Check firewall on the node.
  * Investigate additional API logs.
  * Investigate NetworkPolicies if kubeApi - additional API was not filtered out.
  * Investigate NetworkPolicies if prometheus/additional API was not filtered out.


', '## Mitigation

TODO

See [APIServer aggregation](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/apiserver-aggregation/)
', 'KubeAggregatedAPIErrors'), ('## Meaning

Horizontal Pod Autoscaler has been running at max replicas for longer than 15 minutes.
', '## Impact

Horizontal Pod Autoscaler won''t be able to add new pods and thus scale application. **Notice** for some services maximizing HPA is in fact desired.
', '## Diagnosis

Check why HPA was unable to scale:

  * max replicas too low
  * too low value for requests such as CPU?


', '## Mitigation

If using basic metrics like CPU/Memory then ensure to set proper values for `requests`. For memory based scaling ensure there are no memory leaks. If using custom metrics then tine tune how app scales accordingly to it.

Use performance tests to see how the app scales.
', 'KubeHpaMaxedOut'), ('## Meaning

The `KubeProxyDown` alert is triggered when all Kubernetes Proxy instances have not been reachable by the monitoring system for more than 15 minutes.
', '## Impact

kube-proxy is a network proxy that runs on each node in your cluster, implementing part of the Kubernetes Service concept.

kube-proxy maintains network rules on nodes. These network rules allow network communication to your Pods from network sessions inside or outside of your cluster.

kube-proxy uses the operating system packet filtering layer if there is one and it''s available. Otherwise, kube-proxy forwards the traffic itself.
', '## Diagnosis

Check the status of the `kube-proxy` daemon sets in the `kube-system` namespace.

`console kubectl get pods -l k8s-app=kube-proxy -n kube-system `

Check the specific daemon-set for logs with the following command:

`console kubectl logs -n kube-system kube-proxy-b9g23 `
', '## Mitigation

### AWS EKS

If you are running AWS EKS cluster and you find that the `kube-proxy` pods are all running normally, make sure to update the `kube-proxy-config` cm as shown below.

`console kubectl edit cm -n kube-system kube-proxy-config ... metricsBindAddress: 0.0.0.0:10249 ... ` This setting configures the IP address with port for the metrics server to serve on (set to ''0.0.0.0:10249'' for all IPv4 interfaces and ''[::]:10249'' for all IPv6 interfaces). More information on the [documentation page](https://kubernetes.io/docs/reference/command-line-tools-reference/kube-proxy/)

Then just go delete `kube-proxy` pods and new ones will be created automatically.

`console kubectl delete pod -l k8s-app=kube-proxy -n kube-system `
', 'KubeProxyDown'), ('## Meaning

The Kubelet Pod Lifecycle Event Generator has a 99th percentile duration of XX seconds on node.
', '## Impact

TODO
', '## Diagnosis

TODO
', '## Mitigation

TODO
', 'KubeletPlegDurationHigh'), ('## Meaning

Processes experience elevated CPU throttling.
', '## Impact

The alert is purely informative and unless there is some other issue with the application, it can be skipped.
', '## Diagnosis

  * Check if application is performing normally
  * Check if CPU resource requests are adjusted accordingly to the app usage
  * Check kernel version in the node


', '## Mitigation

**Notice** : User shouldn''t increase CPU limits unless the application is behaving erratically (another alert firing).

For this particular reason, the alert is inhibited by default in kube-prometheus and can be sent only if another alert in the same namespace is firing.

**When mixed with other alerts** :

Give specific container in the pod more CPU limits. Requests can stay the same.

In specific cases kubernetes node has too old kernel which is known to have issues with assigning CPU resources to the process [see here](https://github.com/kubernetes/kubernetes/issues/67577)

In certain scenarios ensure to use CPU Pinning and isolation - in short give to the container full CPU cores. Also ensure to update app so that it is aware it runs in cgropus, or explicitly set number of CPU it can use, or limit number of threads.

Longer and more detailed info - [PDF from Intel](https://builders.intel.com/docs/networkbuilders/cpu-pin-and-isolation-in-kubernetes-app-note.pdf)
', 'CPUThrottlingHigh'), ('## Meaning

Kubelet on node has failed to renew its client certificate (XX errors in the last 15 minutes)
', '## Impact

Node will not be able to be used within the cluster.
', '## Diagnosis

Check when certificate was issued and when it expires.
', '## Mitigation

Update certificates in the cluster control nodes and the worker nodes. Refer to the documentation of the tool used to create cluster.

Another option is to delete node if it affects only one,

In extreme situations recreate cluster.
', 'KubeletClientCertificateRenewalErrors'), ('## Meaning

Job is taking more than 1h to complete.
', '## Impact

  * Long processing of batch jobs.
  * Possible issues with scheduling next Job


', '## Diagnosis

  * Check job via `kubectl -n $NAMESPACE describe jobs $JOB`.
  * Check pod events via `kubectl -n $NAMESPACE describe job $JOB`.


', '## Mitigation

  * Give it more resources so it finishes faster, if applicable.
  * See [Job patterns](https://kubernetes.io/docs/tasks/job/)


', 'KubeJobCompletion'), ('## Meaning

Cluster has overcommitted Memory resource requests for Pods and cannot tolerate node failure.

Full context

Total number of Memory requests for pods exceeds cluster capacity. In case of node failure some pods will not fit in the remaining nodes.
', '## Impact

The cluster cannot tolerate node failure. In the event of a node failure, some Pods will be in `Pending` state.
', '## Diagnosis

  * Check if Memory resource requests are adjusted to the app usage
  * Check if some nodes are available and not cordoned
  * Check if cluster-autoscaler has issues with adding new nodes


', '## Mitigation

  * Add more nodes to the cluster - usually it is better to have more smaller nodes, than few bigger.

  * Add different node pools with different instance types to avoid problem when using only one instance type in the cloud.

  * Use pod priorities to avoid important services from losing performance, see [pod priority and preemption](https://kubernetes.io/docs/concepts/scheduling-eviction/pod-priority-preemption/)

  * Fine tune settings for special pods used with [cluster-autoscaler](https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/FAQ.md#how-does-cluster-autoscaler-work-with-pod-priority-and-preemption)

  * Prepare performance tests for the expected workload, plan cluster capacity accordingly.



', 'KubeMemoryOvercommit') ON CONFLICT(rule_name) DO nothing;

INSERT INTO knowledge_base (description, impact, diagnosis, mitigation, rule_name) VALUES ('## Meaning

At least one instance is unable to routed alert to the corresponding integration.
', '## Impact

No impact since another instance should be able to send the notification, unless `AlertmanagerClusterFailedToSendAlerts` is also triggerd for the same integration.
', '## Diagnosis

Verify the amount of failed notification per alert-manager-[instance] for a specific integration.

You can look metrics exposed in prometheus console using promQL. For exemple the following query will display the number of failed notifications per instance for pager duty integration. We have 3 instances involved in the example bellow.

`promql rate(alertmanager_notifications_total{integration="pagerduty"}[5m]) `

![image](https://user-images.githubusercontent.com/3153333/143552468-ff573f1a-19a6-44ea-9c85-631687d01bf9.png)
', '## Mitigation

Depending on the integration, you can have a look to alert-manager logs and act (network, authorization token, firewall...)

Depending on the integration, you can have a look to alert-manager logs and act (network, authorization token, firewall...)

`shell kubectl -n monitoring logs -l ''alertmanager=main'' -c alertmanager `
', 'AlertmanagerFailedToSendAlerts'), ('## Meaning

Half or more of the Alertmanager instances within the same cluster are crashlooping.
', '## Impact

Alerts could be notified multiple time unless pods are crashing to fast and no alerts can be sent.
', '## Diagnosis

```shell kubectl get pod -l app=alertmanager

NAMESPACE NAME READY STATUS RESTARTS AGE default alertmanager-main-0 1/2 CrashLoopBackOff 37107 2d default alertmanager-main-1 2/2 Running 0 43d default alertmanager-main-2 2/2 Running 0 43d ```

Find the root cause by looking to events for a given pod/deployement

`shell kubectl get events --field-selector involvedObject.name=alertmanager-main-0 `
', '## Mitigation

Make sure pods have enough resources (CPU, MEM) to work correctly.
', 'AlertmanagerClusterCrashlooping'), ('## Meaning

The configuration between instances inside a cluster is inconsistent.
', '## Impact

Configuration inconsistency can be multiple and impact is hard to predict. Nevertheless, in most cases the alert might be lost or routed to the incorrect integration. 
', '## Diagnosis

Run a `diff` tool between all `alertmanager.yml` that are deployed to find what is wrong. You could run a job within your CI to avoid this issue in the future.
', '## Mitigation

Delete the incorrect secret and deploy the correct one.
', 'AlertmanagerConfigInconsistent'), ('## Meaning

At least one of alertmanager cluster members cannot be found.
', '## Impact
', '## Diagnosis

Check if IP addresses discovered by alertmanager cluster are the same ones as in alertmanager Service. Following example show possible inconsistency in Endpoint IP addresses:

```shell $ kubectl describe svc alertmanager-main

Name: alertmanager-main Namespace: monitoring ... Endpoints: 10.128.2.3:9095,10.129.2.5:9095,10.131.0.44:9095

$ kubectl get pod -o wide | grep alertmanager-main

alertmanager-main-0 5/5 Running 0 11d 10.129.2.6 alertmanager-main-1 5/5 Running 0 2d16h 10.131.0.44   
alertmanager-main-2 5/5 Running 0 6d 10.128.2.3   
```
', '## Mitigation

Deleting an incorrect Endpoint should trigger its recreation with a correct IP address.
', 'AlertmanagerMembersInconsistent'), ('## Meaning

All instances failed to send notification to an integration. 
', '## Impact

You will not receive a notification when an alert is raised.
', '## Diagnosis

No alerts are received at the integration level from the cluster. 
', '## Mitigation

Depending on the integration, correct the integration with the faulty instance (network, authorization token, firewall...)
', 'AlertmanagerClusterFailedToSendAlerts'), ('## Meaning

The alert `AlertmanagerFailedReload` is triggered when the Alertmanager instance for the cluster monitoring stack has consistently failed to reload its configuration for a certain period.
', '## Impact

The impact depends on the type of the error you will find in the logs. Most of the time, previous configuration is still working, thanks to multiple instances, so avoid deleting existing pods.
', '## Diagnosis

Verify if there is an error in `config-reloader` container logs. Here an example with network issues.

```shell $ kubectl logs sts/alertmanager-main -c config-reloader

level=error ts=2021-09-24T11:24:52.69629226Z caller=runutil.go:101 msg="function failed. Retrying in next tick" err="trigger reload: reload request failed: Post \"http://localhost:9093/alertmanager/-/reload\": dial tcp [::1]:9093: connect: connection refused" ```

You can also verify directly `alertmanager.yaml` file (default: `/etc/alertmanager/config/alertmanager.yaml`).
', '## Mitigation

Running [amtool check-config alertmanager.yaml](https://github.com/prometheus/alertmanager#amtool) on your configuration file will help you detect problem related to syntax. You could also rollback `alertmanager.yaml` to the previous version in order to get back to a stable version.
', 'AlertmanagerFailedReload'), ('## Meaning

Half or more of the Alertmanager instances within the same cluster are down. 
', '## Impact

You have an unstable cluster, if everything goes wrong you will lose the whole cluster.
', '## Diagnosis

Verify why pods are not running. You can get a big picture with `events`.

`shell $ kubectl get events --field-selector involvedObject.kind=Pod | grep alertmanager `
', '## Mitigation

There are no cheap options to mitigate this risk. Verifying any new changes in preprod before production environment should improve stability. 
', 'AlertmanagerClusterDown') ON CONFLICT(rule_name) DO nothing;
