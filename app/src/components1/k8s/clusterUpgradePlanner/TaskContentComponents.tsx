import React, { useEffect, useState } from 'react';
import { Box, Typography, Skeleton, Alert } from '@mui/material';
import { Chip as DsChip, type ChipTone } from '@components1/ds/Chip';
import Pdb from '@components1/k8s/cluster-upgrade/cards/Pdb';
import EksAddOn from '@components1/k8s/cluster-upgrade/cards/EksAddOn';
import KubeVersion from '@components1/k8s/cluster-upgrade/cards/KubeVersion';
import DeprecatedApis from '@components1/k8s/cluster-upgrade/cards/DeprecatedApis';
import apiKubernetes1 from '@api1/kubernetes1';
import { colors, ds } from 'src/utils/colors';
import CustomTable2 from '@common-new/tables/CustomTable2';
import CustomLabels from '@common-new/widgets/CustomLabels';
import { Button as DsButton } from '@components1/ds/Button';
import WidgetCard from '@components1/ds/WidgetCard';
import { Stat } from '@components1/ds/Stat';
import { hasWriteAccess } from '@lib/auth';
import { convertToLocalTime } from '@lib/datetime';

// Shared green-tone "X/Y healthy" caption used under stat values in the
// pre/post-flight summary cards. Keeps the visual identity from the legacy
// hand-rolled blocks (colored secondary count under the headline number).
const healthySub = (text: string) => (
  <Box component='span' sx={{ color: ds.green[600], fontSize: ds.text.caption, fontWeight: ds.weight.medium }}>
    {text}
  </Box>
);

// Maps the overall-cluster health label (Healthy / Good / Degraded / Critical)
// onto the closest DS Chip tone. Keeps the visual cue (green / blue / amber /
// red) without leaking raw hex colors into JSX.
const healthLabelToTone: Record<string, ChipTone> = {
  Healthy: 'success',
  Good: 'info',
  Degraded: 'warning',
  Critical: 'critical',
};
const toHealthTone = (label: string): ChipTone => healthLabelToTone[label] || 'neutral';

const statCardSx = { flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` };
const statRowSx = {
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))',
  gap: ds.space[3],
  mb: ds.space[3],
};

export const PdbContent: React.FC<{ accountId?: string; onInsightsChange?: (insights: any[]) => void }> = ({ accountId, onInsightsChange }) => {
  const [pdbComponent, setPdbComponent] = useState<React.ReactNode>(null);

  useEffect(() => {
    const initializePdb = async () => {
      const pdb = new Pdb();
      await pdb.canRenderContent(accountId);
      const contentComponents = pdb.getContentComponents();
      const insights = pdb.getHighLightsData();

      if (contentComponents.length > 0) {
        setPdbComponent(contentComponents[0]());
      }

      if (onInsightsChange && insights) {
        onInsightsChange(insights);
      }
    };

    if (accountId) {
      initializePdb();
    }
  }, [accountId, onInsightsChange]);

  return <>{pdbComponent}</>;
};

interface HelmRelease {
  name: string;
  namespace: string;
  chart_name: string;
  chart_version: string;
  app_version?: string;
  kube_version?: string;
  status: string;
  compatible: string;
  reason?: string;
}

interface HelmCompatibilityData {
  target_version: string;
  total_releases: number;
  compatible: number;
  incompatible: number;
  unknown: number;
  releases: HelmRelease[];
}

export const HelmContent: React.FC<{ accountId?: string; onInsightsChange?: (insights: any[]) => void }> = ({ accountId, onInsightsChange }) => {
  const [data, setData] = useState<HelmCompatibilityData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (accountId) {
      apiKubernetes1
        .getClusterHealth(accountId, 'helm_compatibility')
        .then((response: any) => {
          if (response?.res?.helm_compatibility) {
            setData(response.res.helm_compatibility);
          }
          setLoading(false);
        })
        .catch(() => {
          setError('Failed to fetch Helm compatibility data');
          setLoading(false);
        });
    }
  }, [accountId]);

  useEffect(() => {
    if (onInsightsChange && data) {
      const insights: any[] = [];
      if (data.incompatible > 0) {
        insights.push({
          message: `${data.incompatible} Helm release${data.incompatible > 1 ? 's are' : ' is'} incompatible with the target version.`,
          component: null,
          severity: 'Critical',
        });
      }
      if (data.unknown > 0) {
        insights.push({
          message: `${data.unknown} Helm release${data.unknown > 1 ? 's have' : ' has'} no kubeVersion constraint specified.`,
          component: null,
          severity: 'Warning',
        });
      }
      if (data.compatible > 0 && data.incompatible === 0) {
        insights.push({
          message: `${data.compatible} Helm release${data.compatible > 1 ? 's are' : ' is'} compatible with the target version.`,
          component: null,
          severity: 'Info',
        });
      }
      onInsightsChange(insights);
    }
  }, [data, onInsightsChange]);

  if (loading) {
    return (
      <Box sx={{ p: 2 }}>
        <Skeleton variant='text' width='60%' height={24} />
        <Skeleton variant='rectangular' width='100%' height={200} sx={{ mt: 2 }} />
      </Box>
    );
  }

  if (error) {
    return (
      <Box sx={{ p: 2 }}>
        <Alert severity='error'>{error}</Alert>
      </Box>
    );
  }

  const releases = data?.releases || [];

  const tableHeaders = [
    { name: 'Release Name', width: '18%' },
    { name: 'Namespace', width: '14%' },
    { name: 'Chart', width: '16%' },
    { name: 'Chart Version', width: '12%' },
    { name: 'Kube Version Constraint', width: '16%' },
    { name: 'Compatible', width: '10%' },
    { name: 'Reason', width: '14%' },
  ];

  const tableData = releases.map((release) => [
    {
      text: release.name,
      component: <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.primary }}>{release.name}</Typography>,
    },
    {
      text: release.namespace,
      component: <Typography sx={{ fontSize: '12px', color: colors.text.secondary }}>{release.namespace}</Typography>,
    },
    {
      text: release.chart_name,
      component: <Typography sx={{ fontSize: '12px', color: colors.text.secondary }}>{release.chart_name}</Typography>,
    },
    {
      text: release.chart_version,
      component: <Typography sx={{ fontSize: '12px', color: colors.text.secondary, fontFamily: 'monospace' }}>{release.chart_version}</Typography>,
    },
    {
      text: release.kube_version || '-',
      component: (
        <Typography sx={{ fontSize: '11px', color: colors.text.secondary, fontFamily: 'monospace' }}>{release.kube_version || '-'}</Typography>
      ),
    },
    {
      text: release.compatible,
      component: <CustomLabels text={release.compatible} />,
    },
    {
      text: release.reason || '-',
      component: <Typography sx={{ fontSize: '11px', color: colors.text.secondary }}>{release.reason || '-'}</Typography>,
    },
  ]);

  return (
    <Box>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
        <Typography variant='h6' sx={{ fontSize: '16px', fontWeight: 600 }}>
          Helm Compatibility ({releases.length} releases{data?.target_version ? `, target: ${data.target_version}` : ''})
        </Typography>
        <Box sx={{ display: 'flex', gap: 1 }}>
          {data && (
            <>
              <CustomLabels text={`compatible: ${data.compatible}`} height='18px' />
              <CustomLabels text={`incompatible: ${data.incompatible}`} height='18px' />
              <CustomLabels text={`unknown: ${data.unknown}`} height='18px' />
            </>
          )}
        </Box>
      </Box>

      {releases.length === 0 ? (
        <Typography variant='body2' color='text.secondary'>
          No Helm releases found in the cluster.
        </Typography>
      ) : (
        <CustomTable2 tableData={tableData as any} headers={tableHeaders as any} loading={loading} rowsPerPage={10} />
      )}
    </Box>
  );
};

export const AddOnContent: React.FC<{ accountId?: string; onInsightsChange?: (insights: any[]) => void }> = ({ accountId, onInsightsChange }) => {
  const [addOnComponent, setAddOnComponent] = useState<React.ReactNode>(null);

  useEffect(() => {
    const initializeAddOn = async () => {
      const addOn = new EksAddOn();
      await addOn.canRenderContent(accountId);
      const contentComponents = addOn.getContentComponents();
      const insights = addOn.getHighLightsData();

      if (contentComponents.length > 0) {
        setAddOnComponent(contentComponents[0]());
      }

      if (onInsightsChange && insights) {
        onInsightsChange(insights);
      }
    };

    if (accountId) {
      initializeAddOn();
    }
  }, [accountId, onInsightsChange]);

  return <>{addOnComponent}</>;
};

export const KubeProxyContent: React.FC<{ accountId?: string; onInsightsChange?: (insights: any[]) => void }> = ({ accountId, onInsightsChange }) => {
  const [kubeProxyComponent, setKubeProxyComponent] = useState<React.ReactNode>(null);

  useEffect(() => {
    const initializeKubeProxy = async () => {
      const kubeProxy = new KubeVersion();
      await kubeProxy.canRenderContent(accountId);
      const contentComponents = kubeProxy.getContentComponents();
      const insights = kubeProxy.getHighLightsData();

      if (contentComponents.length > 0) {
        setKubeProxyComponent(contentComponents[0]());
      }

      if (onInsightsChange && insights) {
        onInsightsChange(insights);
      }
    };

    if (accountId) {
      initializeKubeProxy();
    }
  }, [accountId, onInsightsChange]);

  return <>{kubeProxyComponent}</>;
};

export const DeprecatedApisContent: React.FC<{ accountId?: string; targetVersion?: string; onInsightsChange?: (insights: any[]) => void }> = ({
  accountId,
  targetVersion,
  onInsightsChange,
}) => {
  const [deprecatedApisComponent, setDeprecatedApisComponent] = useState<React.ReactNode>(null);

  useEffect(() => {
    const initializeDeprecatedApis = async () => {
      const deprecatedApis = new DeprecatedApis({ disabledInfographic: true });
      await deprecatedApis.canRenderContent(accountId, targetVersion);
      const contentComponents = deprecatedApis.getContentComponents();
      const insights = deprecatedApis.getHighLightsData();

      if (contentComponents.length > 0) {
        setDeprecatedApisComponent(contentComponents[0]());
      }

      if (onInsightsChange && insights) {
        onInsightsChange(insights);
      }
    };

    if (accountId) {
      initializeDeprecatedApis();
    }
  }, [accountId, targetVersion, onInsightsChange]);

  return <>{deprecatedApisComponent}</>;
};

interface Workload {
  name: string;
  namespace: string;
  replicas: number;
  available: number;
}

export const ClusterHealthWorkloadsContent: React.FC<{ accountId?: string }> = ({ accountId }) => {
  const [workloads, setWorkloads] = useState<Workload[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (accountId) {
      apiKubernetes1
        .getClusterHealth(accountId, 'workloads')
        .then((response: any) => {
          if (response?.res?.workloads) {
            setWorkloads(response.res.workloads);
          }
          setLoading(false);
        })
        .catch(() => {
          setError('Failed to fetch workloads health data');
          setLoading(false);
        });
    }
  }, [accountId]);

  if (loading) {
    return (
      <Box sx={{ p: 2 }}>
        <Skeleton variant='text' width='60%' height={24} />
        <Skeleton variant='rectangular' width='100%' height={200} sx={{ mt: 2 }} />
      </Box>
    );
  }

  if (error) {
    return (
      <Box sx={{ p: 2 }}>
        <Alert severity='error'>{error}</Alert>
      </Box>
    );
  }

  const getWorkloadStatus = (available: number, replicas: number) => {
    if (available === replicas && replicas > 0) {
      return 'healthy';
    }
    if (available === 0) {
      return 'failed';
    }
    if (available < replicas) {
      return 'degraded';
    }
    return 'unknown';
  };

  const tableHeaders = [
    { name: 'Workload Name', width: '30%' },
    { name: 'Namespace', width: '25%' },
    { name: 'Status', width: '15%' },
    { name: 'Replicas', width: '30%' },
  ];

  const tableData = workloads.map((workload) => {
    const status = getWorkloadStatus(workload.available, workload.replicas);

    return [
      {
        text: workload.name,
        component: <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.primary }}>{workload.name}</Typography>,
      },
      {
        text: workload.namespace,
        component: <Typography sx={{ fontSize: '12px', color: colors.text.secondary }}>{workload.namespace}</Typography>,
      },
      {
        text: status,
        component: <CustomLabels text={status} />,
      },
      {
        text: `${workload.available}/${workload.replicas}`,
        component: (
          <Typography sx={{ fontSize: '12px', color: colors.text.secondary, fontFamily: 'monospace' }}>
            {workload.available}/{workload.replicas}
          </Typography>
        ),
      },
    ];
  });

  // Count by status
  const statusCounts = workloads.reduce((acc, workload) => {
    const status = getWorkloadStatus(workload.available, workload.replicas);
    acc[status] = (acc[status] || 0) + 1;
    return acc;
  }, {} as Record<string, number>);

  return (
    <Box>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
        <Typography variant='h6' sx={{ fontSize: '16px', fontWeight: 600 }}>
          Workload Health ({workloads.length} workloads)
        </Typography>
        <Box sx={{ display: 'flex', gap: 1 }}>
          {Object.entries(statusCounts).map(([status, count]) => (
            <CustomLabels key={status} text={`${status}: ${count}`} height='18px' />
          ))}
        </Box>
      </Box>

      {workloads.length === 0 ? (
        <Typography variant='body2' color='text.secondary'>
          No workloads found in the cluster.
        </Typography>
      ) : (
        <CustomTable2 tableData={tableData as any} headers={tableHeaders as any} loading={loading} rowsPerPage={10} />
      )}
    </Box>
  );
};

interface Service {
  name: string;
  namespace: string;
  type: string;
  selector: Record<string, string>;
  status: string;
}

export const ClusterHealthServicesContent: React.FC<{ accountId?: string }> = ({ accountId }) => {
  const [services, setServices] = useState<Service[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (accountId) {
      apiKubernetes1.getClusterHealth(accountId, 'services').then((response: any) => {
        if (response?.res?.services) {
          setServices(response.res.services);
        }
        setLoading(false);
      });
    }
  }, [accountId]);

  if (loading) {
    return (
      <Box sx={{ p: 2 }}>
        <Skeleton variant='text' width='60%' height={24} />
        <Skeleton variant='rectangular' width='100%' height={200} sx={{ mt: 2 }} />
      </Box>
    );
  }

  const tableHeaders = [
    { name: 'Service Name', width: '25%' },
    { name: 'Namespace', width: '20%' },
    { name: 'Type', width: '15%' },
    { name: 'Selectors', width: '40%' },
  ];

  const tableData = services.map((service) => [
    {
      text: service.name,
      component: <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.primary }}>{service.name}</Typography>,
    },
    {
      text: service.namespace,
      component: <Typography sx={{ fontSize: '12px', color: colors.text.secondary }}>{service.namespace}</Typography>,
    },
    {
      text: service.type,
      component: <CustomLabels text={service.type} />,
    },
    {
      text:
        Object.keys(service.selector).length > 0
          ? Object.entries(service.selector)
              .map(([k, v]) => `${k}: ${v}`)
              .join(', ')
          : 'No selectors',
      component: (
        <Typography sx={{ fontSize: '11px', color: colors.text.secondary }}>
          {Object.keys(service.selector).length > 0
            ? Object.entries(service.selector)
                .map(([k, v]) => `${k}: ${v}`)
                .join(', ')
            : 'No selectors'}
        </Typography>
      ),
    },
  ]);

  return (
    <Box>
      <Typography variant='h6' sx={{ mb: 2, fontSize: '16px', fontWeight: 600 }}>
        Services Health ({services.length} services)
      </Typography>

      {services.length === 0 ? (
        <Typography variant='body2' color='text.secondary'>
          No services found in the cluster.
        </Typography>
      ) : (
        <CustomTable2 tableData={tableData as any} headers={tableHeaders as any} loading={loading} />
      )}
    </Box>
  );
};
interface NodeCondition {
  lastHeartbeatTime: string;
  lastTransitionTime: string;
  message: string;
  reason: string;
  status: string;
  type: string;
}

interface Node {
  name: string;
  version: string;
  conditions: NodeCondition[];
  nodeGroup?: {
    ami_type: string;
    capacity_type: string;
    desired_size: number;
    disk_size: number;
    instance_type: string;
    kubernetes_version: string;
    launch_template: any;
    max_size: number;
    min_size: number;
    name: string;
    nodes: Array<{
      availability_zone: string;
      instance_id: string;
      instance_type: string;
      kubelet_version: string;
      name: string;
      ready: boolean;
      status: string;
    }>;
    release_version: string;
    remote_access: boolean;
    status: string;
    subnets: string[];
    tags: Record<string, any>;
    taints_and_labels: {
      labels: Record<string, string>;
    };
  };
}

export const ClusterHealthNodesContent: React.FC<{ accountId?: string }> = ({ accountId }) => {
  const [nodes, setNodes] = useState<Node[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (accountId) {
      apiKubernetes1
        .getClusterHealth(accountId, 'nodes')
        .then((response: any) => {
          if (response?.res?.nodes) {
            setNodes(response.res.nodes);
          }
          setLoading(false);
        })
        .catch(() => {
          setError('Failed to fetch nodes health data');
          setLoading(false);
        });
    }
  }, [accountId]);

  if (loading) {
    return (
      <Box sx={{ p: 2 }}>
        <Skeleton variant='text' width='60%' height={24} />
        <Skeleton variant='rectangular' width='100%' height={200} sx={{ mt: 2 }} />
      </Box>
    );
  }

  if (error) {
    return (
      <Box sx={{ p: 2 }}>
        <Alert severity='error'>{error}</Alert>
      </Box>
    );
  }

  const getNodeHealthStatus = (conditions: NodeCondition[]) => {
    const hasIssues = conditions.some(
      (c) => (c.type === 'MemoryPressure' || c.type === 'DiskPressure' || c.type === 'PIDPressure') && c.status === 'True'
    );
    return hasIssues ? 'Issues' : 'Healthy';
  };

  const tableHeaders = [
    { name: 'Node Name', width: '25%' },
    { name: 'Version', width: '15%' },
    { name: 'Status', width: '10%' },
    { name: 'Conditions', width: '50%' },
  ];

  const tableData = nodes.map((node) => {
    const healthStatus = getNodeHealthStatus(node.conditions);

    return [
      {
        text: node.name,
        component: <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.primary }}>{node.name}</Typography>,
        drilldownQuery: node.nodeGroup ? { nodeName: node.name, nodeGroup: node.nodeGroup } : undefined,
      },
      {
        text: node.version,
        component: <Typography sx={{ fontSize: '12px', color: colors.text.secondary, fontFamily: 'monospace' }}>{node.version}</Typography>,
      },
      {
        text: healthStatus,
        component: <CustomLabels text={healthStatus} />,
      },
      {
        text: node.conditions.map((c) => `${c.type}: ${c.status}`).join(', '),
        component: (
          <Typography sx={{ fontSize: '11px', color: colors.text.secondary }}>
            {node.conditions.map((c) => `${c.type}: ${c.status}`).join(', ')}
          </Typography>
        ),
      },
    ];
  });

  // Count healthy vs unhealthy nodes
  const healthyCounts = nodes.reduce((acc, node) => {
    const status = getNodeHealthStatus(node.conditions);
    acc[status] = (acc[status] || 0) + 1;
    return acc;
  }, {} as Record<string, number>);

  return (
    <Box>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
        <Typography variant='h6' sx={{ fontSize: '16px', fontWeight: 600 }}>
          Node Health ({nodes.length} nodes)
        </Typography>
        <Box sx={{ display: 'flex', gap: 1 }}>
          {Object.entries(healthyCounts).map(([status, count]) => (
            <CustomLabels key={status} text={`${status}: ${count}`} height='18px' />
          ))}
        </Box>
      </Box>

      {nodes.length === 0 ? (
        <Typography variant='body2' color='text.secondary'>
          No nodes found in the cluster.
        </Typography>
      ) : (
        <CustomTable2 tableData={tableData as any} headers={tableHeaders as any} loading={loading} />
      )}
    </Box>
  );
};

interface Instance {
  description: string;
  instance_id: string;
  reason_code: string;
  state: string;
}

interface HealthCounts {
  InService: number;
  OutOfService: number;
  Unknown: number;
}

interface Instances {
  health_counts: HealthCounts;
  healthy_percentage: number;
  instances: Instance[];
  total_instances: number;
}

interface LoadBalancer {
  service_name: string;
  namespace: string;
  type: string;
  hostname: string;
  load_balancer_name: string;
  check_error: string;
  instances: Instances;
}

export const ClusterHealthLoadBalancerContent: React.FC<{ accountId?: string }> = ({ accountId }) => {
  const [loadBalancers, setLoadBalancers] = useState<LoadBalancer[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (accountId) {
      apiKubernetes1
        .getClusterHealth(accountId, 'load_balancer')
        .then((response: any) => {
          if (response?.res?.load_balancers) {
            setLoadBalancers(response.res.load_balancers);
          }
          setLoading(false);
        })
        .catch(() => {
          setError('Failed to fetch load balancer health data');
          setLoading(false);
        });
    }
  }, [accountId]);

  if (loading) {
    return (
      <Box sx={{ p: 2 }}>
        <Skeleton variant='text' width='60%' height={24} />
        <Skeleton variant='rectangular' width='100%' height={200} sx={{ mt: 2 }} />
      </Box>
    );
  }

  if (error) {
    return (
      <Box sx={{ p: 2 }}>
        <Alert severity='error'>{error}</Alert>
      </Box>
    );
  }

  const getLoadBalancerStatus = (healthyPercentage: number) => {
    if (healthyPercentage === 100) {
      return 'healthy';
    }
    if (healthyPercentage >= 50) {
      return 'degraded';
    }
    return 'unhealthy';
  };

  const tableHeaders = [
    { name: 'Service Name', width: '20%' },
    { name: 'Namespace', width: '15%' },
    { name: 'Type', width: '10%' },
    { name: 'Health Status', width: '15%' },
    { name: 'Instances', width: '15%' },
    { name: 'Hostname', width: '25%' },
  ];

  const tableData = loadBalancers.map((lb) => {
    const healthyPercentage = lb.instances?.healthy_percentage ?? 0;
    const status = getLoadBalancerStatus(healthyPercentage);
    const { health_counts } = lb.instances || { health_counts: { InService: 0, OutOfService: 0, Unknown: 0 } };

    return [
      {
        text: lb.service_name,
        component: <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.primary }}>{lb.service_name}</Typography>,
        drilldownQuery: { loadBalancer: lb },
      },
      {
        text: lb.namespace,
        component: <Typography sx={{ fontSize: '12px', color: colors.text.secondary }}>{lb.namespace}</Typography>,
      },
      {
        text: lb.type,
        component: <CustomLabels text={lb.type} />,
      },
      {
        text: status,
        component: <CustomLabels text={status} />,
      },
      {
        text: `${health_counts.InService}/${lb.instances?.total_instances ?? 0}`,
        component: (
          <Typography sx={{ fontSize: '12px', color: colors.text.secondary, fontFamily: 'monospace' }}>
            {health_counts.InService}/{lb.instances?.total_instances ?? 0}
          </Typography>
        ),
      },
      {
        text: lb.hostname,
        component: <Typography sx={{ fontSize: '11px', color: colors.text.secondary, wordBreak: 'break-all' }}>{lb.hostname}</Typography>,
      },
    ];
  });

  const statusCounts = loadBalancers.reduce((acc, lb) => {
    const healthyPercentage = lb.instances?.healthy_percentage ?? 0;
    const status = getLoadBalancerStatus(healthyPercentage);
    acc[status] = (acc[status] || 0) + 1;
    return acc;
  }, {} as Record<string, number>);

  // Component to render expanded instance details
  const LoadBalancerInstancesExpandedContent = ({ loadBalancer }: { loadBalancer: LoadBalancer }) => {
    if (!loadBalancer) {
      return (
        <Box sx={{ p: 2 }}>
          <Typography variant='body2' color='text.secondary'>
            No load balancer data available.
          </Typography>
        </Box>
      );
    }
    const { instances, health_counts } = loadBalancer.instances || { instances: [], health_counts: { InService: 0, OutOfService: 0, Unknown: 0 } };

    const instanceTableHeaders = [
      { name: 'Instance ID', width: '25%' },
      { name: 'State', width: '15%' },
      { name: 'Reason Code', width: '15%' },
      { name: 'Description', width: '45%' },
    ];

    const instanceTableData = instances.map((instance: Instance) => [
      {
        text: instance.instance_id,
        component: (
          <Typography sx={{ fontSize: '12px', fontFamily: 'monospace', color: colors.text.primary, fontWeight: 500 }}>
            {instance.instance_id}
          </Typography>
        ),
      },
      {
        text: instance.state,
        component: <CustomLabels text={instance.state} />,
      },
      {
        text: instance.reason_code === 'N/A' ? '-' : instance.reason_code,
        component: (
          <Typography sx={{ fontSize: '12px', color: colors.text.secondary }}>
            {instance.reason_code === 'N/A' ? '-' : instance.reason_code}
          </Typography>
        ),
      },
      {
        text: instance.description === 'N/A' ? 'Healthy' : instance.description,
        component: (
          <Typography
            sx={{
              fontSize: '11px',
              color: instance.state === 'OutOfService' ? colors.error : instance.description === 'N/A' ? colors.success : colors.text.secondary,
              wordBreak: 'break-word',
              fontStyle: instance.description === 'N/A' ? 'italic' : 'normal',
            }}
          >
            {instance.description === 'N/A' ? 'Healthy' : instance.description}
          </Typography>
        ),
      },
    ]);

    return (
      <Box sx={{ p: 2, backgroundColor: colors.background.white }}>
        {/* Header with key metrics */}
        <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
          <Typography variant='subtitle2' sx={{ fontWeight: 600, color: colors.text.primary }}>
            {loadBalancer.service_name} Instance Details
          </Typography>
          <Box sx={{ display: 'flex', gap: 1 }}>
            <DsChip size='sm' tone='info'>{`${loadBalancer.instances.healthy_percentage.toFixed(1)}% Healthy`}</DsChip>
            <DsChip size='sm' tone='neutral'>{`${health_counts.InService}/${loadBalancer.instances.total_instances} InService`}</DsChip>
          </Box>
        </Box>

        {/* Hostname info */}
        <Box sx={{ mb: 2, p: 1.5, backgroundColor: colors.background.tableHeader, borderRadius: 1 }}>
          <Typography variant='caption' sx={{ color: colors.text.secondary, fontSize: '10px' }}>
            Load Balancer Hostname
          </Typography>
          <Typography variant='body2' sx={{ fontFamily: 'monospace', fontSize: '11px', color: colors.text.primary, wordBreak: 'break-all' }}>
            {loadBalancer.hostname}
          </Typography>
        </Box>

        {/* Instances Table */}
        <CustomTable2 tableData={instanceTableData as any} headers={instanceTableHeaders as any} loading={false} />
      </Box>
    );
  };

  // Check if any load balancers have instances to determine if expandable
  const hasExpandableLoadBalancers = loadBalancers.some((lb) => lb.instances?.instances?.length > 0);

  return (
    <Box>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
        <Typography variant='h6' sx={{ fontSize: '16px', fontWeight: 600 }}>
          Load Balancer Health ({loadBalancers.length} load balancers)
        </Typography>
        <Box sx={{ display: 'flex', gap: 1 }}>
          {Object.entries(statusCounts).map(([status, count]) => (
            <CustomLabels key={status} text={`${status}: ${count}`} height='18px' />
          ))}
        </Box>
      </Box>

      {loadBalancers.length === 0 ? (
        <Typography variant='body2' color='text.secondary'>
          No load balancers found in the cluster.
        </Typography>
      ) : (
        <CustomTable2
          tableData={tableData as any}
          headers={tableHeaders as any}
          loading={loading}
          expandable={
            hasExpandableLoadBalancers
              ? {
                  tabs: [
                    {
                      componentFn: function (_a: any, drilldownQuery: any) {
                        return <LoadBalancerInstancesExpandedContent loadBalancer={drilldownQuery.loadBalancer} />;
                      },
                      text: 'Instance Details',
                    },
                  ],
                }
              : undefined
          }
          showExpandable={hasExpandableLoadBalancers}
        />
      )}
    </Box>
  );
};

interface NodeInGroup {
  name: string;
  instance_id: string;
  status: string;
  instance_type: string;
  availability_zone: string;
  kubelet_version: string;
  ready: boolean;
}

interface TaintsAndLabels {
  labels: Record<string, string>;
}

interface NodeGroup {
  name: string;
  status: string;
  instance_type: string;
  ami_type: string;
  capacity_type: string;
  min_size: number;
  max_size: number;
  desired_size: number;
  disk_size: number;
  kubernetes_version: string;
  release_version: string;
  remote_access: boolean;
  subnets: string[];
  tags: Record<string, string>;
  launch_template: any;
  taints_and_labels: TaintsAndLabels;
  nodes?: NodeInGroup[];
}

export const ClusterHealthNodeGroupsContent: React.FC<{ accountId?: string }> = ({ accountId }) => {
  const [nodeGroups, setNodeGroups] = useState<NodeGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (accountId) {
      apiKubernetes1
        .getClusterHealth(accountId, 'node_groups')
        .then((response: any) => {
          if (response?.res?.node_groups) {
            setNodeGroups(response.res.node_groups);
          }
          setLoading(false);
        })
        .catch(() => {
          setError('Failed to fetch node groups health data');
          setLoading(false);
        });
    }
  }, [accountId]);

  if (loading) {
    return (
      <Box sx={{ p: 2 }}>
        <Skeleton variant='text' width='60%' height={24} />
        <Skeleton variant='rectangular' width='100%' height={200} sx={{ mt: 2 }} />
      </Box>
    );
  }

  if (error) {
    return (
      <Box sx={{ p: 2 }}>
        <Alert severity='error'>{error}</Alert>
      </Box>
    );
  }

  const getNodeGroupStatus = (ng: NodeGroup) => {
    if (ng.status !== 'ACTIVE') {
      return 'inactive';
    }
    if (ng.nodes) {
      const readyNodes = ng.nodes.filter((n) => n.ready).length;
      if (readyNodes === ng.desired_size && ng.desired_size > 0) {
        return 'healthy';
      }
      if (readyNodes < ng.desired_size) {
        return 'degraded';
      }
    }
    return ng.desired_size === 0 ? 'scaled-down' : 'healthy';
  };

  const ExpandableNodesListing = ({ groupName }: { groupName: string }) => {
    const group = nodeGroups.find((ng: any) => ng.name === groupName);

    if (!group || !group.nodes || group.nodes.length === 0) {
      return (
        <Box sx={{ p: 2 }}>
          <Typography variant='body2' color='text.secondary'>
            No nodes found in this group.
          </Typography>
        </Box>
      );
    }

    const nodeTableHeaders = [
      { name: 'Node Name', width: '30%' },
      { name: 'Status', width: '15%' },
      { name: 'Instance Type', width: '15%' },
      { name: 'Availability Zone', width: '15%' },
      { name: 'Kubelet Version', width: '25%' },
    ];

    const nodeTableData = group.nodes.map((node: any) => [
      {
        text: node.name,
        component: <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.primary }}>{node.name}</Typography>,
      },
      {
        text: node.status,
        component: <CustomLabels text={node.ready ? 'Ready' : 'Not Ready'} />,
      },
      {
        text: node.instance_type,
        component: <Typography sx={{ fontSize: '12px', color: colors.text.secondary }}>{node.instance_type}</Typography>,
      },
      {
        text: node.availability_zone,
        component: <Typography sx={{ fontSize: '12px', color: colors.text.secondary }}>{node.availability_zone}</Typography>,
      },
      {
        text: node.kubelet_version,
        component: <Typography sx={{ fontSize: '11px', color: colors.text.secondary, fontFamily: 'monospace' }}>{node.kubelet_version}</Typography>,
      },
    ]);

    return (
      <Box sx={{ p: 2 }}>
        <Typography variant='subtitle2' sx={{ mb: 2, fontWeight: 600 }}>
          Nodes in {groupName} ({group.nodes.length} nodes)
        </Typography>
        <CustomTable2 tableData={nodeTableData as any} headers={nodeTableHeaders as any} loading={false} />
      </Box>
    );
  };

  const tableHeaders = [
    { name: 'Name', width: '20%' },
    { name: 'Status', width: '10%' },
    { name: 'Instance Type', width: '12%' },
    { name: 'Capacity', width: '10%' },
    { name: 'Size (Min/Desired/Max)', width: '18%' },
    { name: 'K8s Version', width: '15%' },
    { name: 'Ready Nodes', width: '15%' },
  ];

  const tableData = nodeGroups.map((ng) => {
    const status = getNodeGroupStatus(ng);
    const readyNodes = ng.nodes ? ng.nodes.filter((n) => n.ready).length : 0;
    const totalNodes = ng.nodes ? ng.nodes.length : 0;

    return [
      {
        text: ng.name,
        component: <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.primary }}>{ng.name}</Typography>,
        drilldownQuery: { name: ng.name },
      },
      {
        text: status,
        component: <CustomLabels text={status} />,
      },
      {
        text: ng.instance_type,
        component: <Typography sx={{ fontSize: '12px', color: colors.text.secondary }}>{ng.instance_type}</Typography>,
      },
      {
        text: ng.capacity_type,
        component: <CustomLabels text={ng.capacity_type} />,
      },
      {
        text: `${ng.min_size}/${ng.desired_size}/${ng.max_size}`,
        component: (
          <Typography sx={{ fontSize: '12px', color: colors.text.secondary, fontFamily: 'monospace' }}>
            {ng.min_size}/{ng.desired_size}/{ng.max_size}
          </Typography>
        ),
      },
      {
        text: ng.kubernetes_version,
        component: <Typography sx={{ fontSize: '11px', color: colors.text.secondary, fontFamily: 'monospace' }}>{ng.kubernetes_version}</Typography>,
      },
      {
        text: `${readyNodes}/${totalNodes}`,
        component: (
          <Typography sx={{ fontSize: '12px', color: colors.text.secondary, fontFamily: 'monospace' }}>
            {readyNodes}/{totalNodes}
          </Typography>
        ),
      },
    ];
  });

  const statusCounts = nodeGroups.reduce((acc, ng) => {
    const status = getNodeGroupStatus(ng);
    acc[status] = (acc[status] || 0) + 1;
    return acc;
  }, {} as Record<string, number>);

  const totalNodes = nodeGroups.reduce((sum, ng) => sum + (ng.nodes?.length || 0), 0);
  const totalReadyNodes = nodeGroups.reduce((sum, ng) => sum + (ng.nodes?.filter((n) => n.ready).length || 0), 0);

  return (
    <Box>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
        <Typography variant='h6' sx={{ fontSize: '16px', fontWeight: 600 }}>
          Node Groups Health ({nodeGroups.length} groups, {totalReadyNodes}/{totalNodes} nodes ready)
        </Typography>
        <Box sx={{ display: 'flex', gap: 1 }}>
          {Object.entries(statusCounts).map(([status, count]) => (
            <CustomLabels key={status} text={`${status}: ${count}`} height='18px' />
          ))}
        </Box>
      </Box>

      {nodeGroups.length === 0 ? (
        <Typography variant='body2' color='text.secondary'>
          No node groups found in the cluster.
        </Typography>
      ) : (
        <CustomTable2
          tableData={tableData as any}
          headers={tableHeaders as any}
          loading={loading}
          expandable={{
            tabs: [
              {
                componentFn: function (_a: any, drilldownQuery: any) {
                  return <ExpandableNodesListing groupName={drilldownQuery.name} />;
                },
                text: 'Nodes Details',
              },
            ],
          }}
        />
      )}
    </Box>
  );
};

interface PersistentVolume {
  name: string;
  claim: string;
  status: string;
}

export const ClusterHealthPvContent: React.FC<{ accountId?: string }> = ({ accountId }) => {
  const [persistentVolumes, setPersistentVolumes] = useState<PersistentVolume[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (accountId) {
      apiKubernetes1
        .getClusterHealth(accountId, 'persistentvolumes')
        .then((response: any) => {
          if (response?.res?.persistentVolumes) {
            setPersistentVolumes(response.res.persistentVolumes);
          }
          setLoading(false);
        })
        .catch(() => {
          setError('Failed to fetch persistent volumes health data');
          setLoading(false);
        });
    }
  }, [accountId]);

  if (loading) {
    return (
      <Box sx={{ p: 2 }}>
        <Skeleton variant='text' width='60%' height={24} />
        <Skeleton variant='rectangular' width='100%' height={200} sx={{ mt: 2 }} />
      </Box>
    );
  }

  if (error) {
    return (
      <Box sx={{ p: 2 }}>
        <Alert severity='error'>{error}</Alert>
      </Box>
    );
  }

  const tableHeaders = [
    { name: 'PV Name', width: '35%' },
    { name: 'Claim', width: '45%' },
    { name: 'Status', width: '20%' },
  ];

  const tableData = persistentVolumes.map((pv) => [
    {
      text: pv.name,
      component: <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.primary }}>{pv.name}</Typography>,
    },
    {
      text: pv.claim,
      component: <Typography sx={{ fontSize: '12px', color: colors.text.secondary }}>{pv.claim}</Typography>,
    },
    {
      text: pv.status,
      component: <CustomLabels text={pv.status} />,
    },
  ]);

  // Count by status for summary
  const statusCounts = persistentVolumes.reduce((acc, pv) => {
    acc[pv.status] = (acc[pv.status] || 0) + 1;
    return acc;
  }, {} as Record<string, number>);

  return (
    <Box>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
        <Typography variant='h6' sx={{ fontSize: '16px', fontWeight: 600 }}>
          Persistent Volumes Health ({persistentVolumes.length} PVs)
        </Typography>
        <Box sx={{ display: 'flex', gap: 1 }}>
          {Object.entries(statusCounts).map(([status, count]) => (
            <CustomLabels key={status} text={`${status}: ${count}`} height='18px' />
          ))}
        </Box>
      </Box>

      {persistentVolumes.length === 0 ? (
        <Typography variant='body2' color='text.secondary'>
          No persistent volumes found in the cluster.
        </Typography>
      ) : (
        <CustomTable2 tableData={tableData as any} headers={tableHeaders as any} loading={loading} />
      )}
    </Box>
  );
};

// Shared utility functions for health metrics calculation
const calculateHealthMetrics = (recommendation: any) => {
  if (!recommendation) {
    return { healthyNodes: 0, totalNodes: 0, healthyWorkloads: 0, totalWorkloads: 0, boundPVs: 0, totalPVs: 0 };
  }

  // Calculate healthy nodes (nodes without memory/disk/PID pressure)
  const nodes = recommendation.nodes || [];
  const healthyNodes = nodes.filter((node: any) => {
    const conditions = node.conditions || [];
    return !conditions.some(
      (c: any) => (c.type === 'MemoryPressure' || c.type === 'DiskPressure' || c.type === 'PIDPressure') && c.status === 'True'
    );
  }).length;

  // Calculate healthy workloads (available replicas match desired replicas)
  const workloads = recommendation.workloads || [];
  const healthyWorkloads = workloads.filter((w: any) => w.available === w.replicas && w.replicas > 0).length;

  // Calculate bound persistent volumes
  const persistentVolumes = recommendation.persistentVolumes || [];
  const boundPVs = persistentVolumes.filter((pv: any) => pv.status === 'Bound').length;

  return {
    healthyNodes,
    totalNodes: nodes.length,
    healthyWorkloads,
    totalWorkloads: workloads.length,
    boundPVs,
    totalPVs: persistentVolumes.length,
  };
};

const getOverallHealthStatus = (metrics: any) => {
  const nodeHealthPercent = metrics.totalNodes > 0 ? (metrics.healthyNodes / metrics.totalNodes) * 100 : 100;
  const workloadHealthPercent = metrics.totalWorkloads > 0 ? (metrics.healthyWorkloads / metrics.totalWorkloads) * 100 : 100;
  const pvHealthPercent = metrics.totalPVs > 0 ? (metrics.boundPVs / metrics.totalPVs) * 100 : 100;

  const avgHealth = (nodeHealthPercent + workloadHealthPercent + pvHealthPercent) / 3;

  if (avgHealth === 100) {
    return { label: 'Healthy', color: '#10B981' };
  }
  if (avgHealth >= 80) {
    return { label: 'Good', color: '#3B82F6' };
  }
  if (avgHealth >= 50) {
    return { label: 'Degraded', color: '#F59E0B' };
  }
  return { label: 'Critical', color: '#EF4444' };
};

interface PreFlightCheckContentProps {
  accountId?: string;
  planId?: string;
}

export const PreFlightCheckContent: React.FC<PreFlightCheckContentProps> = ({ accountId, planId }) => {
  const [loading, setLoading] = useState(false);
  const [fetchingSnapshot, setFetchingSnapshot] = useState(false);
  const [healthCheckData, setHealthCheckData] = useState<any>(null);
  const [currentSnapshot, setCurrentSnapshot] = useState<any>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (accountId && planId) {
      fetchPreFlightSnapshot();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountId, planId]);

  const fetchPreFlightSnapshot = async () => {
    if (!accountId || !planId) {
      return;
    }

    setFetchingSnapshot(true);
    try {
      const response = await apiKubernetes1.getPreFlightCheck(accountId, planId);

      if (response?.data?.data?.recommendation) {
        // Backend now keeps only one record
        const snapshot = Array.isArray(response.data.data.recommendation) ? response.data.data.recommendation[0] : response.data.data.recommendation;
        setCurrentSnapshot(snapshot);
      }
    } catch (err) {
      console.error('Error fetching pre-flight snapshot:', err);
    } finally {
      setFetchingSnapshot(false);
    }
  };

  const handleCapture = async () => {
    if (!accountId || !planId) {
      setError('Missing account ID or plan ID');
      return;
    }

    setLoading(true);
    setError(null);

    try {
      const response = await apiKubernetes1.executeUpgradePreFlightCheck(accountId, planId);

      if (response?.data?.errors && response.data.errors.length > 0) {
        const errorMessages = response.data.errors.map((err: any) => err.message || err).join(', ');
        setError(`Error capturing cluster state: ${errorMessages}`);
      } else if (response?.data?.data?.upgrade_pre_flight_check) {
        const checkData = response.data.data.upgrade_pre_flight_check;
        setHealthCheckData(checkData);
        // Refresh snapshot after capturing
        await fetchPreFlightSnapshot();
      } else {
        setError('No data received from pre-flight check');
      }
    } catch (err) {
      console.error('Error executing pre-flight check:', err);
      setError('Failed to execute pre-flight check');
    } finally {
      setLoading(false);
    }
  };

  const renderHealthCheckSummary = (checkData: any) => {
    if (!checkData?.recommendation) {
      return null;
    }

    const recommendation = checkData.recommendation;
    const nodesCount = recommendation.nodes?.length || 0;
    const workloadsCount = recommendation.workloads?.length || 0;
    const servicesCount = recommendation.services?.length || 0;
    const persistentVolumesCount = recommendation.persistentVolumes?.length || 0;

    const metrics = calculateHealthMetrics(recommendation);
    const healthStatus = getOverallHealthStatus(metrics);

    return (
      <Box sx={{ mt: 3 }}>
        <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
          <Typography variant='h6' sx={{ fontSize: '16px', fontWeight: 600 }}>
            Cluster Health Summary
          </Typography>
          <DsChip size='sm' tone={toHealthTone(healthStatus.label)}>
            {healthStatus.label}
          </DsChip>
        </Box>

        <Box sx={statRowSx}>
          <WidgetCard sx={statCardSx}>
            <Stat size='md' label='Nodes' value={nodesCount} sub={healthySub(`${metrics.healthyNodes}/${metrics.totalNodes} healthy`)} />
          </WidgetCard>
          <WidgetCard sx={statCardSx}>
            <Stat
              size='md'
              label='Workloads'
              value={workloadsCount}
              sub={healthySub(`${metrics.healthyWorkloads}/${metrics.totalWorkloads} healthy`)}
            />
          </WidgetCard>
          <WidgetCard sx={statCardSx}>
            <Stat size='md' label='Services' value={servicesCount} />
          </WidgetCard>
          <WidgetCard sx={statCardSx}>
            <Stat
              size='md'
              label='Persistent Volumes'
              value={persistentVolumesCount}
              sub={healthySub(`${metrics.boundPVs}/${metrics.totalPVs} bound`)}
            />
          </WidgetCard>
        </Box>

        <Alert severity='success' sx={{ mb: 2 }}>
          Pre-flight check snapshot captured successfully
        </Alert>

        {checkData.created_at && (
          <Box sx={{ mt: 2 }}>
            <Typography variant='body2' sx={{ fontSize: '12px', color: colors.text.tertiary, mb: 1 }}>
              Captured: {convertToLocalTime(checkData.created_at)}
            </Typography>
          </Box>
        )}
      </Box>
    );
  };

  return (
    <Box>
      <Box sx={{ mb: 3 }}>
        <Typography variant='body2' sx={{ color: colors.text.secondary, mb: 2 }}>
          Capture the current state of your cluster before proceeding with the upgrade. This will record the health status of all critical resources.
        </Typography>

        <DsButton tone='primary' size='md' onClick={handleCapture} disabled={!hasWriteAccess(accountId)} loading={loading}>
          Capture
        </DsButton>
      </Box>

      {error && (
        <Alert severity='error' sx={{ mb: 2 }}>
          {error}
        </Alert>
      )}

      {loading && (
        <Box sx={{ p: 2 }}>
          <Skeleton variant='text' width='60%' height={24} />
          <Skeleton variant='rectangular' width='100%' height={200} sx={{ mt: 2 }} />
        </Box>
      )}

      {/* Show the current snapshot or the newly captured one */}
      {(() => {
        if (fetchingSnapshot) {
          return (
            <Box sx={{ p: 2, mt: 3 }}>
              <Skeleton variant='text' width='40%' height={24} />
              <Skeleton variant='rectangular' width='100%' height={150} sx={{ mt: 2 }} />
            </Box>
          );
        }

        if (currentSnapshot) {
          return renderHealthCheckSummary(currentSnapshot);
        }

        if (healthCheckData) {
          return renderHealthCheckSummary({ recommendation: healthCheckData.health_check, created_at: new Date().toISOString() });
        }

        return null;
      })()}

      {/* Show message when no snapshot exists */}
      {!fetchingSnapshot && !currentSnapshot && !healthCheckData && !loading && (
        <Box sx={{ mt: 4, p: 3, border: '1px solid #e0e0e0', borderRadius: '8px', textAlign: 'center' }}>
          <Typography sx={{ fontSize: '14px', color: colors.text.tertiary }}>
            No pre-flight snapshot captured yet. Click the Capture button above to create a snapshot.
          </Typography>
        </Box>
      )}
    </Box>
  );
};

// Post-Flight Check Content Component
interface PostFlightCheckProps {
  accountId?: string;
  planId?: string;
}

export const PostFlightCheckContent: React.FC<PostFlightCheckProps> = ({ accountId, planId }) => {
  const [loading, setLoading] = useState(false);
  const [fetchingSnapshot, setFetchingSnapshot] = useState(false);
  const [currentSnapshot, setCurrentSnapshot] = useState<any>(null);
  const [error, setError] = useState<string | null>(null);

  const normalizeHealth = (obj: any) => {
    if (!obj) {
      return null;
    }
    return {
      ...obj,
      persistentVolumes: obj.persistentVolumes ?? obj.persistent_volumes ?? [],
    };
  };

  const normalizeRecommendation = (obj: any) => {
    if (!obj) {
      return null;
    }
    return {
      ...obj,
      persistentVolumes: obj.persistentVolumes ?? obj.persistent_volumes ?? [],
    };
  };

  useEffect(() => {
    if (accountId && planId) {
      fetchPostFlightSnapshot();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountId, planId]);

  const fetchPostFlightSnapshot = async () => {
    if (!accountId || !planId) {
      return;
    }

    setFetchingSnapshot(true);
    try {
      const response = await apiKubernetes1.getPostFlightCheck(accountId, planId);

      const snapshotData = response?.data?.data?.recommendation;

      if (snapshotData) {
        // Backend now keeps only one record
        const snapshot = Array.isArray(snapshotData) ? snapshotData[0] : snapshotData;
        const normalized = {
          ...snapshot,
          recommendation: normalizeRecommendation(snapshot.recommendation),
        };
        setCurrentSnapshot(normalized);
      }
    } catch (err) {
      console.error('Error fetching post-flight snapshot:', err);
    } finally {
      setFetchingSnapshot(false);
    }
  };

  const handleCapture = async () => {
    if (!accountId || !planId) {
      setError('Missing account ID or plan ID');
      return;
    }

    setLoading(true);
    setError(null);

    try {
      const response = await apiKubernetes1.executeUpgradePostFlightCheck(accountId, planId);

      if (response?.data?.errors?.length) {
        const msg = response.data.errors.map((e: any) => e.message).join(', ');
        setError(`Error capturing post-flight data: ${msg}`);
        return;
      }

      const data = response?.data?.data?.upgrade_post_flight_check;

      if (!data) {
        setError('No data returned from post-flight check');
        return;
      }

      // Normalize backend result
      const normalized = {
        ...data,
        health_check: normalizeHealth(data.health_check),
        pre_flight_summary: {
          ...data.pre_flight_summary,
        },
        comparison: {
          ...data.comparison,
        },
      };

      // Set current snapshot to the newly captured check
      setCurrentSnapshot({
        recommendation: normalized.health_check,
        comparison: normalized.comparison,
        pre_flight_summary: normalized.pre_flight_summary,
        created_at: new Date().toISOString(),
      });

      // Refresh snapshot from backend
      await fetchPostFlightSnapshot();
    } catch (err) {
      console.error('Error executing post-flight check:', err);
      setError('Failed to execute post-flight check');
    } finally {
      setLoading(false);
    }
  };

  /**
   * Renders:
   * 1. Comparison Summary
   * 2. Pre-flight Summary
   * 3. Post-flight Cluster Health Summary
   */
  const renderSelectedCheck = (check: any) => {
    if (!check) {
      return null;
    }

    const recommendation = normalizeRecommendation(check.recommendation);
    const metrics = calculateHealthMetrics(recommendation);
    const healthStatus = getOverallHealthStatus(metrics);

    const pre = check.pre_flight_summary;
    const cmp = check.comparison;

    return (
      <Box sx={{ mt: 3 }}>
        {/* ---------- COMPARISON SUMMARY (only for selected, not history) ---------- */}
        {cmp?.summary && (
          <Box
            sx={{
              p: 2,
              border: '1px solid #e0e0e0',
              borderRadius: '8px',
              mb: 3,
            }}
          >
            <Typography
              sx={{
                fontSize: '16px',
                fontWeight: 600,
                mb: 1,
                color: colors.text.primary,
              }}
            >
              Comparison Summary
            </Typography>

            <Typography
              sx={{
                fontSize: '14px',
                color: colors.text.secondary,
                mb: 2,
              }}
            >
              Total Changes: {cmp.summary.total_changes}
            </Typography>

            {/* Improvements */}
            {cmp.summary.improvements?.length > 0 && (
              <Box sx={{ mb: 2 }}>
                <Typography
                  sx={{
                    fontSize: '14px',
                    fontWeight: 600,
                    color: '#10B981',
                    mb: 1,
                  }}
                >
                  Improvements
                </Typography>

                <Box sx={{ pl: 2 }}>
                  {cmp.summary.improvements.map((item: string, i: number) => (
                    <Typography
                      key={i}
                      sx={{
                        fontSize: '13px',
                        color: '#10B981',
                        mb: 0.5,
                      }}
                    >
                      • {item}
                    </Typography>
                  ))}
                </Box>
              </Box>
            )}

            {/* Degradations */}
            {cmp.summary.degradations?.length > 0 && (
              <Box sx={{ mb: 1 }}>
                <Typography
                  sx={{
                    fontSize: '14px',
                    fontWeight: 600,
                    color: '#EF4444',
                    mb: 1,
                  }}
                >
                  Degradations
                </Typography>

                <Box sx={{ pl: 2 }}>
                  {cmp.summary.degradations.map((item: string, i: number) => (
                    <Typography
                      key={i}
                      sx={{
                        fontSize: '13px',
                        color: '#EF4444',
                        mb: 0.5,
                      }}
                    >
                      • {item}
                    </Typography>
                  ))}
                </Box>
              </Box>
            )}
          </Box>
        )}

        {/* ---------- PRE-FLIGHT SUMMARY ---------- */}
        {pre && (
          <Box
            sx={{
              p: 2,
              border: '1px solid #e0e0e0',
              borderRadius: '8px',
              mb: 3,
            }}
          >
            <Typography variant='h6' sx={{ fontSize: '16px', fontWeight: 600, mb: 2 }}>
              Pre-Flight Summary
            </Typography>

            <Box sx={statRowSx}>
              <WidgetCard sx={statCardSx}>
                <Stat size='md' label='Nodes' value={pre.nodes_count} />
              </WidgetCard>
              <WidgetCard sx={statCardSx}>
                <Stat size='md' label='Workloads' value={pre.workloads_count} />
              </WidgetCard>
              <WidgetCard sx={statCardSx}>
                <Stat size='md' label='Services' value={pre.services_count} />
              </WidgetCard>
              <WidgetCard sx={statCardSx}>
                <Stat size='md' label='Persistent Volumes' value={pre.persistent_volumes_count} />
              </WidgetCard>
            </Box>
          </Box>
        )}

        {/* ---------- POST-FLIGHT HEALTH SUMMARY ---------- */}
        <Box>
          <Box
            sx={{
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
              mb: 2,
            }}
          >
            <Typography variant='h6' sx={{ fontSize: '16px', fontWeight: 600 }}>
              Cluster Health Summary (Post-flight)
            </Typography>
            <DsChip size='sm' tone={toHealthTone(healthStatus.label)}>
              {healthStatus.label}
            </DsChip>
          </Box>

          <Alert severity='success' sx={{ mb: 2 }}>
            Post-flight check snapshot captured successfully
          </Alert>

          <Box sx={statRowSx}>
            <WidgetCard sx={statCardSx}>
              <Stat
                size='md'
                label='Nodes'
                value={recommendation.nodes?.length || 0}
                sub={healthySub(`${metrics.healthyNodes}/${metrics.totalNodes} healthy`)}
              />
            </WidgetCard>
            <WidgetCard sx={statCardSx}>
              <Stat
                size='md'
                label='Workloads'
                value={recommendation.workloads?.length || 0}
                sub={healthySub(`${metrics.healthyWorkloads}/${metrics.totalWorkloads} healthy`)}
              />
            </WidgetCard>
            <WidgetCard sx={statCardSx}>
              <Stat size='md' label='Services' value={recommendation.services?.length || 0} />
            </WidgetCard>
            <WidgetCard sx={statCardSx}>
              <Stat
                size='md'
                label='Persistent Volumes'
                value={recommendation.persistentVolumes?.length || 0}
                sub={healthySub(`${metrics.boundPVs}/${metrics.totalPVs} bound`)}
              />
            </WidgetCard>
          </Box>
          {check.created_at && (
            <Typography
              sx={{
                fontSize: '12px',
                color: colors.text.tertiary,
                mb: 1,
                mt: 2,
              }}
            >
              Captured: {convertToLocalTime(check.created_at)}
            </Typography>
          )}
        </Box>
      </Box>
    );
  };

  return (
    <Box>
      {/* Capture button */}
      <Box sx={{ mb: 3 }}>
        <Typography variant='body2' sx={{ color: colors.text.secondary, mb: 2 }}>
          Capture the current state of your cluster after finishing the upgrade. A comparison with your pre-flight snapshot will be generated.
        </Typography>

        <DsButton tone='primary' size='md' onClick={handleCapture} disabled={!hasWriteAccess(accountId)} loading={loading}>
          Capture
        </DsButton>
      </Box>

      {error && (
        <Alert severity='error' sx={{ mb: 2 }}>
          {error}
        </Alert>
      )}

      {loading && (
        <Box sx={{ p: 2 }}>
          <Skeleton variant='text' width='60%' height={24} />
          <Skeleton variant='rectangular' width='100%' height={200} sx={{ mt: 2 }} />
        </Box>
      )}

      {/* Show the current snapshot */}
      {(() => {
        if (fetchingSnapshot) {
          return (
            <Box sx={{ p: 2, mt: 3 }}>
              <Skeleton variant='text' width='40%' height={24} />
              <Skeleton variant='rectangular' width='100%' height={150} sx={{ mt: 2 }} />
            </Box>
          );
        }

        if (currentSnapshot) {
          return renderSelectedCheck(currentSnapshot);
        }

        if (!loading) {
          return (
            <Box sx={{ mt: 4, p: 3, border: '1px solid #e0e0e0', borderRadius: '8px', textAlign: 'center' }}>
              <Typography sx={{ fontSize: '14px', color: colors.text.tertiary }}>
                No post-flight snapshot captured yet. Click the Capture button above to create a snapshot.
              </Typography>
            </Box>
          );
        }

        return null;
      })()}
    </Box>
  );
};
