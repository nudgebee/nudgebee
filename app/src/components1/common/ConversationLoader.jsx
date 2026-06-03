import { Box, Typography } from '@mui/material';
import { colors } from 'src/utils/colors';
import { useState, useEffect, useMemo, useRef, useCallback } from 'react';

const NUDGEBEE_WORDS = [
  'Nubing',
  'Buzzifying',
  'Hive-syncing',
  'Pollinating',
  'Swarming',
  'Honeying',
  'Bee-bugging',
  'Nudgifying',
  'Hive-tuning',
  'Pod-dancing',
  'Nectar-flowing',
  'Stinging',
  'Bee-scaling',
  'Hiveminding',
  'Buzzworking',
  'Queening',
  'Nudge-crafting',
  'Pollen-parsing',
  'Bee-lieving',
  'Honeycombing',
  'Buzztracing',
  'Nectaring',
  'Bee-lancing',
  'Bee-holding',
  'Waggle-dancing',
  'Hive-diving',
  'Nectar-mining',
  'Buzz-weaving',
  'Colony-syncing',
  'Drone-deploying',
  'Comb-building',
  'Royal-jellying',
  'Forager-routing',
  'Propolis-patching',
  'Swarm-orchestrating',
  'Bee-lining',
  'Hive-warming',
  'Pollen-loading',
  'Worker-dispatching',
  'Nectar-caching',
  'Buzz-amplifying',
  'Colony-balancing',
  'Brood-nurturing',
  'Scout-signaling',
  'Hive-clustering',
  'Flower-mapping',
  'Bee-streaming',
  'Hex-optimizing',
  'Swarm-aligning',
  'Buzzing',
  'Humming',
  'Foraging',
  'Clustering',
  'Nurturing',
  'Harvesting',
  'Communicating',
  'Navigating',
  'Optimizing',
  'Orchestrating',
  'Analyzing',
];

//get random delay between 4 to 10 seconds
const getRandomDelay = () => {
  return Math.floor(Math.random() * (10000 - 4000 + 1)) + 4000;
};

const ConversationLoader = () => {
  const [currentWordIndex, setCurrentWordIndex] = useState(0);
  const [dotCount, setDotCount] = useState(1);
  const timeoutRef = useRef(null);

  const shuffledWords = useMemo(() => {
    return [...NUDGEBEE_WORDS].sort(() => Math.random() - 0.5);
  }, []);

  const advanceWord = useCallback(() => {
    setCurrentWordIndex((prev) => (prev + 1) % shuffledWords.length);
  }, [shuffledWords.length]);

  const scheduleNextWord = useCallback(() => {
    const delay = getRandomDelay();
    timeoutRef.current = setTimeout(() => {
      advanceWord();
      scheduleNextWord();
    }, delay);
  }, [advanceWord]);

  useEffect(() => {
    scheduleNextWord();
    return () => clearTimeout(timeoutRef.current);
  }, [scheduleNextWord]);

  useEffect(() => {
    const dotInterval = setInterval(() => {
      setDotCount((prev) => (prev % 3) + 1);
    }, 400);

    return () => clearInterval(dotInterval);
  }, []);

  const dots = ' .'.repeat(dotCount);

  return (
    <Box
      sx={{
        borderRadius: 'var(--ds-radius-sm)',
        width: '100%',
        display: 'grid',
        gridTemplateColumns: '35px 1fr',
        alignItems: 'center',
        gap: 'var(--ds-space-3)',
      }}
    >
      <Box
        sx={{
          height: '36px',
          width: '36px',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          borderRadius: 'var(--ds-radius-sm)',
          position: 'relative',
          '&::before': {
            content: '""',
            position: 'absolute',
            width: '24px',
            height: '24px',
            borderRadius: '50%',
            border: `2px solid ${colors.text.yellowLabel}`,
            animation: 'ripple 2s ease-in-out infinite',
            top: '50%',
            left: '50%',
          },
          '&::after': {
            content: '""',
            position: 'absolute',
            width: '16px',
            height: '16px',
            borderRadius: '50%',
            backgroundColor: colors.nudgebeeMain,
            animation: 'pulse 2s ease-in-out infinite',
            top: '50%',
            left: '50%',
          },
          '@keyframes ripple': {
            '0%': {
              transform: 'translate(-50%, -50%) scale(0.8)',
              opacity: 0.8,
            },
            '50%': {
              transform: 'translate(-50%, -50%) scale(1)',
              opacity: 0.3,
            },
            '100%': {
              transform: 'translate(-50%, -50%) scale(0.8)',
              opacity: 0.8,
            },
          },
          '@keyframes pulse': {
            '0%': {
              transform: 'translate(-50%, -50%) scale(1)',
              opacity: 0.8,
            },
            '50%': {
              transform: 'translate(-50%, -50%) scale(0.8)',
              opacity: 1,
            },
            '100%': {
              transform: 'translate(-50%, -50%) scale(1)',
              opacity: 0.8,
            },
          },
        }}
      />
      <Box
        sx={{
          height: '40px',
          display: 'flex',
          alignItems: 'center',
          padding: '0 var(--ds-space-4)',
          overflow: 'hidden',
        }}
      >
        <Typography
          component='span'
          sx={{
            fontFamily: '"Roboto", sans-serif',
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-medium)',
            background: 'linear-gradient(90deg, var(--ds-brand-500), #5a7db3, #314e7d)',
            backgroundSize: '200% 100%',
            backgroundClip: 'text',
            WebkitBackgroundClip: 'text',
            WebkitTextFillColor: 'transparent',
            animation: 'shimmerText 2s linear infinite, fadeWord 2s ease-in-out infinite',
            '@keyframes shimmerText': {
              '0%': {
                backgroundPosition: '100% 0',
              },
              '100%': {
                backgroundPosition: '-100% 0',
              },
            },
            '@keyframes fadeWord': {
              '0%': {
                opacity: 0.6,
              },
              '50%': {
                opacity: 1,
              },
              '100%': {
                opacity: 0.6,
              },
            },
          }}
        >
          {shuffledWords[currentWordIndex]}
        </Typography>
        <Typography
          component='span'
          sx={{
            fontFamily: '"Roboto", sans-serif',
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: 'var(--ds-brand-500)',
            minWidth: '30px',
            marginLeft: 'var(--ds-space-1)',
          }}
        >
          {dots}
        </Typography>
      </Box>
    </Box>
  );
};

export default ConversationLoader;
