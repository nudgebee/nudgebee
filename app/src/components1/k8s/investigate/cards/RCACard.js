import React from 'react';
import { Box, Typography, CircularProgress } from '@mui/material';
import MarkDowns from '@components1/common/MarkDowns';
import apiKubernetes from '@api1/kubernetes';
import RCAIcon from '@assets/investigation/rca-icon.svg';
import { colors } from 'src/utils/colors';

const RCAInProgress = () => {
  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        height: '100%',
        minHeight: '240px',
        p: '32px',
      }}
    >
      {/* Main content card */}
      <Box
        sx={{
          display: 'flex',
          alignItems: 'flex-start',
          gap: '16px',
          p: '20px',
          borderRadius: '10px',
          backgroundColor: colors.background?.primaryLightest || '#EFF6FF',
          border: `1px solid ${colors.primary || '#2563EB'}15`,
        }}
      >
        {/* Spinner icon */}
        <Box
          sx={{
            width: '36px',
            height: '36px',
            flexShrink: 0,
            borderRadius: '8px',
            backgroundColor: colors.background?.white || '#FFFFFF',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
          }}
        >
          <CircularProgress size={18} thickness={4} sx={{ color: colors.primary || '#2563EB' }} />
        </Box>

        <Box sx={{ flex: 1 }}>
          <Typography
            sx={{
              fontSize: '14px',
              fontWeight: 600,
              color: colors.text?.secondary || '#374151',
              lineHeight: 1.3,
              mb: '6px',
            }}
          >
            Root cause analysis in progress
          </Typography>
          <Typography
            sx={{
              fontSize: '13px',
              color: colors.text?.tertiary || '#737373',
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
        minHeight: '200px',
        p: '32px',
      }}
    >
      <Box
        sx={{
          display: 'flex',
          alignItems: 'flex-start',
          gap: '16px',
          p: '20px',
          borderRadius: '10px',
          backgroundColor: colors.background?.medium || '#FFF1F1',
          border: `1px solid ${colors.error || '#DC2626'}14`,
        }}
      >
        <Box
          sx={{
            width: '36px',
            height: '36px',
            flexShrink: 0,
            borderRadius: '8px',
            backgroundColor: `${colors.error || '#DC2626'}12`,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
          }}
        >
          <Box component='svg' viewBox='0 0 20 20' fill={colors.error || '#DC2626'} sx={{ width: '18px', height: '18px' }}>
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
              fontSize: '14px',
              fontWeight: 600,
              color: colors.text?.secondary || '#374151',
              mb: '6px',
              lineHeight: 1.3,
            }}
          >
            Analysis couldn't complete
          </Typography>
          <Typography
            sx={{
              fontSize: '13px',
              color: colors.text?.tertiary || '#737373',
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
        minHeight: '200px',
        p: '32px',
      }}
    >
      <Box
        sx={{
          display: 'flex',
          alignItems: 'flex-start',
          gap: '16px',
          p: '20px',
          borderRadius: '10px',
          backgroundColor: colors.background?.tertiaryLightestest || '#f9f9f9',
          border: `1px solid ${colors.border?.primary || '#E5E7EB'}`,
        }}
      >
        <Box
          sx={{
            width: '36px',
            height: '36px',
            flexShrink: 0,
            borderRadius: '8px',
            backgroundColor: colors.background?.tertiaryLight || '#F3F3F3',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
          }}
        >
          <Box component='svg' viewBox='0 0 20 20' fill={colors.text?.tertiary || '#737373'} sx={{ width: '18px', height: '18px' }}>
            <path d='M10 12.5a.75.75 0 01-.75-.75v-4.5a.75.75 0 011.5 0v4.5a.75.75 0 01-.75.75zM10 15a1 1 0 100-2 1 1 0 000 2z' />
            <path fillRule='evenodd' d='M10 1a9 9 0 100 18 9 9 0 000-18zM2.5 10a7.5 7.5 0 1115 0 7.5 7.5 0 01-15 0z' clipRule='evenodd' />
          </Box>
        </Box>

        <Box sx={{ flex: 1 }}>
          <Typography
            sx={{
              fontSize: '14px',
              fontWeight: 600,
              color: colors.text?.secondary || '#374151',
              mb: '6px',
              lineHeight: 1.3,
            }}
          >
            No findings to report
          </Typography>
          <Typography
            sx={{
              fontSize: '13px',
              color: colors.text?.tertiary || '#737373',
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

// Component to render the RCA report content
// Polling is handled at page level (useRcaPolling in investigate.jsx)
const RCAReport = ({ data = {} }) => {
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
    return <MarkDowns data={summary} sx={{ maxHeight: '100%', width: '100%', overflowY: 'auto' }} />;
  } catch (error) {
    console.error('Error parsing RCA data:', error);
    return (
      <Box sx={{ p: 2, color: 'error.main' }}>
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
    return [() => <RCAReport data={this.rcaData} />];
  };
}

export default RCACard;
