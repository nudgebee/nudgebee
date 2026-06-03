import { Box, Typography } from '@mui/material';
import { Chip } from '@components1/ds/Chip';
import { Link } from '@components1/ds/Link';
import CustomTable2 from '@common-new/tables/CustomTable2';
import { GithubIcon } from '@assets';
import { safeJSONParse } from 'src/utils/common';
import Datetime from '@common-new/format/Datetime';
import { ds } from '@utils/colors';

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
          <Link href={pr.url} target='_blank' style={{ fontSize: 'var(--ds-text-body)' }}>
            #{pr.number}
          </Link>
        ),
      },
      {
        component: (
          <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-brand-500)', maxWidth: ds.space.mul(0, 150) }} noWrap title={pr.title}>
            {pr.title}
          </Typography>
        ),
      },
      {
        component: <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-brand-500)' }}>{pr.author}</Typography>,
      },
      {
        component: pr.merged_at ? (
          <Datetime value={pr.merged_at} sx={{ fontSize: 'var(--ds-text-body)' }} />
        ) : (
          <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-400)' }}>—</Typography>
        ),
      },
      {
        component: (
          <Box sx={{ display: 'flex', gap: 'var(--ds-space-1)', flexWrap: 'wrap' }}>
            {(pr.labels || []).map((label) => (
              <Chip key={label} size='xs'>
                {label}
              </Chip>
            ))}
          </Box>
        ),
      },
    ]);

    return (
      <Box sx={{ marginTop: 'var(--ds-space-2)' }}>
        <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-600)', marginBottom: 'var(--ds-space-2)' }}>
          Recently merged pull requests in {repoName || 'the repository'} around the time of this event.
        </Typography>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: 'var(--ds-brand-500)',
            marginBottom: 'var(--ds-space-2)',
          }}
        >
          Recent Pull Requests
        </Typography>
        <CustomTable2 tableData={tableData} headers={headers} totalRows={tableData.length} rowsPerPage={tableData.length} />
      </Box>
    );
  };

  renderWorkflowRuns = () => {
    const headers = ['Workflow', 'Status', 'Commit', 'Triggered'];
    const tableData = this.workflowRuns.map((run) => {
      let statusTone = 'neutral';
      let statusLabel = run.status;
      if (run.conclusion === 'success') {
        statusTone = 'success';
        statusLabel = 'passed';
      } else if (run.conclusion === 'failure') {
        statusTone = 'critical';
        statusLabel = 'failed';
      } else if (run.status === 'in_progress') {
        statusTone = 'warning';
        statusLabel = 'in progress';
      }

      return [
        {
          component: (
            <Link href={run.url} target='_blank' style={{ fontSize: 'var(--ds-text-body)' }}>
              {run.name}
            </Link>
          ),
        },
        {
          component: (
            <Chip size='xs' tone={statusTone}>
              {statusLabel}
            </Chip>
          ),
        },
        {
          component: (
            <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-brand-500)', fontFamily: 'monospace' }}>
              {run.commit_sha?.substring(0, 7)}
            </Typography>
          ),
        },
        {
          component: run.created_at ? (
            <Datetime value={run.created_at} sx={{ fontSize: 'var(--ds-text-body)' }} />
          ) : (
            <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-400)' }}>—</Typography>
          ),
        },
      ];
    });

    return (
      <Box sx={{ marginTop: 'var(--ds-space-4)' }}>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: 'var(--ds-brand-500)',
            marginBottom: 'var(--ds-space-2)',
          }}
        >
          CI Workflow Runs
        </Typography>
        <CustomTable2 tableData={tableData} headers={headers} totalRows={tableData.length} rowsPerPage={tableData.length} />
      </Box>
    );
  };
}

export default GithubPRHistoryCard;
