import CpuUsageIcon from '@assets/kubernetes/cpu-usage.svg';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import { formatBytes } from 'src/utils/common';
import { getTableData1 } from './util';

class ClusterMemoryRequestsSummary {
  constructor() {
    this.id = 'ClusterMemoryRequestsSummary';
    this.icon = CpuUsageIcon;
    this.text = 'Memory Allocated By Pods';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.clusterMemorySummary = {};
  }

  canRenderContent = async (evidenceData, event) => {
    if (event.aggregation_key !== 'KubeMemoryOvercommit') {
      this.renderContent = false;
      return this.renderContent;
    }
    const filterJSONType = evidenceData.filter((item) => item.type === 'json') ?? [];
    let clusterRequestSummary = {};
    if (filterJSONType.length > 0) {
      for (const element of filterJSONType) {
        const jsonParsed = JSON.parse(element.data);
        if (jsonParsed.name == 'cluster_memory_requests_summary') {
          clusterRequestSummary = jsonParsed;
          break;
        }
      }
    }

    if (clusterRequestSummary.data && clusterRequestSummary.data.pods?.length > 0) {
      const getHeaders = Object.keys(clusterRequestSummary.data.pods[0]);
      const { headers, convertedJson2 } = getTableData1({
        data: {
          headers: getHeaders,
          rows: clusterRequestSummary.data.pods
            .sort((a, b) => {
              const memoryA = parseFloat(a.memory_requested, 10) || 0;
              const memoryB = parseFloat(b.memory_requested, 10) || 0;
              return memoryB - memoryA;
            })
            .map((p) => ({
              ...p,
              memory_requested: p.memory_requested ? formatBytes(parseFloat(p.memory_requested)) : '-',
            })),
        },
      });
      this.clusterMemorySummary = {
        headers: headers,
        tableData: convertedJson2,
      };
      this.renderContent = true;
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [
      () => (
        <CustomTable2
          tableData={this.clusterMemorySummary?.tableData}
          headers={this.clusterMemorySummary?.headers}
          totalRows={this.clusterMemorySummary?.tableData?.length}
          rowsPerPage={this.clusterMemorySummary?.tableData?.length}
        />
      ),
    ];
  };
}

export default ClusterMemoryRequestsSummary;
