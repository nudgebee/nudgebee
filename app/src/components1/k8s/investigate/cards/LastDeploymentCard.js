import Datetime from '@common-new/format/Datetime';
import { Modal } from '@common-new/modal';
import { Box, Typography } from '@mui/material';
import { Divider } from '@components1/ds/Divider';
import InvestigateResolution from '@components1/k8s/investigate/InvestigateResolution';
import LastDeploymentIcon from '@assets/investigation/last-deployment.svg';
import { toast as snackbar } from '@components1/ds/Toast';
import { safeJSONParse } from 'src/utils/common';
import CodeMirrorDiffViewer from '@components1/common/DiffViewer';
import { ds } from '@utils/colors';

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
          <Box display={'flex'} flexDirection={'row'} justifyContent={'space-between'} alignItems={'center'} marginTop={ds.space.mul(0, 10)}>
            <Typography
              display={'inline'}
              sx={{
                color: 'var(--ds-brand-500)',
                fontSize: 'var(--ds-text-body-lg) !important',
                fontWeight: 'var(--ds-font-weight-medium)',
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
                gap={ds.space.mul(0, 3)}
                sx={{
                  border: '1px solid var(--grey-40, var(--ds-brand-200))',
                  padding: 'var(--ds-space-1) var(--ds-space-1)',
                  borderRadius: 'var(--ds-radius-sm)',
                }}
              >
                <Typography
                  sx={{
                    color: 'var(--ds-brand-500)',
                    fontSize: 'var(--ds-text-body-lg) !important',
                    fontWeight: 'var(--ds-font-weight-medium)',
                  }}
                  display={'inline'}
                >
                  {'Deployed '}
                </Typography>
                <Datetime
                  value={this.diff.start_at}
                  sx={{
                    fontSize: 'var(--ds-text-body-lg) !important',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    lineHeight: '16px',
                  }}
                />
              </Box>
            )}
          </Box>
          <Box
            sx={{
              borderRadius: 'var(--ds-radius-lg)',
              border: '1px solid var(--grey-40, var(--ds-brand-200))',
              backgroundColor: 'var(--ds-background-200)',
              padding: 'var(--ds-space-4) var(--ds-space-5)',
              marginTop: 'var(--ds-space-3)',
              marginBottom: 'var(--ds-space-2)',
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
                <Box display={'flex'} flexDirection={'row'} justifyContent={'space-between'} alignItems={'center'} marginTop={ds.space.mul(0, 10)}>
                  <Typography
                    display={'inline'}
                    sx={{
                      color: 'var(--ds-brand-500)',
                      fontSize: 'var(--ds-text-body-lg) !important',
                      fontWeight: 'var(--ds-font-weight-medium)',
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
                      gap={ds.space.mul(0, 3)}
                      sx={{
                        border: '1px solid var(--grey-40, var(--ds-brand-200))',
                        padding: 'var(--ds-space-1) var(--ds-space-1)',
                        borderRadius: 'var(--ds-radius-sm)',
                      }}
                    >
                      <Typography
                        sx={{
                          color: 'var(--ds-brand-500)',
                          fontSize: 'var(--ds-text-body-lg) !important',
                          fontWeight: 'var(--ds-font-weight-medium)',
                        }}
                        display={'inline'}
                      >
                        {'Deployed '}
                      </Typography>
                      <Datetime
                        value={d.diff[0].start_at}
                        sx={{
                          fontSize: 'var(--ds-text-body-lg) !important',
                          fontWeight: 'var(--ds-font-weight-medium)',
                          lineHeight: '16px',
                        }}
                      />
                    </Box>
                  )}
                </Box>

                <Box
                  sx={{
                    borderRadius: 'var(--ds-radius-lg)',
                    border: '1px solid var(--grey-40, var(--ds-brand-200))',
                    backgroundColor: 'var(--ds-background-200)',
                    padding: 'var(--ds-space-4) var(--ds-space-5)',
                    marginTop: 'var(--ds-space-3)',
                    marginBottom: 'var(--ds-space-2)',
                  }}
                >
                  <CodeMirrorDiffViewer originalCode={d.diff[0]?.data?.old} newCode={d.diff[0]?.data?.new} />
                </Box>

                {!isLast && <Divider sx={{ margin: 'var(--ds-space-4) 0' }} />}
              </Box>
            );
          })}
        </Box>
      );
    }
    return (
      <Typography marginTop={ds.space.mul(0, 5)} fontSize={ds.text.bodyLg} fontWeight={500}>
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
