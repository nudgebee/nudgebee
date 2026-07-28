import type { JSX } from 'react';

export interface ICustomTableRow {
  component?: JSX.Element;
  drilldownQuery?: {
    podName?: any;
    workloadName?: any;
    namespaceName?: any;
    cpuRecc?: string;
    cpuReq?: string;
    memoryReq?: string;
    memoryRecc?: string;
    resourceId?: any;
    memLimit?: string | undefined;
    cpuLimit?: any;
    recommendation?: any;
    recommenedationDetails?: any;
    event?: any;
  };
  text?: any;
  data?: any;
}
