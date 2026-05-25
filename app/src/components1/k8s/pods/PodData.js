export default {
  name: 'dev-grafana-28260000-s29kz',
  status: 'Completed',
  Created: 'Sep 25, 2023, 10:45:00 AM UTC+5:30',
  PodIP: '172.31.6.232',
  PodIPs: ['172.31.6.232', '172.31.6.232'],
  Controlledby: 'Job/optscale-resource-discovery-scheduler-28260315',
  ParentController: 'CronJob/optscale-resource-discovery-scheduler',
  Node: 'ip-172-31-1-86.ec2.internal',
  Namespace: 'optscale-test',
  QoSClass: 'BestEffort',
  Labels: [
    'controller-uid=a947ec33-1797-4dd7-a5c1-beb530b9d6fa',
    'job-name=optscale-resource-discovery-scheduler-28260315',
    'app.kubernetes.io/version=0.25.0',
  ],
  Annotations: ['kubectl.kubernetes.io/default-container=alert', 'seccomp.security.alpha.kubernetes.io/pod=runtime/default'],
  Conditions: ['Initialized', 'Ready', 'ContainersReady', 'PodScheduled'],
  ports: [
    { name: 'txp', value: '8080' },
    { name: 'txp', value: '8081' },
  ],
};
