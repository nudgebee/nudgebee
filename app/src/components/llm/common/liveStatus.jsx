import React from 'react';
import PropTypes from 'prop-types';
import ConversationLoader from '@shared/ConversationLoader';

// The backend writes one entry per agent while a model call waits on a retry, a
// fallback or a summary (llm-server agents/core/llm_live_status.go). The newest
// entry is the one worth showing.
export const latestLiveStatus = (liveStatus) => {
  if (!liveStatus || typeof liveStatus !== 'object') {
    return null;
  }
  const entries = Object.values(liveStatus).filter((s) => s && typeof s.text === 'string' && s.text);
  if (entries.length === 0) {
    return null;
  }
  return entries.reduce((a, b) => ((Date.parse(b.updated_at) || 0) > (Date.parse(a.updated_at) || 0) ? b : a));
};

export const liveStatusText = (status, now = Date.now()) => {
  if (!status) {
    return '';
  }
  let text = status.text;
  if (status.retry_at) {
    const seconds = Math.ceil((Date.parse(status.retry_at) - now) / 1000);
    text += seconds > 0 ? ` Retrying in ${seconds}s` : ' Retrying now';
    if (status.attempt && status.max_attempts) {
      text += ` (attempt ${status.attempt} of ${status.max_attempts})`;
    }
    text += '.';
  }
  return text;
};

// The status line for the thinking indicator, counting down to the next retry.
export const useLiveStatusText = (liveStatus) => {
  const status = latestLiveStatus(liveStatus);
  const [now, setNow] = React.useState(Date.now());

  const retryAt = status?.retry_at;

  React.useEffect(() => {
    if (!retryAt) {
      return undefined;
    }
    setNow(Date.now());
    const retryTime = Date.parse(retryAt);
    if (Number.isNaN(retryTime) || retryTime <= Date.now()) {
      return undefined;
    }
    // Stop ticking once the countdown reaches zero; "Retrying now" doesn't change.
    const id = setInterval(() => {
      const current = Date.now();
      setNow(current);
      if (retryTime <= current) {
        clearInterval(id);
      }
    }, 1000);
    return () => clearInterval(id);
  }, [retryAt]);

  return liveStatusText(status, now);
};

// The thinking indicator with the live status as its label. The countdown ticks
// here, so only the loader re-renders every second, not the whole chat.
export const LiveStatusLoader = ({ liveStatus, ...loaderProps }) => {
  const label = useLiveStatusText(liveStatus);
  return <ConversationLoader {...loaderProps} label={label} />;
};

LiveStatusLoader.propTypes = {
  liveStatus: PropTypes.object,
};
