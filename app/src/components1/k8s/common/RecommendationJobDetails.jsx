import Datetime from '@components1/common/format/Datetime';
import { useData } from '@context/DataContext';
import { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Grid, Typography } from '@mui/material';

export default function RecommendationJobDetails({ jobName }) {
  const { selectedCluster } = useData();
  const [recommendationJob, setRecommendationJob] = useState({});

  useEffect(() => {
    if (!jobName) {
      setRecommendationJob({});
      return;
    }
    let job = {};
    for (let j of selectedCluster?.agent?.connection_status?.schedule_jobs ?? []) {
      if (j?.runnable_params?.action_func_name == jobName) {
        job = j;
        break;
      }
    }
    setRecommendationJob(job);
  }, [jobName, selectedCluster]);

  return Object.keys(recommendationJob) == 0 ? (
    <></>
  ) : (
    <Grid container direction={'row'} sx={{ justifyContent: 'end' }} paddingTop={2}>
      <Typography fontSize={14}>Refreshed At - </Typography>
      <Datetime value={new Date(recommendationJob?.state?.last_exec_time_sec * 1000)} />
    </Grid>
  );
}

RecommendationJobDetails.propTypes = {
  jobName: PropTypes.string.isRequired,
};
