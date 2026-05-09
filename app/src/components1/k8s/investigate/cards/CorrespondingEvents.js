import { extractWorkloadName, safeJSONParse } from 'src/utils/common';
import { AllEventsIconBlack } from '@assets';
import KubernetesEventsTable from '@components1/events/KubernetesEvents';
import apiKubernetes from '@api1/kubernetes';
import { formatDate } from '@lib/formatter';
import React, { useState, useEffect } from 'react';

class CorrespondingEvents {
  constructor() {
    this.id = 'CorrespondingEvents';
    this.icon = AllEventsIconBlack;
    this.text = 'Corresponding Events';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.impactedAPIData = {};
    this.event = {};
    this.workloadNames = [];
    this.namespaces = [];
    this.isFetching = false;
  }

  canRenderContent = async (evidenceData, event) => {
    this.event = event;
    if (evidenceData.canTracesRender) {
      const filteredData = evidenceData.find((item) => item.type === 'json' && safeJSONParse(item.data)?.name === 'api_traces_enricher');
      if (filteredData) {
        const tracesData = JSON.parse(filteredData.data);
        let namespaceAndWorkload = tracesData.data.map((g) => ({
          namespace: g.workload_namespace || g.ResourceAttributes['k8s.namespace.name'] || '',
          workload: g.workload_name || g.ResourceAttributes['k8s.deployment.name'] || '',
        }));
        this.namespaces = [...new Set(namespaceAndWorkload.map((g) => g.namespace).filter((ns) => ns))];
        this.workloadNames = [...new Set(namespaceAndWorkload.map((g) => g.workload).filter((wl) => wl))];
      }
      if (this.namespaces.length > 0 && this.workloadNames.length > 0) {
        this.renderContent = true;
      }
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.getCorrespondingEvents()];
  };

  getCorrespondingEvents = () => {
    const { event } = this;

    const CorrespondingEventsComponent = () => {
      const [isDataReady, setIsDataReady] = useState(false);
      const [hasError, setHasError] = useState(false);
      const [namespaces, setNamespaces] = useState(this.namespaces);
      const [workloadNames, setWorkloadNames] = useState(this.workloadNames);
      const [isFetching, setIsFetching] = useState(false);

      const getServiceMapFromRelay = (data) => {
        if (isFetching) {
          return;
        }
        setIsFetching(true);

        const tempNamespaces = [];
        const tempWorkloads = [];
        if (this.namespaces && this.workloadNames) {
          setIsDataReady(true);
          setIsFetching(false);
        } else {
          apiKubernetes
            .relayForwardRequest(data)
            .then((res) => {
              if (res?.data?.data) {
                const filterMonitoringAndControlPlane = res?.data?.data
                  .filter((d) => d.Category?.category != 'monitoring' && d.Category?.category != 'control-plane')
                  .map((e) => ({
                    ...e,
                    Status: e.Status || 'ok',
                  }));

                if (filterMonitoringAndControlPlane?.length > 0) {
                  const filterWorkload =
                    filterMonitoringAndControlPlane.filter(
                      (g) =>
                        g.Id.namespace == data.body.action_params.workload_filter.workload_namespace &&
                        g.Id.name == data.body.action_params.workload_filter.workload_name
                    ) || [];

                  if (filterWorkload.length > 0) {
                    const upstreams = filterWorkload[0].Upstreams;
                    upstreams.forEach((item) => {
                      const parts = item.Id.split(':');
                      const namespace = parts[0] || null;
                      const workload = parts[2] || null;
                      if (namespace) {
                        tempNamespaces.push(namespace);
                      }
                      if (workload) {
                        tempWorkloads.push(workload);
                      }
                    });

                    tempNamespaces.push(data.body.action_params.workload_filter.workload_namespace);
                    tempWorkloads.push(data.body.action_params.workload_filter.workload_name);

                    setNamespaces(tempNamespaces);
                    setWorkloadNames(tempWorkloads);
                    setIsDataReady(true);
                    setIsFetching(false);
                    return;
                  }
                }
              }
              setHasError(true);
              setIsFetching(false);
            })
            .catch((error) => {
              console.error('Error fetching service map data:', error);
              setHasError(true);
              setIsFetching(false);
            });
        }
      };

      useEffect(() => {
        const data = {
          no_sinks: true,
          body: {
            account_id: event.cloud_account_id,
            action_name: 'service_map',
            action_params: {
              r_start_time: formatDate(new Date(new Date(event.starts_at + 'Z').getTime() - 15 * 60 * 1000)),
              r_end_time: formatDate(new Date(new Date(event.starts_at + 'Z').getTime() + 15 * 60 * 1000)),
            },
          },
          cache: false,
        };

        if (event.subject_namespace && event.subject_name) {
          data.body.action_params['workload_filter'] = {
            workload_name: extractWorkloadName(event.subject_name),
            workload_namespace: event.subject_namespace,
          };
          getServiceMapFromRelay(data);
        } else {
          setHasError(true);
        }
      }, []);

      if (hasError) {
        return <div className='p-4 text-center'>No data available at this time</div>;
      }

      if (isDataReady) {
        return (
          <KubernetesEventsTable
            accountId={event.cloud_account_id}
            enableTrendChart={false}
            enableFilters={false}
            heading={''}
            defaultQuery={{
              namespace: namespaces,
              workloadName: workloadNames,
              startTime: new Date(new Date(event.starts_at + 'Z').getTime() - 15 * 60 * 1000),
              endTime: new Date(new Date(event.starts_at + 'Z').getTime() + 15 * 60 * 1000),
            }}
            disabledFilters={['subjectType', 'namespace', 'workload', 'aggregationKey', 'source']}
            stickyColumnIndex={'6'}
            showTimeFilter={false}
          />
        );
      }

      return <div>Loading...</div>;
    };

    return <CorrespondingEventsComponent />;
  };
}

export default CorrespondingEvents;
