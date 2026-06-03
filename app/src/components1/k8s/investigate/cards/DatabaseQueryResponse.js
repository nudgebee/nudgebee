import CustomTable2 from '@common-new/tables/CustomTable2';
import CubeIcon from '@assets/kubernetes/cube-icon.svg';
import { getTableData2 } from './util';

class DatabaseQueryResponse {
  constructor() {
    this.id = 'DatabaseQueryResponse';
    this.icon = CubeIcon;
    this.text = 'Database Response';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.databaseQueryResponse = [];
  }

  canRenderContent = async (evidenceData, _event) => {
    const filterDatabaseResponseType = evidenceData.find((item) => {
      try {
        const parsedData = typeof item.data === 'string' ? JSON.parse(item.data) : item.data;
        return parsedData?.type === 'database_query_response';
      } catch {
        return {};
      }
    });
    if (filterDatabaseResponseType && Object.keys(filterDatabaseResponseType).length > 0) {
      let t = filterDatabaseResponseType.data;
      if (t) {
        try {
          const parsedJson = JSON.parse(t);
          if (parsedJson.response?.length > 0) {
            for (const res of parsedJson.response) {
              const { headers, convertedJson2 } = getTableData2(res.response);
              if (headers) {
                this.renderContent = true;
                this.databaseQueryResponse.push({
                  query: res.query,
                  headers: headers,
                  tableData: convertedJson2,
                });
              }
            }
          }
        } catch {
          this.renderContent = false;
          return false;
        }
      }
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderDatabaseQueryResponse(this.databaseQueryResponse)];
  };

  renderDatabaseQueryResponse = (databaseQueryResponse) => {
    return (
      <>
        {databaseQueryResponse.map((dqr, index) => (
          <div key={index}>
            <span style={{ fontWeight: 'var(--ds-font-weight-semibold)' }}>Executed Query: {dqr.query}</span>
            <CustomTable2 tableData={dqr?.tableData} headers={dqr?.headers} totalRows={dqr?.tableData?.length} rowsPerPage={dqr?.tableData?.length} />
          </div>
        ))}
      </>
    );
  };
}

export default DatabaseQueryResponse;
