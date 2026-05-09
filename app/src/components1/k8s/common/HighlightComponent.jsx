import React from 'react';
import { Typography } from '@mui/material';
import PrimaryLink from '@components1/common/PrimaryLink';
import { useRouter } from 'next/router';
import PropTypes from 'prop-types';

const DEFAULT_HIGHLIGHT_WORDS = ['OOMKilled', 'Hi-Restarts', 'workload'];

const HighlightText = ({ message, highlightWords = DEFAULT_HIGHLIGHT_WORDS, cluster }) => {
  const router = useRouter();

  const highlight = () => {
    const words = message.split(/\s+/);
    return words.map((word, index) => {
      const lowerCaseWord = word.toLowerCase();
      const match = highlightWords.find((hw) => hw.toLowerCase() === lowerCaseWord);

      let route = null;

      if (lowerCaseWord === 'oomkilled') {
        route = `/kubernetes/details/${cluster}?accountId=${cluster}&podTab=pod_oom_killer_enricher#events/pod-errors`;
      } else if (lowerCaseWord === 'hi-restarts') {
        route = `/kubernetes/details/${cluster}?accountId=${cluster}&podTab=report_crash_loop#events/pod-errors`;
      } else if (lowerCaseWord === 'right' || lowerCaseWord === 'sized') {
        route = `/kubernetes/details/${cluster}?accountId=${cluster}#optimize/right-sizing`;
      }

      return (
        <React.Fragment key={`${lowerCaseWord}-${index}`}>
          {index > 0 && ' '}
          {match && route ? (
            <PrimaryLink sx={{ color: '#3F83F8', cursor: 'pointer', fontSize: '13px', fontWeight: 400 }} onClick={() => router.push(route)}>
              {word}
            </PrimaryLink>
          ) : (
            word
          )}
        </React.Fragment>
      );
    });
  };

  const containsHighlight = message.split(/\s+/).some((word) => highlightWords.map((hw) => hw.toLowerCase()).includes(word.toLowerCase()));

  return (
    <Typography
      sx={{
        fontSize: '13px',
        fontWeight: 400,
        color: '#374151',
      }}
    >
      {containsHighlight ? highlight() : message}
    </Typography>
  );
};

HighlightText.propTypes = {
  message: PropTypes.string.isRequired,
  highlightWords: PropTypes.arrayOf(PropTypes.string),
  cluster: PropTypes.string.isRequired,
};

export default HighlightText;
