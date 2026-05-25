import CustomTable2 from '@components1/common/tables/CustomTable2';
import { titleCase } from '@lib/formatter';
import LogsIcon from '@assets/investigation/logs-blue.svg';
import { safeJSONParse } from 'src/utils/common';
import { getTableData2 } from '@components1/k8s/investigate/cards/util';

class CloudLog {
  constructor(data, _event) {
    this.id = `CloudLog_${_event}`;
    this.text = titleCase(data?.additional_info?.title) || 'Cloud Logs';
    this.icon = LogsIcon;
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.enricherData = data;
    this.tableData = {};
    this.disabled = data?.additional_info?.status == 'skipped';
  }

  canRenderContent = async () => {
    this.renderContent = false;
    const isCloudLogData = this.enricherData?.additional_info?.action_name === 'cloud_logs';
    if (isCloudLogData) {
      const serverLogParsedData = safeJSONParse(this.enricherData.data);
      if (serverLogParsedData) {
        const logsData = serverLogParsedData.data.map(({ timestamp, message }) => ({
          timestamp,
          message,
        }));
        const { headers, convertedJson2, tableInsight } = getTableData2(logsData, true);
        this.tableData = {
          headers: headers,
          tableData: convertedJson2,
        };
        // Merge insights from both evidence and table parsing
        this.insightData = [];
        if (this.enricherData?.insight) {
          this.insightData.push(...this.enricherData.insight);
        }
        if (tableInsight) {
          this.insightData.push(...tableInsight);
        }
        this.renderContent = true;
      }
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderTableData(this.tableData)];
  };

  renderTableData = (tableData) => {
    return (
      <CustomTable2
        tableData={tableData?.tableData}
        headers={tableData?.headers}
        totalRows={tableData?.tableData?.length}
        rowsPerPage={tableData?.tableData?.length}
      />
    );
  };
}

export default CloudLog;
