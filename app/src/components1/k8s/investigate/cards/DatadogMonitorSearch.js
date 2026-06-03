import CustomTable2 from '@common-new/tables/CustomTable2';
import { safeJSONParse } from 'src/utils/common';
import { DatadogIcon } from '@assets';
import Datetime from '@common-new/format/Datetime';
import Text from '@common-new/format/Text';

class DatadogMonitorSearch {
  constructor(data, index) {
    this.id = `TableCard_${index}`;
    this.text = 'Datadog Monitors';
    this.icon = DatadogIcon;
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.enricherData = data;
    this.tableData = {};
    this.disabled = data?.additional_info?.status == 'skipped';
  }

  canRenderContent = async () => {
    if (this.enricherData) {
      const data = safeJSONParse(this.enricherData.data);
      if (data?.monitors?.length) {
        const tableData = data?.monitors?.map((g) => {
          return [
            {
              text: g.name,
            },
            {
              component: <Datetime value={g.last_triggered} />,
            },
            {
              component: (
                <>
                  {g.metrics?.length > 0 && (
                    <Text
                      sx={{
                        '@media(max-width: 1425px)': {
                          fontSize: 'var(--ds-text-small)',
                        },
                        '@media(max-width: 1100px)': {
                          fontSize: 'var(--ds-text-caption)',
                        },
                      }}
                      showAutoEllipsis
                      value={`metrics: ${g.metrics?.join(', ')}`}
                    />
                  )}
                  {g.query && (
                    <Text
                      secondaryText
                      sx={{
                        '@media(max-width: 1425px)': {
                          fontSize: 'var(--ds-text-small)',
                        },
                        '@media(max-width: 1100px)': {
                          fontSize: 'var(--ds-text-caption)',
                        },
                      }}
                      value={`Query: ${g.query}`}
                    />
                  )}
                </>
              ),
            },
          ];
        });
        this.tableData = {
          headers: ['Monitor Name', 'Last Triggered', 'Other Details'],
          tableData,
        };
        this.renderContent = true;
      }
      if (this.enricherData?.insight && this.enricherData?.insight.length > 0) {
        this.insightData = this.enricherData.insight;
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

export default DatadogMonitorSearch;
