import CustomTable2 from '@common-new/tables/CustomTable2';
import ApiFailureIcon from '@assets/investigation/api-failure.svg';

class ApiFailureCard {
  constructor() {
    this.id = 'ApiFailureCard';
    this.icon = ApiFailureIcon;
    this.text = 'Api failure';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.apiData = {};
  }

  canRenderContent = async (evidenceData, event) => {
    const filterTableType = evidenceData.filter((item) => item.type === 'json');
    if (filterTableType && filterTableType.length > 0) {
      for (let e of filterTableType) {
        if (!e?.data || typeof e.data !== 'string') {
          continue;
        }
        try {
          let data = JSON.parse(e.data);
          if (data?.name === 'api_failure_enricher') {
            this.apiData = data;
            this.renderContent = true;
            this.event = event;
            this.insightData.push({ message: `Api Failed with status ${data.status} on endpoint ${data.path}` });
          }
        } catch {
          // do nothing
        }
      }
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderAlertLabels(this.apiData)];
  };

  renderAlertLabels = (apiData) => {
    let tableRows = Object.entries(apiData).map(([key, value]) => {
      return [
        {
          text: key,
        },
        {
          text: value,
        },
      ];
    });
    return <CustomTable2 tableData={tableRows} headers={['Name', 'Value']} totalRows={tableRows.length} rowsPerPage={tableRows.length} />;
  };
}

export default ApiFailureCard;
