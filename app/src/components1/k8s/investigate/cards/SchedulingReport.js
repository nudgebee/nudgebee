import CustomTable2 from '@components1/common/tables/CustomTable2';
import CubeIcon from '@assets/kubernetes/cube-icon.svg';
import { getTableData } from './util';

class SchedulingReport {
  constructor() {
    this.id = 'SchedulingReport';
    this.icon = CubeIcon;
    this.text = 'Scheduling Report';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.schedulingReport = {};
  }

  canRenderContent = async (evidenceData, _event) => {
    const filterTableType = evidenceData.filter((item) => item.type === 'table' && item.data.table_name === 'table');
    if (filterTableType && filterTableType.length > 0) {
      let t = filterTableType[0];
      const { headers, convertedJson2, tableInsight } = getTableData(t);
      this.renderContent = true;
      const obj = {};
      t.data.rows.forEach((innerArray) => {
        const [key, value] = innerArray;
        obj[key] = value;
      });
      this.schedulingReport = {
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
    return [() => this.renderSchedulingReport(this.schedulingReport)];
  };

  renderSchedulingReport = (sr) => {
    return <CustomTable2 tableData={sr?.tableData} headers={sr?.headers} totalRows={sr?.tableData?.length} rowsPerPage={sr?.tableData?.length} />;
  };
}

export default SchedulingReport;
