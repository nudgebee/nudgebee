import CubeIcon from '@assets/kubernetes/cube-icon.svg';
import { Box, Typography } from '@mui/material';
import CustomTable2 from '@common-new/tables/CustomTable2';
import { LineChart } from '@components1/common';
import Text from '@common-new/format/Text';
import { ds } from '@utils/colors';

class PerformanceInsightsCard {
  constructor(evidenceData, index) {
    this.id = `PerformanceInsights_${index}`;
    this.text = evidenceData?.additional_info?.title || 'Performance Insights';
    this.icon = CubeIcon;
    this.enricherData = evidenceData;
    this.performanceData = null;
  }

  async canRenderContent() {
    if (!this.enricherData?.data) {
      return false;
    }

    try {
      const data = typeof this.enricherData.data === 'string' ? JSON.parse(this.enricherData.data) : this.enricherData.data;
      this.performanceData = data;

      return !!(data?.performance_insights_enabled && (data.top_sql?.length > 0 || data.wait_events?.length > 0 || data.metrics?.length > 0));
    } catch (e) {
      console.error('Error parsing performance insights data:', e);
      return false;
    }
  }

  getHighLightsData = () => {
    return this.enricherData?.insight || [];
  };

  getContentComponents = () => {
    return [() => this.renderCardContent()];
  };

  renderCardContent = () => {
    if (!this.performanceData) {
      return null;
    }

    return (
      <Box sx={{ p: ds.space[4], display: 'flex', flexDirection: 'column', gap: ds.space[5] }}>
        {/* DB Instance Info */}
        <Box>
          <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>
            Database Instance
          </Typography>
          <Text value={this.performanceData.db_instance_identifier || 'N/A'} secondaryText />
        </Box>

        {/* Top SQL Queries */}
        {this.performanceData.top_sql?.length > 0 && (
          <Box>
            <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>
              Top SQL Queries by DB Load
            </Typography>
            {this.renderTopSqlTable()}
          </Box>
        )}

        {/* Wait Events */}
        {this.performanceData.wait_events?.length > 0 && (
          <Box>
            <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>
              Wait Events
            </Typography>
            {this.renderWaitEventsTable()}
          </Box>
        )}

        {/* DB Load Metrics */}
        {this.performanceData.metrics?.length > 0 && (
          <Box>
            <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>
              DB Load Average (AAS)
            </Typography>
            {this.renderMetricsChart()}
          </Box>
        )}
      </Box>
    );
  };

  renderTopSqlTable = () => {
    const tableRows = this.performanceData.top_sql.slice(0, 10).map((sql) => {
      const sqlText = sql.sql_text || 'N/A';

      return [
        {
          component: (
            <Text
              value={sqlText}
              sx={{
                fontSize: ds.text.small,
                fontFamily: 'monospace',
                maxWidth: '600px',
                wordBreak: 'break-word',
              }}
            />
          ),
        },
        {
          component: <Text value={sql.db_load?.toFixed(4) || 'N/A'} sx={{ fontSize: ds.text.small }} />,
        },
      ];
    });

    return (
      <CustomTable2
        headers={['SQL Query', 'DB Load']}
        tableData={tableRows}
        rowsPerPage={Math.min(this.performanceData.top_sql.length, 10)}
        totalRows={tableRows.length}
      />
    );
  };

  renderWaitEventsTable = () => {
    const tableRows = this.performanceData.wait_events
      .sort((a, b) => b.db_load - a.db_load)
      .map((event) => {
        return [
          {
            component: <Text value={event.event_type || 'N/A'} sx={{ fontSize: ds.text.small }} />,
          },
          {
            component: <Text value={event.db_load?.toFixed(4) || 'N/A'} sx={{ fontSize: ds.text.small }} />,
          },
          {
            component: <Text value={event.percentage != null ? `${event.percentage.toFixed(2)}%` : 'N/A'} sx={{ fontSize: ds.text.small }} />,
          },
        ];
      });

    return (
      <CustomTable2
        headers={['Event Type', 'DB Load', 'Percentage']}
        tableData={tableRows}
        rowsPerPage={Math.min(this.performanceData.wait_events.length, 10)}
        totalRows={tableRows.length}
      />
    );
  };

  renderMetricsChart = () => {
    const metric = this.performanceData.metrics[0];
    if (!metric) {
      return null;
    }

    // Prepare labels (timestamps) and data (values)
    const labels = metric.timestamps.map((timestamp) => {
      const date = new Date(timestamp * 1000);
      return date.toLocaleTimeString();
    });

    const dataset = [
      {
        borderColor: '#2563EB',
        backgroundColor: 'rgba(37, 99, 235, 0.1)',
        data: metric.values.map((val) => parseFloat(val?.toFixed(2) || 0)),
        label: `${metric.name} (${metric.unit})`,
        fill: true,
      },
    ];

    return (
      <Box sx={{ height: '300px', width: '100%' }}>
        <LineChart labels={labels} dataset={dataset} />
      </Box>
    );
  };
}

export default PerformanceInsightsCard;
