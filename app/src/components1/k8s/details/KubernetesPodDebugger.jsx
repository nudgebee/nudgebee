import { Grid } from '@mui/material';
import NDialog from '@components1/common/modal/NDialog';
import Terminal from './XtermTerminal';
import { v4 as uuidv4 } from 'uuid';

const KubernetesPodDebugger = ({ accountId, selectedPodName = {}, debugPodOpen, closeDebugPod }) => {
  const additionalComponent = () => {
    return (
      <Grid container spacing={4}>
        <Terminal accountId={accountId} data={selectedPodName} requestId={uuidv4()} />
      </Grid>
    );
  };

  return (
    <NDialog
      dialogTitle={`${selectedPodName.name}`}
      open={debugPodOpen}
      additionalComponent={additionalComponent()}
      isSubmitRequired={false}
      handleClose={closeDebugPod}
      isCancelRequired={true}
    />
  );
};

export default KubernetesPodDebugger;
