import CustomTable2 from '@components1/common/tables/CustomTable2';
import { getTableData } from './util';
import { titleCase } from '@lib/formatter';
import LogsIcon from '@assets/investigation/logs-blue.svg';

class ShowingTableCard {
  constructor(data, index) {
    this.id = `TableCard_${index}`;
    this.text = titleCase(data?.data?.table_name?.replaceAll(':', '')?.replaceAll('*', '') || '') || `Diagnostic Summary`;
    this.icon = LogsIcon;
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.enricherData = data;
    this.tableData = {};
    this.disabled = data?.additional_info?.status == 'skipped';
  }

  canRenderContent = async () => {
    if (this.enricherData) {
      let t = this.enricherData;
      const { headers, convertedJson2, tableInsight } = getTableData(t);
      this.renderContent = true;
      const obj = {};
      t.data.rows.forEach((innerArray) => {
        const [key, value] = innerArray;
        obj[key] = value;
      });
      this.tableData = {
        headers: headers,
        tableData: convertedJson2,
      };
      if (t?.insight && t?.insight.length > 0) {
        this.insightData = t.insight;
      } else if (tableInsight) {
        this.insightData = tableInsight;
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

export default ShowingTableCard;
