import React from 'react';
import PropTypes from 'prop-types';
import Text from '@shared/format/Text';
import { ds } from '@utils/colors';
import { useRollbackCheck } from '@components/k8s/investigate/useRollbackCheck';

/**
 * The line under a rollback row that names one change: when it landed and what rolling it back
 * writes ("running now → after rollback"). That reads the change's recorded values, so it is only
 * true while nothing has changed the workload since — which this asks the server, as the dialog
 * does. Until the answer is "changed again", the row says what it always said.
 */
const RollbackActionDetail = ({ accountId, eventId, changeEventId, line, blockedLine }) => {
  const check = useRollbackCheck({ accountId, eventId, changeEventId });
  const value = check.status === 'blocked' ? blockedLine : line;
  return value ? <Text value={value} sx={{ fontSize: ds.text.caption, color: ds.gray[600] }} /> : null;
};

RollbackActionDetail.propTypes = {
  accountId: PropTypes.string,
  eventId: PropTypes.string,
  changeEventId: PropTypes.string,
  line: PropTypes.string,
  blockedLine: PropTypes.string,
};

export default RollbackActionDetail;
