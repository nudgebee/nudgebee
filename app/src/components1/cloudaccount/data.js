export const payload1 = [
  {
    account_id: '0053b816-4b45-4dcd-a612-19545110f8aa',
    tenant_id: '890cad87-c452-4aa7-b84a-742cee0454a1',
    timestamp: '2024-05-20T00:00:00Z',
    pod_cost: 0.17159,
    avg_cpu_used: 0.00088,
    avg_cpu_request: 0.2,
    avg_memory_used: 33491590.09948,
    avg_memory_request: 209715200,
  },
  {
    account_id: '0053b816-4b45-4dcd-a612-19545110f8aa',
    tenant_id: '890cad87-c452-4aa7-b84a-742cee0454a1',
    timestamp: '2024-05-21T00:00:00Z',
    pod_cost: 0.17159,
    avg_cpu_used: 0.00092,
    avg_cpu_request: 0.2,
    avg_memory_used: 33486259.2,
    avg_memory_request: 209715200,
  },
  {
    account_id: '0053b816-4b45-4dcd-a612-19545110f8aa',
    tenant_id: '890cad87-c452-4aa7-b84a-742cee0454a1',
    timestamp: '2024-05-22T00:00:00Z',
    pod_cost: 0.17159,
    avg_cpu_used: 0.0009,
    avg_cpu_request: 0.2,
    avg_memory_used: 32804232.53333,
    avg_memory_request: 209715200,
  },
  {
    account_id: '0053b816-4b45-4dcd-a612-19545110f8aa',
    tenant_id: '890cad87-c452-4aa7-b84a-742cee0454a1',
    timestamp: '2024-05-23T00:00:00Z',
    pod_cost: 0.17159,
    avg_cpu_used: 0.00145,
    avg_cpu_request: 0.2,
    avg_memory_used: 38364859.73333,
    avg_memory_request: 209715200,
  },
  {
    account_id: '0053b816-4b45-4dcd-a612-19545110f8aa',
    tenant_id: '890cad87-c452-4aa7-b84a-742cee0454a1',
    timestamp: '2024-05-24T00:00:00Z',
    pod_cost: 0.17159,
    avg_cpu_used: 0.00218,
    avg_cpu_request: 0.2,
    avg_memory_used: 46460784.35556,
    avg_memory_request: 209715200,
  },
  {
    account_id: '0053b816-4b45-4dcd-a612-19545110f8aa',
    tenant_id: '890cad87-c452-4aa7-b84a-742cee0454a1',
    timestamp: '2024-05-25T00:00:00Z',
    pod_cost: 0.17159,
    avg_cpu_used: 0.00173,
    avg_cpu_request: 0.2,
    avg_memory_used: 45101721.6,
    avg_memory_request: 209715200,
  },
  {
    account_id: '0053b816-4b45-4dcd-a612-19545110f8aa',
    tenant_id: '890cad87-c452-4aa7-b84a-742cee0454a1',
    timestamp: '2024-05-26T00:00:00Z',
    pod_cost: 0.17159,
    avg_cpu_used: 0.00264,
    avg_cpu_request: 0.2,
    avg_memory_used: 53053916.44444,
    avg_memory_request: 209715200,
  },
  {
    account_id: '0053b816-4b45-4dcd-a612-19545110f8aa',
    tenant_id: '890cad87-c452-4aa7-b84a-742cee0454a1',
    timestamp: '2024-05-27T00:00:00Z',
    pod_cost: 0.12571,
    avg_cpu_used: 0.00199,
    avg_cpu_request: 0.2,
    avg_memory_used: 50973004.26565,
    avg_memory_request: 209715200,
  },
];

export const payload = [
  {
    cloud_resourse: {
      name: 'vmalertmanager-victoria-victoria-metrics-k8s-stack-0',
      id: '3f9d0c36-2ffe-5ca1-bc39-fe01d7d2282c',
      type: 'Pod',
      meta: {
        node: 'ip-172-31-84-17.ec2.internal',
        config: {
          ip: '',
          owner: [
            {
              kind: 'StatefulSet',
              name: 'vmalertmanager-victoria-victoria-metrics-k8s-stack',
            },
          ],
          labels: {
            'managed-by': 'vm-operator',
            'app.kubernetes.io/name': 'vmalertmanager',
            'controller-revision-hash': 'vmalertmanager-victoria-victoria-metrics-k8s-stack-6569d8c6c',
            'app.kubernetes.io/instance': 'victoria-victoria-metrics-k8s-stack',
            'app.kubernetes.io/component': 'monitoring',
            'statefulset.kubernetes.io/pod-name': 'vmalertmanager-victoria-victoria-metrics-k8s-stack-0',
          },
          volumes: [
            {
              name: 'config-volume',
              persistent_volume_claim: null,
            },
            {
              name: 'templates-victoria-victoria-metrics-k8s-stack-alertmanager-monz',
              persistent_volume_claim: null,
            },
            {
              name: 'vmalertmanager-victoria-victoria-metrics-k8s-stack-db',
              persistent_volume_claim: null,
            },
            {
              name: 'kube-api-access-k77xv',
              persistent_volume_claim: null,
            },
          ],
          affinity: {},
          qos_class: 'Guaranteed',
          conditions: [
            {
              type: 'Initialized',
              status: 'True',
            },
            {
              type: 'Ready',
              status: 'True',
            },
            {
              type: 'ContainersReady',
              status: 'True',
            },
            {
              type: 'PodScheduled',
              status: 'True',
            },
          ],
          containers: [
            {
              env: [],
              name: 'alertmanager',
              image: 'prom/alertmanager:v0.25.0',
              ports: [9093, 9094],
              resources: {
                limits: {
                  cpu: '100m',
                  memory: '100Mi',
                },
                requests: {
                  cpu: '100m',
                  memory: '100Mi',
                },
              },
              volume_mounts: null,
              liveness_probe: {
                grpc: null,
                _exec: null,
                http_get: {
                  host: null,
                  path: '/-/healthy',
                  port: 'web',
                  scheme: 'HTTP',
                  http_headers: null,
                },
                tcp_socket: null,
                period_seconds: 5,
                timeout_seconds: 5,
                failure_threshold: 10,
                success_threshold: 1,
                initial_delay_seconds: null,
                termination_grace_period_seconds: null,
              },
              readiness_probe: {
                grpc: null,
                _exec: null,
                http_get: {
                  host: null,
                  path: '/-/healthy',
                  port: 'web',
                  scheme: 'HTTP',
                  http_headers: null,
                },
                tcp_socket: null,
                period_seconds: 5,
                timeout_seconds: 5,
                failure_threshold: 10,
                success_threshold: 1,
                initial_delay_seconds: null,
                termination_grace_period_seconds: null,
              },
            },
            {
              env: [],
              name: 'config-reloader',
              image: 'jimmidyson/configmap-reload:v0.3.0',
              ports: [],
              resources: {
                limits: {
                  cpu: '100m',
                  memory: '100Mi',
                },
                requests: {
                  cpu: '100m',
                  memory: '100Mi',
                },
              },
              volume_mounts: null,
              liveness_probe: {},
              readiness_probe: {},
            },
          ],
          toleration: [
            {
              key: 'node.kubernetes.io/not-ready',
              value: null,
              effect: 'NoExecute',
              operator: 'Exists',
              toleration_seconds: 300,
            },
            {
              key: 'node.kubernetes.io/unreachable',
              value: null,
              effect: 'NoExecute',
              operator: 'Exists',
              toleration_seconds: 300,
            },
          ],
          annotations: {
            'kubectl.kubernetes.io/restartedAt': '2024-04-12T06:02:14Z',
          },
          service_account: null,
        },
        status: 'Running',
        namespace: 'victoria',
        controller: 'vmalertmanager-victoria-victoria-metrics-k8s-stack',
        ready_pods: 1,
        total_pods: 1,
        restart_count: {
          alertmanager: 0,
          'config-reloader': 0,
        },
        controllerKind: 'StatefulSet',
        is_helm_release: false,
      },
    },
    severity: 'Info',
    category: 'RightSizing',
    rule_name: 'pod_right_sizing',
    recommendation: {
      alertmanager: [
        {
          info: null,
          metric: {},
          add_info: {
            cpu_percentile_92: 0.01,
            cpu_percentile_95: 0.01,
            cpu_percentile_97: 0.01,
            cpu_percentile_99: 0.01,
          },
          priority: {
            limit: 3,
            request: 1,
          },
          resource: 'cpu',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: 0.1,
            request: 0.1,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: null,
            request: 0.01,
          },
        },
        {
          info: null,
          metric: {},
          add_info: {
            actual_recommended_limit: 104857600,
            actual_recommended_request: 104857600,
          },
          priority: {
            limit: 1,
            request: 1,
          },
          resource: 'memory',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: 104857600,
            request: 104857600,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: 104857600,
            request: 104857600,
          },
        },
      ],
      'config-reloader': [
        {
          info: null,
          metric: {},
          add_info: {
            cpu_percentile_92: 0.01,
            cpu_percentile_95: 0.01,
            cpu_percentile_97: 0.01,
            cpu_percentile_99: 0.01,
          },
          priority: {
            limit: 3,
            request: 1,
          },
          resource: 'cpu',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: 0.1,
            request: 0.1,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: null,
            request: 0.01,
          },
        },
        {
          info: null,
          metric: {},
          add_info: {
            actual_recommended_limit: 104857600,
            actual_recommended_request: 104857600,
          },
          priority: {
            limit: 1,
            request: 1,
          },
          resource: 'memory',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: 104857600,
            request: 104857600,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: 104857600,
            request: 104857600,
          },
        },
      ],
    },
    estimated_savings: 4.025223529411766,
    account_object_id: null,
    updated_at: '2024-05-09T12:03:11.172033',
    id: '481da7a9-d1f7-42a6-88e2-d6b8cbe44d26',
  },
  {
    cloud_resourse: {
      name: 'actions-runner-controller-898bf58b6-dkk6d',
      id: '56d5f2aa-3e2e-595e-a3aa-3f8656268ab3',
      type: 'Pod',
      meta: {
        node: 'ip-172-31-84-17.ec2.internal',
        config: {
          ip: '',
          owner: [
            {
              kind: 'Deployment',
              name: 'actions-runner-controller',
            },
          ],
          labels: {
            'pod-template-hash': '898bf58b6',
            'app.kubernetes.io/name': 'actions-runner-controller',
            'app.kubernetes.io/instance': 'actions-runner-controller',
          },
          volumes: [
            {
              name: 'secret',
              persistent_volume_claim: null,
            },
            {
              name: 'cert',
              persistent_volume_claim: null,
            },
            {
              name: 'tmp',
              persistent_volume_claim: null,
            },
            {
              name: 'kube-api-access-dzqrx',
              persistent_volume_claim: null,
            },
          ],
          affinity: {},
          qos_class: 'Burstable',
          conditions: [
            {
              type: 'Initialized',
              status: 'True',
            },
            {
              type: 'Ready',
              status: 'True',
            },
            {
              type: 'ContainersReady',
              status: 'True',
            },
            {
              type: 'PodScheduled',
              status: 'True',
            },
          ],
          containers: [
            {
              env: [],
              name: 'manager',
              image: 'summerwind/actions-runner-controller:v0.27.6',
              ports: [9443],
              resources: {
                limits: {},
                requests: {
                  cpu: '100m',
                  memory: '100Mi',
                },
              },
              volume_mounts: null,
              liveness_probe: {},
              readiness_probe: {},
            },
            {
              env: [],
              name: 'kube-rbac-proxy',
              image: 'quay.io/brancz/kube-rbac-proxy:v0.13.1',
              ports: [8443],
              resources: {
                limits: {},
                requests: {
                  cpu: '100m',
                  memory: '100Mi',
                },
              },
              volume_mounts: null,
              liveness_probe: {},
              readiness_probe: {},
            },
          ],
          toleration: [
            {
              key: 'node.kubernetes.io/not-ready',
              value: null,
              effect: 'NoExecute',
              operator: 'Exists',
              toleration_seconds: 300,
            },
            {
              key: 'node.kubernetes.io/unreachable',
              value: null,
              effect: 'NoExecute',
              operator: 'Exists',
              toleration_seconds: 300,
            },
          ],
          annotations: {
            'kubectl.kubernetes.io/restartedAt': '2024-03-30T04:43:50Z',
          },
          service_account: null,
        },
        status: 'Running',
        namespace: 'actions-runner-system-1',
        controller: 'actions-runner-controller',
        ready_pods: 1,
        total_pods: 1,
        restart_count: {
          manager: 0,
          'kube-rbac-proxy': 0,
        },
        controllerKind: 'Deployment',
        is_helm_release: false,
      },
    },
    severity: 'Info',
    category: 'RightSizing',
    rule_name: 'pod_right_sizing',
    recommendation: {
      manager: [
        {
          info: null,
          metric: {},
          add_info: {
            cpu_percentile_92: 0.01,
            cpu_percentile_95: 0.01,
            cpu_percentile_97: 0.01,
            cpu_percentile_99: 0.01,
          },
          priority: {
            limit: 1,
            request: 1,
          },
          resource: 'cpu',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: null,
            request: 0.1,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: null,
            request: 0.01,
          },
        },
        {
          info: null,
          metric: {},
          add_info: {
            actual_recommended_limit: 104857600,
            actual_recommended_request: 104857600,
          },
          priority: {
            limit: 3,
            request: 1,
          },
          resource: 'memory',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: null,
            request: 104857600,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: 104857600,
            request: 104857600,
          },
        },
      ],
      'kube-rbac-proxy': [
        {
          info: null,
          metric: {},
          add_info: {
            cpu_percentile_92: 0.01,
            cpu_percentile_95: 0.01,
            cpu_percentile_97: 0.01,
            cpu_percentile_99: 0.01,
          },
          priority: {
            limit: 1,
            request: 1,
          },
          resource: 'cpu',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: null,
            request: 0.1,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: null,
            request: 0.01,
          },
        },
        {
          info: null,
          metric: {},
          add_info: {
            actual_recommended_limit: 104857600,
            actual_recommended_request: 104857600,
          },
          priority: {
            limit: 3,
            request: 1,
          },
          resource: 'memory',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: null,
            request: 104857600,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: 104857600,
            request: 104857600,
          },
        },
      ],
    },
    estimated_savings: 4.025223529411766,
    account_object_id: null,
    updated_at: '2024-05-22T16:19:03',
    id: '08e48e2b-d2b5-4003-af42-e784f488e66d',
  },
  {
    cloud_resourse: {
      name: 'victoria-grafana-76f4b86545-s6zfp',
      id: 'e843fd34-7647-5790-8ec8-dd3fc05c3c8e',
      type: 'Pod',
      meta: {
        node: 'ip-172-31-84-17.ec2.internal',
        config: {
          ip: '',
          owner: [
            {
              kind: 'Deployment',
              name: 'victoria-grafana',
            },
          ],
          labels: {
            'pod-template-hash': '76f4b86545',
            'app.kubernetes.io/name': 'grafana',
            'app.kubernetes.io/instance': 'victoria',
          },
          volumes: [
            {
              name: 'config',
              persistent_volume_claim: null,
            },
            {
              name: 'dashboards-default',
              persistent_volume_claim: null,
            },
            {
              name: 'storage',
              persistent_volume_claim: null,
            },
            {
              name: 'sc-dashboard-volume',
              persistent_volume_claim: null,
            },
            {
              name: 'sc-dashboard-provider',
              persistent_volume_claim: null,
            },
            {
              name: 'sc-datasources-volume',
              persistent_volume_claim: null,
            },
            {
              name: 'kube-api-access-59pqv',
              persistent_volume_claim: null,
            },
          ],
          affinity: {},
          qos_class: 'Burstable',
          conditions: [
            {
              type: 'Initialized',
              status: 'True',
            },
            {
              type: 'Ready',
              status: 'True',
            },
            {
              type: 'ContainersReady',
              status: 'True',
            },
            {
              type: 'PodScheduled',
              status: 'True',
            },
          ],
          containers: [
            {
              env: [
                {
                  name: 'METHOD',
                  value: 'WATCH',
                },
                {
                  name: 'LABEL',
                  value: 'grafana_dashboard',
                },
                {
                  name: 'FOLDER',
                  value: '/tmp/dashboards',
                },
                {
                  name: 'RESOURCE',
                  value: 'both',
                },
                {
                  name: 'REQ_URL',
                  value: 'http://localhost:3000/api/admin/provisioning/dashboards/reload',
                },
                {
                  name: 'REQ_METHOD',
                  value: 'POST',
                },
              ],
              name: 'grafana-sc-dashboard',
              image: 'quay.io/kiwigrid/k8s-sidecar:1.26.1',
              ports: [],
              resources: {
                limits: {},
                requests: {
                  cpu: '100m',
                  memory: '100Mi',
                },
              },
              volume_mounts: null,
              liveness_probe: {},
              readiness_probe: {},
            },
            {
              env: [
                {
                  name: 'GF_PATHS_DATA',
                  value: '/var/lib/grafana/',
                },
                {
                  name: 'GF_PATHS_LOGS',
                  value: '/var/log/grafana',
                },
                {
                  name: 'GF_PATHS_PLUGINS',
                  value: '/var/lib/grafana/plugins',
                },
                {
                  name: 'GF_PATHS_PROVISIONING',
                  value: '/etc/grafana/provisioning',
                },
              ],
              name: 'grafana',
              image: 'docker.io/grafana/grafana:10.4.0',
              ports: [3000, 9094, 9094],
              resources: {
                limits: {},
                requests: {
                  cpu: '100m',
                  memory: '121Mi',
                },
              },
              volume_mounts: null,
              liveness_probe: {
                grpc: null,
                _exec: null,
                http_get: {
                  host: null,
                  path: '/api/health',
                  port: 3000,
                  scheme: 'HTTP',
                  http_headers: null,
                },
                tcp_socket: null,
                period_seconds: 10,
                timeout_seconds: 30,
                failure_threshold: 10,
                success_threshold: 1,
                initial_delay_seconds: 60,
                termination_grace_period_seconds: null,
              },
              readiness_probe: {
                grpc: null,
                _exec: null,
                http_get: {
                  host: null,
                  path: '/api/health',
                  port: 3000,
                  scheme: 'HTTP',
                  http_headers: null,
                },
                tcp_socket: null,
                period_seconds: 10,
                timeout_seconds: 1,
                failure_threshold: 3,
                success_threshold: 1,
                initial_delay_seconds: null,
                termination_grace_period_seconds: null,
              },
            },
          ],
          toleration: [
            {
              key: 'node.kubernetes.io/not-ready',
              value: null,
              effect: 'NoExecute',
              operator: 'Exists',
              toleration_seconds: 300,
            },
            {
              key: 'node.kubernetes.io/unreachable',
              value: null,
              effect: 'NoExecute',
              operator: 'Exists',
              toleration_seconds: 300,
            },
          ],
          annotations: {
            'checksum/config': 'c96eb011d26a4734c2d6ba100d3fb3d4593ec6cf6ba35b9883ce3b3f938c433d',
            'checksum/secret': '6a8a3eaed285ba9de69b23e8368f3ca7c93e64a46de132360971270c38dc3f5a',
            'checksum/dashboards-json-config': 'a3692c9af0a883ba541fd56d63c84cd7a958592718760d129a58d9c7bb261bbe',
            'checksum/sc-dashboard-provider-config': '593c0a8778b83f11fe80ccb21dfb20bc46705e2be3178df1dc4c89d164c8cd9c',
            'kubectl.kubernetes.io/default-container': 'grafana',
          },
          service_account: null,
        },
        status: 'Running',
        namespace: 'victoria',
        controller: 'victoria-grafana',
        ready_pods: 1,
        total_pods: 1,
        restart_count: {
          grafana: 0,
          'grafana-sc-dashboard': 0,
        },
        controllerKind: 'Deployment',
        is_helm_release: false,
      },
    },
    severity: 'Info',
    category: 'RightSizing',
    rule_name: 'pod_right_sizing',
    recommendation: {
      grafana: [
        {
          info: null,
          metric: {},
          add_info: {
            cpu_percentile_92: 0.01,
            cpu_percentile_95: 0.01,
            cpu_percentile_97: 0.01,
            cpu_percentile_99: 0.01,
          },
          priority: {
            limit: 1,
            request: 1,
          },
          resource: 'cpu',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: null,
            request: 0.1,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: null,
            request: 0.01,
          },
        },
        {
          info: null,
          metric: {},
          add_info: {
            actual_recommended_limit: 112197632,
            actual_recommended_request: 112197632,
          },
          priority: {
            limit: 3,
            request: 1,
          },
          resource: 'memory',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: null,
            request: 126877696,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: 130023424,
            request: 130023424,
          },
        },
      ],
      'grafana-sc-dashboard': [
        {
          info: null,
          metric: {},
          add_info: {
            cpu_percentile_92: 0.01,
            cpu_percentile_95: 0.01,
            cpu_percentile_97: 0.01,
            cpu_percentile_99: 0.01,
          },
          priority: {
            limit: 1,
            request: 1,
          },
          resource: 'cpu',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: null,
            request: 0.1,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: null,
            request: 0.01,
          },
        },
        {
          info: null,
          metric: {},
          add_info: {
            actual_recommended_limit: 104857600,
            actual_recommended_request: 104857600,
          },
          priority: {
            limit: 3,
            request: 1,
          },
          resource: 'memory',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: null,
            request: 104857600,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: 104857600,
            request: 104857600,
          },
        },
      ],
    },
    estimated_savings: 4.016289705882354,
    account_object_id: null,
    updated_at: '2024-05-20T03:06:16.463714',
    id: 'd9bbfca2-d305-4d63-8a4a-153e3f90eb67',
  },
  {
    cloud_resourse: {
      name: 'k8s-collector-5c697d7f64-4k49c',
      id: '9a96ddd5-175b-56f5-93c4-cd8e36388826',
      type: 'Pod',
      meta: {
        node: 'ip-172-31-86-85.ec2.internal',
        config: {
          ip: '',
          owner: [
            {
              kind: 'Deployment',
              name: 'k8s-collector',
            },
          ],
          labels: {
            'pod-template-hash': '5c697d7f64',
            'app.kubernetes.io/name': 'k8s-collector',
            'app.kubernetes.io/instance': 'k8s-collector',
          },
          volumes: [
            {
              name: 'kube-api-access-7ln2t',
              persistent_volume_claim: null,
            },
          ],
          affinity: {
            nodeAffinity: {
              requiredDuringSchedulingIgnoredDuringExecution: {
                nodeSelectorTerms: [
                  {
                    matchExpressions: [
                      {
                        key: 'node',
                        values: ['app', 'db'],
                        operator: 'In',
                      },
                    ],
                  },
                ],
              },
            },
          },
          qos_class: '',
          conditions: [
            {
              type: 'Initialized',
              status: 'True',
            },
            {
              type: 'Ready',
              status: 'False',
            },
            {
              type: 'ContainersReady',
              status: 'False',
            },
            {
              type: 'PodScheduled',
              status: 'True',
            },
          ],
          containers: [
            {
              env: [],
              name: 'k8s-collector',
              image:
                '280501305789.dkr.ecr.us-east-1.amazonaws.com/nudgebee-k8s-collector:2024-04-17T03-23-59_b02c50403dffee702b200b98a517cd2881a4848a',
              ports: [5000],
              resources: {
                limits: {},
                requests: {
                  cpu: '100m',
                  memory: '276Mi',
                },
              },
              volume_mounts: null,
              liveness_probe: {},
              readiness_probe: {},
            },
          ],
          toleration: [
            {
              key: 'node',
              value: 'db',
              effect: 'NoSchedule',
              operator: 'Equal',
            },
            {
              key: 'node.kubernetes.io/not-ready',
              effect: 'NoExecute',
              operator: 'Exists',
              tolerationSeconds: 300,
            },
            {
              key: 'node.kubernetes.io/unreachable',
              effect: 'NoExecute',
              operator: 'Exists',
              tolerationSeconds: 300,
            },
          ],
          annotations: {
            'kubectl.kubernetes.io/restartedAt': '2024-04-18T05:36:30Z',
          },
          service_account: '',
        },
        status: 'Running',
        namespace: 'nudgebee',
        controller: 'k8s-collector',
        ready_pods: 0,
        total_pods: 1,
        restart_count: {
          'k8s-collector': 0,
        },
        controllerKind: 'Deployment',
        is_helm_release: false,
      },
    },
    severity: 'Info',
    category: 'RightSizing',
    rule_name: 'pod_right_sizing',
    recommendation: {
      'k8s-collector': [
        {
          info: null,
          metric: {},
          add_info: {
            cpu_percentile_92: 0.019,
            cpu_percentile_95: 0.029,
            cpu_percentile_97: 0.037,
            cpu_percentile_99: 0.042,
          },
          priority: {
            limit: 1,
            request: 1,
          },
          resource: 'cpu',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: null,
            request: 0.042,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: null,
            request: 0.042,
          },
        },
        {
          info: null,
          metric: {},
          add_info: {
            actual_recommended_limit: 232783872,
            actual_recommended_request: 232783872,
          },
          priority: {
            limit: 4,
            request: 4,
          },
          resource: 'memory',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: 1572864000,
            request: 1572864000,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: 267386880,
            request: 267386880,
          },
        },
      ],
    },
    estimated_savings: 3.977877987132354,
    account_object_id: null,
    updated_at: '2024-05-22T13:20:06',
    id: '71c361e5-a9f5-4d6b-ba87-eb8e3bf59778',
  },
  {
    cloud_resourse: {
      name: 'k8s-collector-64d5fd5dfb-snfjn',
      id: 'b3a34847-6f5d-593f-a61e-b2fe32604d95',
      type: 'Pod',
      meta: {
        node: 'ip-172-31-84-17.ec2.internal',
        config: {
          ip: '',
          owner: [
            {
              kind: 'Deployment',
              name: 'k8s-collector',
            },
          ],
          labels: {
            'pod-template-hash': '64d5fd5dfb',
            'app.kubernetes.io/name': 'k8s-collector',
            'app.kubernetes.io/instance': 'k8s-collector',
          },
          volumes: [
            {
              name: 'kube-api-access-zrknx',
              persistent_volume_claim: null,
            },
          ],
          affinity: {
            nodeAffinity: {
              requiredDuringSchedulingIgnoredDuringExecution: {
                nodeSelectorTerms: [
                  {
                    matchExpressions: [
                      {
                        key: 'node',
                        values: ['app', 'db'],
                        operator: 'In',
                      },
                    ],
                  },
                ],
              },
            },
          },
          qos_class: '',
          conditions: [
            {
              type: 'Initialized',
              status: 'True',
            },
            {
              type: 'Ready',
              status: 'False',
            },
            {
              type: 'ContainersReady',
              status: 'False',
            },
            {
              type: 'PodScheduled',
              status: 'True',
            },
          ],
          containers: [
            {
              env: [],
              name: 'k8s-collector',
              image:
                '280501305789.dkr.ecr.us-east-1.amazonaws.com/nudgebee-k8s-collector-test:2024-04-29T07-59-14_b98c6161ce10da8c0931a7bf425099a6b3a1f0bc',
              ports: [5000],
              resources: {
                limits: {},
                requests: {
                  cpu: '100m',
                  memory: '249Mi',
                },
              },
              volume_mounts: null,
              liveness_probe: {},
              readiness_probe: {},
            },
          ],
          toleration: [
            {
              key: 'node',
              value: 'db',
              effect: 'NoSchedule',
              operator: 'Equal',
            },
            {
              key: 'node.kubernetes.io/not-ready',
              effect: 'NoExecute',
              operator: 'Exists',
              tolerationSeconds: 300,
            },
            {
              key: 'node.kubernetes.io/unreachable',
              effect: 'NoExecute',
              operator: 'Exists',
              tolerationSeconds: 300,
            },
          ],
          annotations: {
            'kubectl.kubernetes.io/restartedAt': '2024-05-01T08:39:30Z',
          },
          service_account: '',
        },
        status: 'Running',
        namespace: 'nudgebee-test',
        controller: 'k8s-collector',
        ready_pods: 0,
        total_pods: 1,
        restart_count: {
          'k8s-collector': 0,
        },
        controllerKind: 'Deployment',
        is_helm_release: false,
      },
    },
    severity: 'Info',
    category: 'RightSizing',
    rule_name: 'pod_right_sizing',
    recommendation: {
      'k8s-collector': [
        {
          info: null,
          metric: {},
          add_info: {
            cpu_percentile_92: 0.01,
            cpu_percentile_95: 0.011,
            cpu_percentile_97: 0.013,
            cpu_percentile_99: 0.027,
          },
          priority: {
            limit: 1,
            request: 1,
          },
          resource: 'cpu',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: null,
            request: 0.022,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: null,
            request: 0.027,
          },
        },
        {
          info: null,
          metric: {},
          add_info: {
            actual_recommended_limit: 225443840,
            actual_recommended_request: 225443840,
          },
          priority: {
            limit: 4,
            request: 4,
          },
          resource: 'memory',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: 1572864000,
            request: 1572864000,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: 258998272,
            request: 258998272,
          },
        },
      ],
    },
    estimated_savings: 3.619548529411765,
    account_object_id: null,
    updated_at: '2024-05-25T07:55:05',
    id: '2d49ef90-ca91-4a20-87e0-eb3d2261469e',
  },
  {
    cloud_resourse: {
      name: 'jaeger-operator-b59bd5899-wscjc',
      id: '4f5bb35f-8b1b-589e-a4f4-d7efcba57c13',
      type: 'Pod',
      meta: {
        node: 'ip-172-31-9-121.ec2.internal',
        config: {
          ip: '',
          owner: [
            {
              kind: 'Deployment',
              name: 'jaeger-operator',
            },
          ],
          labels: {
            name: 'jaeger-operator',
            'pod-template-hash': 'b59bd5899',
          },
          volumes: [
            {
              name: 'cert',
              persistent_volume_claim: null,
            },
            {
              name: 'kube-api-access-7pdvg',
              persistent_volume_claim: null,
            },
          ],
          affinity: {},
          qos_class: 'Burstable',
          conditions: [
            {
              type: 'Initialized',
              status: 'True',
            },
            {
              type: 'Ready',
              status: 'True',
            },
            {
              type: 'ContainersReady',
              status: 'True',
            },
            {
              type: 'PodScheduled',
              status: 'True',
            },
          ],
          containers: [
            {
              env: [
                {
                  name: 'OPERATOR_NAME',
                  value: 'jaeger-operator',
                },
                {
                  name: 'LOG-LEVEL',
                  value: 'DEBUG',
                },
                {
                  name: 'KAFKA-PROVISIONING-MINIMAL',
                  value: 'true',
                },
              ],
              name: 'jaeger-operator',
              image: 'quay.io/jaegertracing/jaeger-operator:1.57.0',
              ports: [9443],
              resources: {
                limits: {},
                requests: {
                  cpu: '100m',
                  memory: '100Mi',
                },
              },
              volume_mounts: null,
              liveness_probe: {
                grpc: null,
                _exec: null,
                http_get: {
                  host: null,
                  path: '/healthz',
                  port: 8081,
                  scheme: 'HTTP',
                  http_headers: null,
                },
                tcp_socket: null,
                period_seconds: 20,
                timeout_seconds: 1,
                failure_threshold: 3,
                success_threshold: 1,
                initial_delay_seconds: 15,
                termination_grace_period_seconds: null,
              },
              readiness_probe: {
                grpc: null,
                _exec: null,
                http_get: {
                  host: null,
                  path: '/readyz',
                  port: 8081,
                  scheme: 'HTTP',
                  http_headers: null,
                },
                tcp_socket: null,
                period_seconds: 10,
                timeout_seconds: 1,
                failure_threshold: 3,
                success_threshold: 1,
                initial_delay_seconds: 5,
                termination_grace_period_seconds: null,
              },
            },
            {
              env: [],
              name: 'kube-rbac-proxy',
              image: 'gcr.io/kubebuilder/kube-rbac-proxy:v0.13.1',
              ports: [8443],
              resources: {
                limits: {},
                requests: {
                  cpu: '100m',
                  memory: '100Mi',
                },
              },
              volume_mounts: null,
              liveness_probe: {},
              readiness_probe: {},
            },
          ],
          toleration: [
            {
              key: 'node.kubernetes.io/not-ready',
              value: null,
              effect: 'NoExecute',
              operator: 'Exists',
              toleration_seconds: 300,
            },
            {
              key: 'node.kubernetes.io/unreachable',
              value: null,
              effect: 'NoExecute',
              operator: 'Exists',
              toleration_seconds: 300,
            },
          ],
          annotations: null,
          service_account: null,
        },
        status: 'Running',
        namespace: 'observability',
        controller: 'jaeger-operator',
        ready_pods: 1,
        total_pods: 1,
        restart_count: {
          'jaeger-operator': 0,
          'kube-rbac-proxy': 0,
        },
        controllerKind: 'Deployment',
        is_helm_release: false,
      },
    },
    severity: 'Info',
    category: 'RightSizing',
    rule_name: 'pod_right_sizing',
    recommendation: {
      'jaeger-operator': [
        {
          info: null,
          metric: {},
          add_info: {
            cpu_percentile_92: 0.01,
            cpu_percentile_95: 0.01,
            cpu_percentile_97: 0.01,
            cpu_percentile_99: 0.01,
          },
          priority: {
            limit: 1,
            request: 1,
          },
          resource: 'cpu',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: null,
            request: 0.1,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: null,
            request: 0.01,
          },
        },
        {
          info: null,
          metric: {},
          add_info: {
            actual_recommended_limit: 104857600,
            actual_recommended_request: 104857600,
          },
          priority: {
            limit: 3,
            request: 1,
          },
          resource: 'memory',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: null,
            request: 104857600,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: 104857600,
            request: 104857600,
          },
        },
      ],
      'kube-rbac-proxy': [
        {
          info: null,
          metric: {},
          add_info: {
            cpu_percentile_92: 0.01,
            cpu_percentile_95: 0.01,
            cpu_percentile_97: 0.01,
            cpu_percentile_99: 0.01,
          },
          priority: {
            limit: 1,
            request: 1,
          },
          resource: 'cpu',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: null,
            request: 0.1,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: null,
            request: 0.01,
          },
        },
        {
          info: null,
          metric: {},
          add_info: {
            actual_recommended_limit: 104857600,
            actual_recommended_request: 104857600,
          },
          priority: {
            limit: 3,
            request: 1,
          },
          resource: 'memory',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: null,
            request: 104857600,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: 104857600,
            request: 104857600,
          },
        },
      ],
    },
    estimated_savings: 3.488527058823529,
    account_object_id: null,
    updated_at: '2024-05-27T12:00:00.506196',
    id: '05917eae-09fa-4744-9d9b-f03c66bee6e4',
  },
  {
    cloud_resourse: {
      name: 'relay-server-68855fcb76-fb255',
      id: '1a29c61c-ffdd-5a9e-bd5b-4a8344326553',
      type: 'Pod',
      meta: {
        node: 'ip-172-31-94-146.ec2.internal',
        config: {
          ip: '',
          owner: [
            {
              kind: 'Deployment',
              name: 'relay-server',
            },
          ],
          labels: {
            'pod-template-hash': '68855fcb76',
            'app.kubernetes.io/name': 'relay-server',
            'app.kubernetes.io/instance': 'relay-server',
          },
          volumes: [
            {
              name: 'kube-api-access-4dcvs',
              persistent_volume_claim: null,
            },
          ],
          affinity: {
            pod_affinity: null,
            node_affinity: {
              required_during_scheduling_ignored_during_execution: {
                node_selector_terms: [
                  {
                    match_fields: null,
                    match_expressions: [
                      {
                        key: 'node',
                        values: ['app'],
                        operator: 'In',
                      },
                    ],
                  },
                ],
              },
              preferred_during_scheduling_ignored_during_execution: null,
            },
            pod_anti_affinity: null,
          },
          qos_class: 'Burstable',
          conditions: [
            {
              type: 'Initialized',
              status: 'True',
            },
            {
              type: 'Ready',
              status: 'True',
            },
            {
              type: 'ContainersReady',
              status: 'True',
            },
            {
              type: 'PodScheduled',
              status: 'True',
            },
          ],
          containers: [
            {
              env: [],
              name: 'relay-server',
              image: '280501305789.dkr.ecr.us-east-1.amazonaws.com/relay-server:latest',
              ports: [8080],
              resources: {
                limits: {
                  memory: '104857600',
                },
                requests: {
                  cpu: '188m',
                  memory: '104857600',
                },
              },
            },
          ],
          toleration: [
            {
              key: 'node.kubernetes.io/not-ready',
              value: null,
              effect: 'NoExecute',
              operator: 'Exists',
              toleration_seconds: 300,
            },
            {
              key: 'node.kubernetes.io/unreachable',
              value: null,
              effect: 'NoExecute',
              operator: 'Exists',
              toleration_seconds: 300,
            },
          ],
          annotations: {
            'kubernetes.io/psp': 'eks.privileged',
            'kubectl.kubernetes.io/restartedAt': '2023-12-29T11:14:17Z',
          },
          service_account: 'relay-server',
        },
        status: 'Running',
        namespace: 'nudgebee',
        controller: 'relay-server',
        ready_pods: 1,
        total_pods: 1,
        restart_count: {
          'relay-server': 0,
        },
        controllerKind: 'Deployment',
        is_helm_release: [false],
      },
    },
    severity: 'Info',
    category: 'RightSizing',
    rule_name: 'pod_right_sizing',
    recommendation: {
      'relay-server': [
        {
          info: null,
          metric: {},
          add_info: {
            cpu_percentile_92: 0.01,
            cpu_percentile_95: 0.01,
            cpu_percentile_97: 0.01,
            cpu_percentile_99: 0.01,
          },
          priority: {
            limit: 1,
            request: 1,
          },
          resource: 'cpu',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: null,
            request: 0.1,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: null,
            request: 0.01,
          },
        },
        {
          info: null,
          metric: {},
          add_info: {
            actual_recommended_limit: 104857600,
            actual_recommended_request: 104857600,
          },
          priority: {
            limit: 1,
            request: 1,
          },
          resource: 'memory',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: 134217728,
            request: 134217728,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: 104857600,
            request: 104857600,
          },
        },
      ],
    },
    estimated_savings: 2.2488270220588236,
    account_object_id: null,
    updated_at: '2024-05-23T02:45:10',
    id: 'c7a3dcfe-fcd9-467e-82a5-e5a90403411f',
  },
  {
    cloud_resourse: {
      name: 'auto-pilot-worker-668957b674-mk6kl',
      id: '0f13ab88-64b6-57b9-8e6e-a3be21481101',
      type: 'Pod',
      meta: {
        node: 'ip-172-31-84-17.ec2.internal',
        config: {
          ip: '',
          owner: [
            {
              kind: 'Deployment',
              name: 'auto-pilot-worker',
            },
          ],
          labels: {
            'helm.sh/chart': 'auto-pilot-0.1.0',
            'pod-template-hash': '668957b674',
            'app.kubernetes.io/name': 'auto-pilot-worker',
            'app.kubernetes.io/version': '1.16.0',
            'app.kubernetes.io/instance': 'auto-pilot',
            'app.kubernetes.io/managed-by': 'Helm',
          },
          volumes: [
            {
              name: 'kube-api-access-b6wgh',
              persistent_volume_claim: null,
            },
          ],
          affinity: {},
          qos_class: '',
          conditions: [
            {
              type: 'Initialized',
              status: 'True',
            },
            {
              type: 'Ready',
              status: 'False',
            },
            {
              type: 'ContainersReady',
              status: 'False',
            },
            {
              type: 'PodScheduled',
              status: 'True',
            },
          ],
          containers: [
            {
              env: [
                {
                  name: 'APP_MODE',
                  value: 'worker',
                },
              ],
              name: 'auto-pilot-worker',
              image:
                '280501305789.dkr.ecr.us-east-1.amazonaws.com/nudgebee-auto-pilot-test:2024-04-18T17-30-37_0ac536b6e929fe6a5bf941e291b368059358dcbf',
              ports: [9988],
              resources: {
                limits: {
                  memory: '1Gi',
                },
                requests: {
                  cpu: '100m',
                  memory: '400Mi',
                },
              },
              volume_mounts: null,
              liveness_probe: {},
              readiness_probe: {},
            },
          ],
          toleration: [
            {
              key: 'node.kubernetes.io/not-ready',
              effect: 'NoExecute',
              operator: 'Exists',
              tolerationSeconds: 300,
            },
            {
              key: 'node.kubernetes.io/unreachable',
              effect: 'NoExecute',
              operator: 'Exists',
              tolerationSeconds: 300,
            },
          ],
          annotations: {
            'kubectl.kubernetes.io/restartedAt': '2024-04-18T04:15:07Z',
          },
          service_account: '',
        },
        status: 'Running',
        namespace: 'nudgebee-test',
        controller: 'auto-pilot-worker',
        ready_pods: 0,
        total_pods: 1,
        restart_count: {
          'auto-pilot-worker': 0,
        },
        controllerKind: 'Deployment',
        is_helm_release: true,
      },
    },
    severity: 'Info',
    category: 'RightSizing',
    rule_name: 'pod_right_sizing',
    recommendation: {
      'auto-pilot-worker': [
        {
          info: null,
          metric: {},
          add_info: {
            cpu_percentile_92: 0.01,
            cpu_percentile_95: 0.01,
            cpu_percentile_97: 0.01,
            cpu_percentile_99: 0.025,
          },
          priority: {
            limit: 1,
            request: 1,
          },
          resource: 'cpu',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: null,
            request: 0.085,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: null,
            request: 0.025,
          },
        },
        {
          info: null,
          metric: {},
          add_info: {
            actual_recommended_limit: 104857600,
            actual_recommended_request: 104857600,
          },
          priority: {
            limit: 4,
            request: 3,
          },
          resource: 'memory',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: 1073741824,
            request: 419430400,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: 104857600,
            request: 104857600,
          },
        },
      ],
    },
    estimated_savings: 2.235123529411765,
    account_object_id: null,
    updated_at: '2024-05-10T04:59:17.941375',
    id: 'ea942f40-522f-4a8d-99bf-54ac8466904c',
  },
  {
    cloud_resourse: {
      name: 'vmalert-victoria-victoria-metrics-k8s-stack-5f957b67f9-fsdwf',
      id: '2d1c5444-d096-5244-a803-5bc5b0884b2c',
      type: 'Pod',
      meta: {
        node: 'ip-172-31-88-6.ec2.internal',
        config: {
          ip: '',
          owner: [
            {
              kind: 'Deployment',
              name: 'vmalert-victoria-victoria-metrics-k8s-stack',
            },
          ],
          labels: {
            'managed-by': 'vm-operator',
            'pod-template-hash': '5f957b67f9',
            'app.kubernetes.io/name': 'vmalert',
            'app.kubernetes.io/instance': 'victoria-victoria-metrics-k8s-stack',
            'app.kubernetes.io/component': 'monitoring',
          },
          volumes: [
            {
              name: 'remote-secrets',
              persistent_volume_claim: null,
            },
            {
              name: 'tls-assets',
              persistent_volume_claim: null,
            },
            {
              name: 'vm-victoria-victoria-metrics-k8s-stack-rulefiles-0',
              persistent_volume_claim: null,
            },
            {
              name: 'kube-api-access-x2jbf',
              persistent_volume_claim: null,
            },
          ],
          affinity: {},
          qos_class: '',
          conditions: [
            {
              type: 'DisruptionTarget',
              status: 'True',
            },
            {
              type: 'Initialized',
              status: 'True',
            },
            {
              type: 'Ready',
              status: 'False',
            },
            {
              type: 'ContainersReady',
              status: 'False',
            },
            {
              type: 'PodScheduled',
              status: 'True',
            },
          ],
          containers: [
            {
              env: [],
              name: 'vmalert',
              image: 'victoriametrics/vmalert:v1.98.0',
              ports: [8080],
              resources: {
                limits: {
                  memory: '100Mi',
                },
                requests: {
                  cpu: '100m',
                  memory: '100Mi',
                },
              },
              volume_mounts: null,
              liveness_probe: {
                httpGet: {
                  path: '/health',
                  port: 8080,
                  scheme: 'HTTP',
                },
                periodSeconds: 5,
                timeoutSeconds: 5,
                failureThreshold: 10,
                successThreshold: 1,
              },
              readiness_probe: {
                httpGet: {
                  path: '/health',
                  port: 8080,
                  scheme: 'HTTP',
                },
                periodSeconds: 5,
                timeoutSeconds: 5,
                failureThreshold: 10,
                successThreshold: 1,
              },
            },
            {
              env: [],
              name: 'config-reloader',
              image: 'jimmidyson/configmap-reload:v0.3.0',
              ports: [],
              resources: {
                limits: {
                  memory: '100Mi',
                },
                requests: {
                  cpu: '100m',
                  memory: '100Mi',
                },
              },
              volume_mounts: null,
              liveness_probe: {},
              readiness_probe: {},
            },
          ],
          toleration: [
            {
              key: 'node.kubernetes.io/not-ready',
              effect: 'NoExecute',
              operator: 'Exists',
              tolerationSeconds: 300,
            },
            {
              key: 'node.kubernetes.io/unreachable',
              effect: 'NoExecute',
              operator: 'Exists',
              tolerationSeconds: 300,
            },
          ],
          annotations: null,
          service_account: '',
        },
        status: 'Running',
        namespace: 'victoria',
        controller: 'vmalert-victoria-victoria-metrics-k8s-stack',
        ready_pods: 0,
        total_pods: 1,
        restart_count: {
          vmalert: 0,
          'config-reloader': 0,
        },
        controllerKind: 'Deployment',
        is_helm_release: false,
      },
    },
    severity: 'Info',
    category: 'RightSizing',
    rule_name: 'pod_right_sizing',
    recommendation: {
      vmalert: [
        {
          info: null,
          metric: {},
          add_info: {
            cpu_percentile_92: 0.033,
            cpu_percentile_95: 0.034,
            cpu_percentile_97: 0.034,
            cpu_percentile_99: 0.035,
          },
          priority: {
            limit: 3,
            request: 1,
          },
          resource: 'cpu',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: 0.2,
            request: 0.05,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: null,
            request: 0.035,
          },
        },
        {
          info: null,
          metric: {},
          add_info: {
            actual_recommended_limit: 104857600,
            actual_recommended_request: 104857600,
          },
          priority: {
            limit: 3,
            request: 2,
          },
          resource: 'memory',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: 524288000,
            request: 209715200,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: 104857600,
            request: 104857600,
          },
        },
      ],
      'config-reloader': [
        {
          info: null,
          metric: {},
          add_info: {
            cpu_percentile_92: 0.01,
            cpu_percentile_95: 0.01,
            cpu_percentile_97: 0.01,
            cpu_percentile_99: 0.01,
          },
          priority: {
            limit: 3,
            request: 1,
          },
          resource: 'cpu',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: 0.1,
            request: 0.1,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: null,
            request: 0.01,
          },
        },
        {
          info: null,
          metric: {},
          add_info: {
            actual_recommended_limit: 104857600,
            actual_recommended_request: 104857600,
          },
          priority: {
            limit: 1,
            request: 1,
          },
          resource: 'memory',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: 26214400,
            request: 26214400,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: 104857600,
            request: 104857600,
          },
        },
      ],
    },
    estimated_savings: 2.0994961764705877,
    account_object_id: null,
    updated_at: '2024-05-12T12:01:01.222845',
    id: '5604b577-1135-43fe-8bc5-b6a6ed93e0df',
  },
  {
    cloud_resourse: {
      name: 'relay-server-844fc85f4b-dp2sk',
      id: '54f7b4c7-cae0-5f48-b588-29f2e0742669',
      type: 'Pod',
      meta: {
        node: 'ip-172-31-9-132.ec2.internal',
        config: {
          ip: '',
          owner: [
            {
              kind: 'Deployment',
              name: 'relay-server',
            },
          ],
          labels: {
            'pod-template-hash': '844fc85f4b',
            'app.kubernetes.io/name': 'relay-server',
            'app.kubernetes.io/instance': 'relay-server',
          },
          volumes: [
            {
              name: 'kube-api-access-xdfsr',
              persistent_volume_claim: null,
            },
          ],
          affinity: {
            nodeAffinity: {
              requiredDuringSchedulingIgnoredDuringExecution: {
                nodeSelectorTerms: [
                  {
                    matchExpressions: [
                      {
                        key: 'node',
                        values: ['app', 'db'],
                        operator: 'In',
                      },
                    ],
                  },
                ],
              },
            },
          },
          qos_class: '',
          conditions: [
            {
              type: 'Initialized',
              status: 'True',
            },
            {
              type: 'Ready',
              status: 'False',
            },
            {
              type: 'ContainersReady',
              status: 'False',
            },
            {
              type: 'PodScheduled',
              status: 'True',
            },
          ],
          containers: [
            {
              env: [],
              name: 'relay-server',
              image:
                '280501305789.dkr.ecr.us-east-1.amazonaws.com/nudgebee-relay-server-test:2024-04-22T07-55-34_2486f2e930d95822df0c5630e6372718c4d4abad',
              ports: [8080],
              resources: {
                limits: {
                  memory: '100Mi',
                },
                requests: {
                  cpu: '100m',
                  memory: '100Mi',
                },
              },
              volume_mounts: null,
              liveness_probe: {
                httpGet: {
                  path: '/status',
                  port: 'http',
                  scheme: 'HTTP',
                },
                periodSeconds: 10,
                timeoutSeconds: 1,
                failureThreshold: 3,
                successThreshold: 1,
              },
              readiness_probe: {
                httpGet: {
                  path: '/status',
                  port: 'http',
                  scheme: 'HTTP',
                },
                periodSeconds: 10,
                timeoutSeconds: 1,
                failureThreshold: 3,
                successThreshold: 1,
              },
            },
          ],
          toleration: [
            {
              key: 'node',
              value: 'db',
              effect: 'NoSchedule',
              operator: 'Equal',
            },
            {
              key: 'node.kubernetes.io/not-ready',
              effect: 'NoExecute',
              operator: 'Exists',
              tolerationSeconds: 300,
            },
            {
              key: 'node.kubernetes.io/unreachable',
              effect: 'NoExecute',
              operator: 'Exists',
              tolerationSeconds: 300,
            },
          ],
          annotations: {
            'kubectl.kubernetes.io/restartedAt': '2024-04-22T09:02:12Z',
          },
          service_account: '',
        },
        status: 'Running',
        namespace: 'nudgebee-test',
        controller: 'relay-server',
        ready_pods: 0,
        total_pods: 1,
        restart_count: {
          'relay-server': 0,
        },
        controllerKind: 'Deployment',
        is_helm_release: false,
      },
    },
    severity: 'Info',
    category: 'RightSizing',
    rule_name: 'pod_right_sizing',
    recommendation: {
      'relay-server': [
        {
          info: null,
          metric: {},
          add_info: {
            cpu_percentile_92: 0.01,
            cpu_percentile_95: 0.01,
            cpu_percentile_97: 0.01,
            cpu_percentile_99: 0.01,
          },
          priority: {
            limit: 1,
            request: 1,
          },
          resource: 'cpu',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: null,
            request: 0.1,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: null,
            request: 0.01,
          },
        },
        {
          info: null,
          metric: {},
          add_info: {
            actual_recommended_limit: 104857600,
            actual_recommended_request: 104857600,
          },
          priority: {
            limit: 1,
            request: 1,
          },
          resource: 'memory',
          strategy: {
            name: 'nudgebee',
            settings: {
              cpu_percentile: 99,
              points_required: 50,
              history_duration: 336,
              cpu_percentile_92: 92,
              cpu_percentile_95: 95,
              cpu_percentile_97: 97,
              cpu_percentile_99: 99,
              timeframe_duration: 1.25,
              memory_buffer_percentage: 15,
            },
          },
          allocated: {
            limit: 134217728,
            request: 134217728,
          },
          description:
            '[b]Nudgebee Strategy[/b]\n\nCPU request: 99.0% percentile, limit: unset\nMemory request: max + 15.0%, limit: max + 15.0%\nHistory: 336.0 hours\nStep: 1.25 minutes\n\nThis strategy does not work with objects with HPA defined (Horizontal Pod Autoscaler).\nIf HPA is defined for CPU or Memory, the strategy will return "?" for that resource.\n\nLearn more: [underline]https://github.com/robusta-dev/krr#algorithm[/underline]',
          recommended: {
            limit: 104857600,
            request: 104857600,
          },
        },
      ],
    },
    estimated_savings: 2.0959941176470593,
    account_object_id: null,
    updated_at: '2024-05-10T04:59:17.941375',
    id: '6607f81b-9777-4f4c-866c-e55e9f084d99',
  },
];

export const summaryResponse = {
  cluster_data: {
    account_id: '0053b816-4b45-4dcd-a612-19545110f8aa',
    avg_cpu_used_node: 10.152059999999999,
    avg_memory_used_node: 41765.51171310308,
    node_count: 4,
    spot_node_count: 2,
    ondemand_node_count: 2,
    pod_count: 144,
    failed_pod_count: 3,
    running_pod_count: 137,
    pending_pod_count: 1,
    total_memory_allocated: 41765.51171310308,
    total_memory_capacity: 54746,
    total_cpu_allocated: 10.152059999999999,
    total_cpu_capacity: 14,
    replicaSet: 65,
    statefulSet: 15,
    daemonSet: 8,
    deployment: 58,
    job: 1,
    event: [
      {
        aggregation_key: 'ApplicationAPIFailures',
        event_count: 322,
      },
      {
        aggregation_key: 'CPUThrottlingHigh',
        event_count: 1,
      },
      {
        aggregation_key: 'HighErrorCriticalLogs',
        event_count: 7,
      },
      {
        aggregation_key: 'image_pull_backoff_reporter',
        event_count: 5,
      },
      {
        aggregation_key: 'job_failure',
        event_count: 2,
      },
      {
        aggregation_key: 'KubeJobFailed',
        event_count: 3,
      },
      {
        aggregation_key: 'KubeJobNotCompleted',
        event_count: 1,
      },
      {
        aggregation_key: 'KubePodNotReady',
        event_count: 1,
      },
      {
        aggregation_key: 'Kubernetes Warning Event',
        event_count: 50,
      },
      {
        aggregation_key: 'pod_oom_killer_enricher',
        event_count: 2,
      },
    ],
  },
  last_month_spend: 441.43122999999997,
  current_month_spend: 11.097570000000001,
  current_month_projected_spend: 11.097570000000001,
  recommended_saving: 67.49877481250005,
  yearly_recommendation_saving: 809.9852977500005,
  current_month_avg_daily_cost: 11.097570000000001,
  last_month_avg_daily_cost: 14.239717096774193,
  current_year_spend: 1601.4249299999967,
  current_year_projected_spend: 1668.0103499999968,
};
