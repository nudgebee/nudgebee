import CustomTable2 from '@common-new/tables/CustomTable2';
import { getTableData3 } from './util';
import { titleCase } from '@lib/formatter';
import LogsIcon from '@assets/investigation/logs-blue.svg';
import { safeJSONParse, snakeToTitleCase } from 'src/utils/common';
import { ArgocdIcon } from '@assets';

class ShowingObjectCard {
  constructor(evidenceData, _event, index) {
    this.id = `ShowingObjectCard_${index}`;
    this.text =
      titleCase(evidenceData?.additional_info?.title || snakeToTitleCase(evidenceData?.additional_info?.action_name)) || `Diagnostic Summary`;
    this.icon = evidenceData?.additional_info?.action_name == 'argocd_app_history' ? ArgocdIcon : LogsIcon;
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.enricherData = evidenceData;
    this.tableData = {};
    this.disabled = evidenceData?.additional_info?.status == 'skipped';
  }

  canRenderContent = async () => {
    if (this.enricherData) {
      const parsedData = safeJSONParse(this.enricherData.data);
      if (parsedData) {
        const { headers, convertedJson2 } = getTableData3(parsedData);
        this.renderContent = true;
        this.tableData = {
          headers: headers?.map((g) => snakeToTitleCase(g)),
          tableData: convertedJson2,
        };
        if (this.enricherData?.insight?.length > 0) {
          this.insightData = this.enricherData.insight;
        }
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

export default ShowingObjectCard;
