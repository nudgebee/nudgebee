import { Box, Chip, Link, Typography } from '@mui/material';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import { GithubIcon } from '@assets';
import { safeJSONParse } from 'src/utils/common';
import Datetime from '@components1/common/format/Datetime';

class GithubPRHistoryCard {
  constructor(evidenceData, _event, index) {
    this.id = `GithubPRHistoryCard_${index}`;
    this.icon = GithubIcon;
    this.text = evidenceData?.additional_info?.title || 'GitHub Recent Changes';
    this.resolveButton = false;
    this.renderContent = false;
    this.insightData = [];
    this.pullRequests = [];
    this.workflowRuns = [];
    this.repoUrl = '';
    this.enricherData = evidenceData;
  }

  canRenderContent = async () => {
    if (!this.enricherData) {
      return false;
    }
    const parsedData = safeJSONParse(this.enricherData.data);
    if (!parsedData) {
      return false;
    }
    this.pullRequests = parsedData.pull_requests || [];
    this.workflowRuns = parsedData.workflow_runs || [];
    this.repoUrl = parsedData.repo_url || '';

    if (this.pullRequests.length > 0 || this.workflowRuns.length > 0) {
      this.renderContent = true;
    }
    if (this.enricherData?.insight?.length > 0) {
      this.insightData = this.enricherData.insight;
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    const components = [];
    if (this.pullRequests.length > 0) {
      components.push(() => this.renderPRTable());
    }
    if (this.workflowRuns.length > 0) {
      components.push(() => this.renderWorkflowRuns());
    }
    return components;
  };

  renderPRTable = () => {
    const headers = ['PR', 'Title', 'Author', 'Merged', 'Labels'];
    const repoName = this.repoUrl ? this.repoUrl.replace('https://github.com/', '') : '';
    const tableData = this.pullRequests.map((pr) => [
      {
        component: (
          <Link href={pr.url} target='_blank' rel='noopener noreferrer' sx={{ fontSize: '13px' }}>
            #{pr.number}
          </Link>
        ),
      },
      {
        component: (
          <Typography sx={{ fontSize: '13px', color: '#374151', maxWidth: '300px' }} noWrap title={pr.title}>
            {pr.title}
          </Typography>
        ),
      },
      {
        component: <Typography sx={{ fontSize: '13px', color: '#374151' }}>{pr.author}</Typography>,
      },
      {
        component: pr.merged_at ? (
          <Datetime value={pr.merged_at} sx={{ fontSize: '13px' }} />
        ) : (
          <Typography sx={{ fontSize: '13px', color: '#9CA3AF' }}>—</Typography>
        ),
      },
      {
        component: (
          <Box sx={{ display: 'flex', gap: '4px', flexWrap: 'wrap' }}>
            {(pr.labels || []).map((label) => (
              <Chip key={label} label={label} size='small' sx={{ fontSize: '11px', height: '20px' }} />
            ))}
          </Box>
        ),
      },
    ]);

    return (
      <Box sx={{ marginTop: '8px' }}>
        <Typography sx={{ fontSize: '13px', color: '#6B7280', marginBottom: '8px' }}>
          Recently merged pull requests in {repoName || 'the repository'} around the time of this event.
        </Typography>
        <Typography sx={{ fontSize: '14px', fontWeight: 500, color: '#374151', marginBottom: '8px' }}>Recent Pull Requests</Typography>
        <CustomTable2 tableData={tableData} headers={headers} totalRows={tableData.length} rowsPerPage={tableData.length} />
      </Box>
    );
  };

  renderWorkflowRuns = () => {
    const headers = ['Workflow', 'Status', 'Commit', 'Triggered'];
    const tableData = this.workflowRuns.map((run) => {
      let statusColor = '#9CA3AF';
      let statusLabel = run.status;
      if (run.conclusion === 'success') {
        statusColor = '#10B981';
        statusLabel = 'passed';
      } else if (run.conclusion === 'failure') {
        statusColor = '#EF4444';
        statusLabel = 'failed';
      } else if (run.status === 'in_progress') {
        statusColor = '#F59E0B';
        statusLabel = 'in progress';
      }

      return [
        {
          component: (
            <Link href={run.url} target='_blank' rel='noopener noreferrer' sx={{ fontSize: '13px' }}>
              {run.name}
            </Link>
          ),
        },
        {
          component: (
            <Chip
              label={statusLabel}
              size='small'
              sx={{
                fontSize: '11px',
                height: '20px',
                backgroundColor: `${statusColor}20`,
                color: statusColor,
                fontWeight: 500,
              }}
            />
          ),
        },
        {
          component: <Typography sx={{ fontSize: '13px', color: '#374151', fontFamily: 'monospace' }}>{run.commit_sha?.substring(0, 7)}</Typography>,
        },
        {
          component: run.created_at ? (
            <Datetime value={run.created_at} sx={{ fontSize: '13px' }} />
          ) : (
            <Typography sx={{ fontSize: '13px', color: '#9CA3AF' }}>—</Typography>
          ),
        },
      ];
    });

    return (
      <Box sx={{ marginTop: '16px' }}>
        <Typography sx={{ fontSize: '14px', fontWeight: 500, color: '#374151', marginBottom: '8px' }}>CI Workflow Runs</Typography>
        <CustomTable2 tableData={tableData} headers={headers} totalRows={tableData.length} rowsPerPage={tableData.length} />
      </Box>
    );
  };
}

export default GithubPRHistoryCard;
