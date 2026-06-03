import React from 'react';
import NDialog from '@common-new/modal/NDialog';
import MarkDowns from '@components1/common/MarkDowns';
import CopyButton from '@common-new/CopyButton';
import { Box, Typography } from '@mui/material';

interface KubernetesPodConnectionProps {
  handleClose: () => void;
  open: boolean;
  podData: any;
}

const KubernetesPodConnection: React.FC<KubernetesPodConnectionProps> = ({ handleClose, open, podData }) => {
  const renderMarkdown = () => {
    const jsxElements: any = [];
    let hasContainerExecHeader = false;
    let hasPortForwardInfoHeader = false;

    if (podData?.meta?.config?.containers && podData.meta.config.containers.length > 0) {
      podData.meta.config.containers.forEach((f: any, cIndx: number) => {
        const execCmd = 'kubectl exec -it pods/' + f.name + ' -n ' + podData?.meta?.namespace + ' sh';

        if (f.ports && f.ports.length > 0) {
          if (!hasPortForwardInfoHeader) {
            jsxElements.push(
              <Typography key='portForwardInfoHeader' gutterBottom style={{ fontWeight: 'var(--ds-font-weight-semibold)' }}>
                Port Forward Info:
              </Typography>
            );
            hasPortForwardInfoHeader = true;
          }

          f.ports.forEach((p: any, index: number) => {
            const portFowardCmd = 'kubectl port-forward pods/' + f.name + ' -n ' + podData?.meta?.namespace + ' ' + p + ':' + p;
            jsxElements.push(
              <Box key={`portForwardCmd${index}`} sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
                <Box sx={{ flex: 1, minWidth: 0 }}>
                  <MarkDowns
                    sx={{ maxHeight: '', width: '100%', overflowY: '' }}
                    data={'```'.concat(portFowardCmd).concat('```')}
                    allowExecutable={false}
                    onLinkClick={null}
                  />
                </Box>
                <CopyButton text={portFowardCmd} size='sm' />
              </Box>
            );
          });
        }

        if (!hasContainerExecHeader) {
          jsxElements.push(
            <Typography key='containerExecHeader' gutterBottom style={{ fontWeight: 'var(--ds-font-weight-semibold)' }}>
              Container Exec:
            </Typography>
          );
          hasContainerExecHeader = true;
        }

        jsxElements.push(
          <Box key={`execCmd${cIndx}`} sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
            <Box sx={{ flex: 1, minWidth: 0 }}>
              <MarkDowns
                sx={{ maxHeight: '', width: '100%', overflowY: '' }}
                data={'```'.concat(execCmd).concat('```')}
                allowExecutable={false}
                onLinkClick={null}
              />
            </Box>
            <CopyButton text={execCmd} size='sm' />
          </Box>
        );
      });
    }

    // Render jsxElements
    return <React.Fragment>{jsxElements}</React.Fragment>;
  };

  return (
    <NDialog
      open={open}
      handleClose={handleClose}
      dialogTitle={'Container Connectivity'}
      dialogContent={renderMarkdown()}
      additionalComponent={null}
      isSubmitRequired={false}
      isCancelRequired={false}
    />
  );
};

export default KubernetesPodConnection;
