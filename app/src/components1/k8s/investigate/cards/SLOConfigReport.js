import { SLOInspectionBlackIcon } from '@assets';
import { getTableData2 } from './util';
import CustomTable from '@components1/common/tables/CustomTable2';
import { Typography } from '@mui/material';
import { colors } from 'src/utils/colors';

class SLOConfigReport {
  constructor() {
    this.id = 'SLOConfigReport';
    this.event = {};
    this.sloConfig = {};
    this.sloReport = {};
    this.resolveButton = false;
    this.text = 'SLO Report';
    this.insightData = [];
    this.renderContent = false;
    this.icon = SLOInspectionBlackIcon;
    this.tableData = {
      headers: [],
      data: [],
    };
    this.totalErrorBudget = '';
  }

  extractSLOMetrics = (sloReport, sloConfig) => {
    const totalErrorBudgetMinutes = (1 - sloConfig.goal) * (sloConfig.window / 60);
    const totalErrorBudgetSeconds = totalErrorBudgetMinutes * 60;

    const totalErrorBudgetMessage = `This means that the service can only afford ${Math.round(
      totalErrorBudgetSeconds
    )} seconds of errors per hour before it violates the SLO.`;

    const keyMetrics = [
      {
        metric: 'SLO Goal',
        value: `${(sloConfig.goal * 100).toFixed(2)}%`,
        description: 'The target percentage of successful requests over the monitoring window.',
      },
      {
        metric: 'SLO Status',
        value: sloReport.status,
        description: 'Current state of the SLO evaluation (e.g., FIRING if threshold is breached).',
      },
      {
        metric: 'SLI Measurement',
        value: sloReport.sli_measurement,
        description: 'Service Level Indicator (SLI), representing the actual service performance.',
      },
      {
        metric: 'Error Budget Target',
        value: sloReport.error_budget_target.toFixed(4),
        description: 'Maximum fraction of time the service can be in error without violating the SLO.',
      },
      {
        metric: 'Error Budget Minutes',
        value: sloReport.error_budget_minutes.toFixed(2),
        description: 'Allocated minutes of allowable errors within the monitoring window.',
      },
      {
        metric: 'Error Budget Burn Rate',
        value: sloReport.error_budget_burn_rate.toFixed(1),
        description: 'Rate at which the service is consuming the error budget. A high rate indicates issues.',
      },
      {
        metric: 'Error Budget Remaining Minutes',
        value: sloReport.error_budget_remaining_minutes.toFixed(2),
        description: 'Remaining time before the service violates the error budget.',
      },
      {
        metric: 'Error Budget Consumed Ratio',
        value: sloReport.error_budget_consumed_ratio.toFixed(2),
        description: 'Percentage of the error budget that has been used.',
      },
      {
        metric: 'Total Events Count',
        value: sloReport.events_count,
        description: 'Total number of events observed in the monitoring window.',
      },
      {
        metric: 'Good Events Count',
        value: sloReport.good_events_count,
        description: 'Number of successful events that met the SLO criteria.',
      },
      {
        metric: 'Bad Events Count',
        value: sloReport.bad_events_count,
        description: 'Number of failed events that did not meet the SLO criteria.',
      },
      {
        metric: 'Workload Name',
        value: sloReport.workload_name,
        description: 'The name of the monitored workload.',
      },
      {
        metric: 'Workload Namespace',
        value: sloReport.workload_namespace,
        description: 'The namespace where the workload is running.',
      },
    ];

    return { keyMetrics, totalErrorBudget: totalErrorBudgetMessage };
  };

  canRenderContent = async (evidenceData, event) => {
    this.event = event;
    const sloConfig = evidenceData.find((item) => item.name === 'SLO');
    if (sloConfig) {
      this.sloConfig = sloConfig.SLOConfig;
      this.sloReport = sloConfig.SLOReport;
      const { keyMetrics, totalErrorBudget } = this.extractSLOMetrics(this.sloReport, this.sloConfig);
      const { headers, convertedJson2 } = getTableData2(keyMetrics);
      this.tableData = {
        headers: headers,
        data: convertedJson2,
      };
      this.totalErrorBudget = totalErrorBudget;
      this.renderContent = true;
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderConfigReport()];
  };

  renderConfigReport = () => {
    return (
      <>
        <Typography sx={{ marginBottom: '10px', fontSize: '13px', fontWeight: 400, color: colors.text.secondary }}>
          The total error budget is calculated as follows:{this.totalErrorBudget}
        </Typography>
        <CustomTable
          headers={this.tableData.headers}
          tableData={this.tableData.data}
          rowsPerPage={this.tableData.data.length}
          totalRows={this.tableData.data.length}
        />
      </>
    );
  };
}

export default SLOConfigReport;
