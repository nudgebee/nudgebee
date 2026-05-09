import Datetime from '@components1/common/format/Datetime';
import { Modal } from '@components1/common/modal';
import { Box, Divider, Typography } from '@mui/material';
import InvestigateResolution from '@components1/k8s/investigate/InvestigateResolution';
import LastDeploymentIcon from '@assets/investigation/last-deployment.svg';
import { snackbar } from '@components1/common/snackbarService';
import { safeJSONParse } from 'src/utils/common';
import CodeMirrorDiffViewer from '@components1/common/DiffViewer';

class LastDeploymentCard {
  constructor(evidenceData, event, index) {
    this.id = `LastDeploymentCard_${index}`;
    this.icon = LastDeploymentIcon;
    this.text = evidenceData.additional_info?.title || 'Last Deployment Change';
    this.resolveButton = true;
    this.renderContent = false;
    this.highlightsData = [];
    this.diff = [];
    this.event = event;
    this.evidenceData = evidenceData;
    this.deploymentHistory = {};
  }

  canRenderContent = async () => {
    if (this.evidenceData?.additional_info?.action_name == 'deployment_history') {
      const parsedData = safeJSONParse(this.evidenceData.data);
      if (parsedData) {
        const diffData = parsedData?.deployments
          ?.map((d) => {
            const filterDiffs = d?.evidences?.filter((i) => i.type == 'diff') || [];
            return {
              diff: filterDiffs,
              description: d.description,
              deploymentStrategy: d.deployment_strategy,
              timeBeforeEvent: d.time_before_event,
            };
          })
          ?.filter((d) => d.diff.length > 0);

        if (diffData && diffData.length > 0) {
          const deploymentHistory = {
            namespace: parsedData.namespace,
            rolloutName: parsedData.rollout_name,
            service: parsedData.service_name,
            timeRangeHours: parsedData.time_range_hours,
            diffData,
          };
          this.deploymentHistory = deploymentHistory;
          this.renderContent = true;
        }
      }
      this.highlightsData = this.evidenceData.insight;
    } else {
      const diff = this.evidenceData.type === 'diff';
      if (diff) {
        this.renderContent = true;
        this.diff = this.evidenceData;
        this.highlightsData = this.evidenceData?.insight;
      }
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.highlightsData;
  };

  getContentComponents = () => {
    return [() => this.renderDiffData()];
  };

  renderDiffData = () => {
    if (Object.keys(this.diff).length > 0) {
      return (
        <Box>
          <Box display={'flex'} flexDirection={'row'} justifyContent={'space-between'} alignItems={'center'} marginTop={'20px'}>
            <Typography
              display={'inline'}
              sx={{
                color: '#374151',
                fontSize: '14px !important',
                fontWeight: 500,
              }}
            >
              {' Here’s What Changed'}
            </Typography>
            {this.diff.start_at && (
              <Box
                display={'flex'}
                flexDirection={'row'}
                alignItems={'center'}
                justifyContent={'center'}
                gap={'6px'}
                sx={{
                  border: '1px solid var(--grey-40, #D0D0D0)',
                  padding: '4px 6px',
                  borderRadius: '4px',
                }}
              >
                <Typography
                  sx={{
                    color: '#374151',
                    fontSize: '14px !important',
                    fontWeight: 500,
                  }}
                  display={'inline'}
                >
                  {'Deployed '}
                </Typography>
                <Datetime
                  value={this.diff.start_at}
                  sx={{
                    fontSize: '14px !important',
                    fontWeight: 500,
                    lineHeight: '16px',
                  }}
                />
              </Box>
            )}
          </Box>
          <Box
            sx={{
              borderRadius: '8px',
              border: '1px solid var(--grey-40, #D0D0D0)',
              backgroundColor: '#F6FAFF',
              padding: '20px 24px',
              marginTop: '12px',
              marginBottom: '10px',
            }}
          >
            <CodeMirrorDiffViewer originalCode={this.diff?.data?.old} newCode={this.diff?.data?.new} />
          </Box>
        </Box>
      );
    } else if (this.deploymentHistory && Object.keys(this.deploymentHistory).length > 0) {
      return (
        <Box>
          <Typography>{`Namespace: ${this.deploymentHistory.namespace}`}</Typography>
          <Typography>{`Rollout Name: ${this.deploymentHistory.rolloutName}`}</Typography>
          <Typography>{`Service Name: ${this.deploymentHistory.service}`}</Typography>
          <Typography>{`Time Range: ${this.deploymentHistory.timeRangeHours}`}</Typography>
          {this.deploymentHistory.diffData?.map((d, index) => {
            const isLast = index === this.deploymentHistory.diffData.length - 1;

            return (
              <Box key={index}>
                <Box display={'flex'} flexDirection={'row'} justifyContent={'space-between'} alignItems={'center'} marginTop={'20px'}>
                  <Typography
                    display={'inline'}
                    sx={{
                      color: '#374151',
                      fontSize: '14px !important',
                      fontWeight: 500,
                    }}
                  >
                    {' Here’s What Changed'}
                  </Typography>

                  {d?.diff?.[0]?.start_at && (
                    <Box
                      display={'flex'}
                      flexDirection={'row'}
                      alignItems={'center'}
                      justifyContent={'center'}
                      gap={'6px'}
                      sx={{
                        border: '1px solid var(--grey-40, #D0D0D0)',
                        padding: '4px 6px',
                        borderRadius: '4px',
                      }}
                    >
                      <Typography
                        sx={{
                          color: '#374151',
                          fontSize: '14px !important',
                          fontWeight: 500,
                        }}
                        display={'inline'}
                      >
                        {'Deployed '}
                      </Typography>
                      <Datetime
                        value={d.diff[0].start_at}
                        sx={{
                          fontSize: '14px !important',
                          fontWeight: 500,
                          lineHeight: '16px',
                        }}
                      />
                    </Box>
                  )}
                </Box>

                <Box
                  sx={{
                    borderRadius: '8px',
                    border: '1px solid var(--grey-40, #D0D0D0)',
                    backgroundColor: '#F6FAFF',
                    padding: '20px 24px',
                    marginTop: '12px',
                    marginBottom: '10px',
                  }}
                >
                  <CodeMirrorDiffViewer originalCode={d.diff[0]?.data?.old} newCode={d.diff[0]?.data?.new} />
                </Box>

                {!isLast && <Divider sx={{ margin: '20px 0' }} />}
              </Box>
            );
          })}
        </Box>
      );
    }
    return (
      <Typography marginTop={'10px'} fontSize={'14px'} fontWeight={500}>
        No diff availabe.
      </Typography>
    );
  };

  RevertTheDeployment = (props) => {
    const handleSnackbar = (type, message) => {
      if (['success', 'error'].includes(type)) {
        snackbar[type](message);
      }
    };

    return (
      <Modal
        width='md'
        open={props.open}
        handleClose={props.onCloseComponent}
        title={`Revert Development of ${this.event?.subject_name}`}
        loader={false}
      >
        <InvestigateResolution
          accountId={this.event?.cloud_account_id ?? ''}
          row={this.event}
          handleClose={props.onCloseComponent}
          updateInvestigateSuccessSnackBar={handleSnackbar}
          isRevertTheDevelopment={props.open}
          cardId={this.id}
        />
      </Modal>
    );
  };

  getResolveComponent = () => {
    if (this.diff?.data?.old && this.diff?.data?.new) {
      return this.RevertTheDeployment;
    }
    this.resolveButton = false;
    return null;
  };
}

export default LastDeploymentCard;
