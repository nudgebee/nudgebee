import React from 'react';
import { Box, Typography, CircularProgress } from '@mui/material';
import MarkDowns from '@shared/viewers/MarkDowns';
import apiKubernetes from '@api1/kubernetes';
import RCAIcon from '@assets/investigation/rca-icon.svg';
import { ds } from '@utils/colors';
import { Button } from '@ui/Button';
import { Chip } from '@ui/Chip';
import { toast as snackbar } from '@ui/Toast';

const RCAInProgress = () => {
  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        height: '100%',
        minHeight: ds.space.mul(0, 120),
        p: 'var(--ds-space-6)',
      }}
    >
      {/* Main content card */}
      <Box
        sx={{
          display: 'flex',
          alignItems: 'flex-start',
          gap: 'var(--ds-space-4)',
          p: 'var(--ds-space-4)',
          borderRadius: 'var(--ds-radius-lg)',
          backgroundColor: ds.blue[100],
          border: `1px solid color-mix(in srgb, ${ds.blue[600]} 8%, transparent)`,
        }}
      >
        {/* Spinner icon */}
        <Box
          sx={{
            width: ds.space.mul(0, 18),
            height: ds.space.mul(0, 18),
            flexShrink: 0,
            borderRadius: 'var(--ds-radius-lg)',
            backgroundColor: ds.background[100],
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
          }}
        >
          <CircularProgress size={18} thickness={4} sx={{ color: ds.blue[500] }} />
        </Box>

        <Box sx={{ flex: 1 }}>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: ds.gray[700],
              lineHeight: 1.3,
              mb: 'var(--ds-space-1)',
            }}
          >
            Root cause analysis in progress
          </Typography>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-body)',
              color: ds.gray[600],
              lineHeight: 1.6,
            }}
          >
            We're correlating signals around this event to identify the root cause. This typically takes a minute or two — results will update
            automatically.
          </Typography>
        </Box>
      </Box>
    </Box>
  );
};

const RCAFailed = () => {
  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        height: '100%',
        minHeight: ds.space.mul(0, 100),
        p: 'var(--ds-space-6)',
      }}
    >
      <Box
        sx={{
          display: 'flex',
          alignItems: 'flex-start',
          gap: 'var(--ds-space-4)',
          p: 'var(--ds-space-4)',
          borderRadius: 'var(--ds-radius-lg)',
          backgroundColor: ds.red[100],
          border: `1px solid color-mix(in srgb, ${ds.red[600]} 8%, transparent)`,
        }}
      >
        <Box
          sx={{
            width: ds.space.mul(0, 18),
            height: ds.space.mul(0, 18),
            flexShrink: 0,
            borderRadius: 'var(--ds-radius-lg)',
            backgroundColor: `color-mix(in srgb, ${ds.red[600]} 7%, transparent)`,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
          }}
        >
          <Box component='svg' viewBox='0 0 20 20' fill={ds.red[600]} sx={{ width: ds.space.mul(0, 9), height: ds.space.mul(0, 9) }}>
            <path
              fillRule='evenodd'
              d='M8.485 2.495c.673-1.167 2.357-1.167 3.03 0l6.28 10.875c.673 1.167-.17 2.625-1.516 2.625H3.72c-1.347 0-2.189-1.458-1.515-2.625L8.485 2.495zM10 5a.75.75 0 01.75.75v3.5a.75.75 0 01-1.5 0v-3.5A.75.75 0 0110 5zm0 9a1 1 0 100-2 1 1 0 000 2z'
              clipRule='evenodd'
            />
          </Box>
        </Box>

        <Box sx={{ flex: 1 }}>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: ds.gray[700],
              mb: 'var(--ds-space-1)',
              lineHeight: 1.3,
            }}
          >
            Analysis couldn't complete
          </Typography>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-body)',
              color: ds.gray[600],
              lineHeight: 1.6,
            }}
          >
            The root cause analysis ran into an issue and couldn't finish. This can happen if the event data is incomplete or a timeout occurred. Try
            re-triggering the analysis — if it keeps failing, the team can look into it.
          </Typography>
        </Box>
      </Box>
    </Box>
  );
};

const RCANoData = () => {
  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        height: '100%',
        minHeight: ds.space.mul(0, 100),
        p: 'var(--ds-space-6)',
      }}
    >
      <Box
        sx={{
          display: 'flex',
          alignItems: 'flex-start',
          gap: 'var(--ds-space-4)',
          p: 'var(--ds-space-4)',
          borderRadius: 'var(--ds-radius-lg)',
          backgroundColor: ds.background[200],
          border: `1px solid ${ds.gray[300]}`,
        }}
      >
        <Box
          sx={{
            width: ds.space.mul(0, 18),
            height: ds.space.mul(0, 18),
            flexShrink: 0,
            borderRadius: 'var(--ds-radius-lg)',
            backgroundColor: ds.gray[100],
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
          }}
        >
          <Box component='svg' viewBox='0 0 20 20' fill={ds.gray[600]} sx={{ width: ds.space.mul(0, 9), height: ds.space.mul(0, 9) }}>
            <path d='M10 12.5a.75.75 0 01-.75-.75v-4.5a.75.75 0 011.5 0v4.5a.75.75 0 01-.75.75zM10 15a1 1 0 100-2 1 1 0 000 2z' />
            <path fillRule='evenodd' d='M10 1a9 9 0 100 18 9 9 0 000-18zM2.5 10a7.5 7.5 0 1115 0 7.5 7.5 0 01-15 0z' clipRule='evenodd' />
          </Box>
        </Box>

        <Box sx={{ flex: 1 }}>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: ds.gray[700],
              mb: 'var(--ds-space-1)',
              lineHeight: 1.3,
            }}
          >
            No findings to report
          </Typography>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-body)',
              color: ds.gray[600],
              lineHeight: 1.6,
            }}
          >
            The analysis completed but didn't surface any root cause. This could mean the event resolved on its own, or there wasn't enough signal in
            the data to pinpoint a cause.
          </Typography>
        </Box>
      </Box>
    </Box>
  );
};

const FORMAT_SOURCE_LABELS = {
  rule: 'Rule format',
  account: 'Account format',
  default: 'Default format',
};

// Action row above the completed report: which format level applies, plus
// copy-as-markdown, download and (write access only) regenerate.
const RCAReportToolbar = ({ report, formatSource, onRegenerate, eventId }) => {
  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(report);
      snackbar.success('RCA report copied as Markdown');
    } catch (error) {
      console.error('Failed to copy RCA report:', error);
      snackbar.error('Failed to copy the report');
    }
  };

  const handleDownload = () => {
    const blob = new Blob([report], { type: 'text/markdown' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `rca-report-${eventId || 'event'}.md`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  };

  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', mb: 'var(--ds-space-3)' }}>
      {formatSource ? (
        <Chip size='sm' tone='neutral'>
          {FORMAT_SOURCE_LABELS[formatSource] || formatSource}
        </Chip>
      ) : null}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', ml: 'auto' }}>
        <Button tone='secondary' size='xs' onClick={handleCopy} data-testid='rca-copy-btn'>
          Copy
        </Button>
        <Button tone='secondary' size='xs' onClick={handleDownload} data-testid='rca-download-btn'>
          Download
        </Button>
        {onRegenerate && (
          <Button tone='secondary' size='xs' onClick={() => onRegenerate()} data-testid='rca-regenerate-btn'>
            Regenerate
          </Button>
        )}
      </Box>
    </Box>
  );
};

// Shown when an input analysis (summary / investigation / log) was updated
// after this report was generated — the report may no longer match findings.
const RCAOutdatedBanner = ({ onRegenerate }) => (
  <Box
    sx={{
      display: 'flex',
      alignItems: 'center',
      gap: 'var(--ds-space-3)',
      p: 'var(--ds-space-3) var(--ds-space-4)',
      mb: 'var(--ds-space-4)',
      backgroundColor: ds.yellow[100],
      border: `1px solid ${ds.amber[300]}`,
      borderRadius: 'var(--ds-radius-lg)',
    }}
  >
    <Typography sx={{ flex: 1, fontSize: 'var(--ds-text-body)', color: ds.gray[700] }}>
      The investigation has been updated since this report was generated, so it may not reflect the latest findings.
    </Typography>
    {onRegenerate && (
      <Button tone='secondary' size='xs' onClick={() => onRegenerate()} data-testid='rca-outdated-regenerate-btn'>
        Regenerate
      </Button>
    )}
  </Box>
);

// Component to render the RCA report content
// Polling is handled at page level (useRcaPolling in investigate.jsx)
const RCAReport = ({ data = {}, onRegenerate, eventId }) => {
  const status = data?.status?.toUpperCase();

  if (status === 'IN_PROGRESS') {
    return <RCAInProgress />;
  } else if (status === 'COMPLETED') {
    if (!data?.analysis) {
      return <RCANoData />;
    }
  } else if (status === 'FAILED') {
    return <RCAFailed />;
  }

  try {
    let summary = data.analysis;
    if (typeof summary === 'string' && summary.startsWith('```') && summary.endsWith('```')) {
      summary = summary.slice(3, -3).trim();
    }
    return (
      <Box sx={{ width: '100%' }}>
        <RCAReportToolbar report={summary} formatSource={data?.format_source} onRegenerate={onRegenerate} eventId={eventId} />
        {data?.outdated ? <RCAOutdatedBanner onRegenerate={onRegenerate} /> : null}
        <MarkDowns data={summary} sx={{ maxHeight: '100%', width: '100%', overflowY: 'auto' }} />
      </Box>
    );
  } catch (error) {
    console.error('Error parsing RCA data:', error);
    return (
      <Box sx={{ p: ds.space[4], color: 'error.main' }}>
        <Typography>Error parsing analysis data. Please try again later.</Typography>
      </Box>
    );
  }
};

class RCACard {
  constructor() {
    this.id = 'RCACard';
    this.icon = RCAIcon;
    this.text = 'Root Cause Analysis';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.rcaData = null;
    this.isBeta = true;
    this.event = {};
    this.onDataUpdate = null;
    // Set by the page (write access only); triggers a regenerate=true run.
    this.onRegenerate = null;
    this.refreshRenderId = 0;
  }

  setDataUpdateCallback(callback) {
    this.onDataUpdate = callback;
    this.refreshRenderId += 1;
  }

  canRenderContent = async (_evidenceData, event) => {
    this.event = event;
    await apiKubernetes.generateRCA(event.id, event.cloud_account_id, false).then((response) => {
      if (typeof response?.status === 'string' && response.status.trim() !== '') {
        this.renderContent = true;
      } else {
        this.renderContent = false;
        return this.renderContent;
      }
      let rcaData = response;
      if (rcaData?.status.toUpperCase() === 'IN_PROGRESS') {
        this.insightData.push({
          message: 'RCA is underway — check back shortly for results',
          severity: 'Info',
        });
        this.rcaData = { status: rcaData.status };
      } else if (rcaData?.status.toUpperCase() === 'COMPLETED') {
        try {
          this.insightData.push({
            message: 'RCA report is ready',
            severity: 'Info',
          });
          this.rcaData = {
            file_details: {},
            status: rcaData.status,
            summary: rcaData.summary,
            analysis: rcaData.analysis,
            outdated: rcaData.outdated,
            format_source: rcaData.format_source,
          };
        } catch (error) {
          console.error('Error parsing RCA summary for insights:', error);
          this.insightData.push({
            message: 'Error parsing RCA summary for insights',
            severity: 'Error',
          });
        }
      } else if (rcaData?.status.toUpperCase() === 'FAILED') {
        this.insightData.push({
          message: 'RCA hit a snag — try re-triggering or check back later',
          severity: 'Error',
        });
        this.rcaData = { status: rcaData.status };
      }
    });

    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => <RCAReport data={this.rcaData} onRegenerate={this.onRegenerate} eventId={this.event?.id} />];
  };
}

export default RCACard;
