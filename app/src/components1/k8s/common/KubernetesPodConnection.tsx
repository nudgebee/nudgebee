import React from 'react';
import Dialog from '@mui/material/Dialog';
import DialogTitle from '@mui/material/DialogTitle';
import DialogContent from '@mui/material/DialogContent';
import IconButton from '@mui/material/IconButton';
import CloseIcon from '@mui/icons-material/Close';
import MarkDowns from '@components1/common/MarkDowns';
import CopyableText from '@common/CopyableText';
import { Typography } from '@mui/material';

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
              <Typography key='portForwardInfoHeader' gutterBottom style={{ fontWeight: 600 }}>
                Port Forward Info:
              </Typography>
            );
            hasPortForwardInfoHeader = true;
          }

          f.ports.forEach((p: any, index: number) => {
            const portFowardCmd = 'kubectl port-forward pods/' + f.name + ' -n ' + podData?.meta?.namespace + ' ' + p + ':' + p;
            jsxElements.push(
              <CopyableText copyableText={portFowardCmd} key={`portForwardCmd${index}`} iconColor={undefined} onCopy={undefined}>
                <MarkDowns
                  sx={{ maxHeight: '', width: '100%', overflowY: '' }}
                  data={'```'.concat(portFowardCmd).concat('```')}
                  allowExecutable={false}
                  onLinkClick={null}
                />
              </CopyableText>
            );
          });
        }

        if (!hasContainerExecHeader) {
          jsxElements.push(
            <Typography key='containerExecHeader' gutterBottom style={{ fontWeight: 600 }}>
              Container Exec:
            </Typography>
          );
          hasContainerExecHeader = true;
        }

        jsxElements.push(
          <CopyableText copyableText={execCmd} key={`execCmd${cIndx}`} iconColor={undefined} onCopy={undefined}>
            <MarkDowns
              sx={{ maxHeight: '', width: '100%', overflowY: '' }}
              data={'```'.concat(execCmd).concat('```')}
              allowExecutable={false}
              onLinkClick={null}
            />
          </CopyableText>
        );
      });
    }

    // Render jsxElements
    return <React.Fragment>{jsxElements}</React.Fragment>;
  };

  return (
    <Dialog onClose={handleClose} aria-labelledby='customized-dialog-title' open={open}>
      <DialogTitle sx={{ m: 0, p: 2 }} id='customized-dialog-title'>
        Container Connectivity
      </DialogTitle>
      <IconButton
        aria-label='close'
        onClick={handleClose}
        sx={{
          position: 'absolute',
          right: 8,
          top: 8,
          color: (theme) => theme.palette.grey[500],
        }}
      >
        <CloseIcon />
      </IconButton>
      <DialogContent dividers>{renderMarkdown()}</DialogContent>
    </Dialog>
  );
};

export default KubernetesPodConnection;
