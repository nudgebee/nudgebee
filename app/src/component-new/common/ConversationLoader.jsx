import { Box } from '@mui/material';
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

const getRandomDelay = () => Math.floor(Math.random() * (10000 - 4000 + 1)) + 4000;

const ConversationLoader = () => {
  const [currentWordIndex, setCurrentWordIndex] = useState(0);
  const [dotCount, setDotCount] = useState(1);
  const timeoutRef = useRef(null);

  const shuffledWords = useMemo(() => [...NUDGEBEE_WORDS].sort(() => Math.random() - 0.5), []);

  const advanceWord = useCallback(() => {
    setCurrentWordIndex((prev) => (prev + 1) % shuffledWords.length);
  }, [shuffledWords.length]);

  const scheduleNextWord = useCallback(() => {
    timeoutRef.current = setTimeout(() => {
      advanceWord();
      scheduleNextWord();
    }, getRandomDelay());
  }, [advanceWord]);

  useEffect(() => {
    scheduleNextWord();
    return () => clearTimeout(timeoutRef.current);
  }, [scheduleNextWord]);

  useEffect(() => {
    const id = setInterval(() => setDotCount((prev) => (prev % 3) + 1), 400);
    return () => clearInterval(id);
  }, []);

  const dots = ' .'.repeat(dotCount);

  return (
    <Box
      sx={{
        width: '100%',
        display: 'grid',
        gridTemplateColumns: '36px 1fr',
        alignItems: 'center',
        gap: 'var(--ds-space-3)',
        borderRadius: 'var(--ds-radius-sm)',
      }}
    >
      {/* Ripple + pulse dot — brand yellow */}
      <Box
        sx={{
          height: '36px',
          width: '36px',
          position: 'relative',
          flexShrink: 0,
          '@keyframes cl-ripple': {
            '0%, 100%': { transform: 'translate(-50%, -50%) scale(0.8)', opacity: 0.8 },
            '50%': { transform: 'translate(-50%, -50%) scale(1.1)', opacity: 0.3 },
          },
          '@keyframes cl-pulse': {
            '0%, 100%': { transform: 'translate(-50%, -50%) scale(1)', opacity: 0.85 },
            '50%': { transform: 'translate(-50%, -50%) scale(0.8)', opacity: 1 },
          },
          '&::before': {
            content: '""',
            position: 'absolute',
            top: '50%',
            left: '50%',
            width: '24px',
            height: '24px',
            borderRadius: 'var(--ds-radius-pill)',
            border: '2px solid var(--ds-yellow-600)',
            animation: 'cl-ripple 2s ease-in-out infinite',
          },
          '&::after': {
            content: '""',
            position: 'absolute',
            top: '50%',
            left: '50%',
            width: '14px',
            height: '14px',
            borderRadius: 'var(--ds-radius-pill)',
            backgroundColor: 'var(--ds-yellow-500)',
            animation: 'cl-pulse 2s ease-in-out infinite',
          },
          '@media (prefers-reduced-motion: reduce)': {
            '&::before': { animation: 'none', opacity: 0.5 },
            '&::after': { animation: 'none' },
          },
        }}
      />

      {/* Shimmer word + animated dots */}
      <Box
        sx={{
          height: '40px',
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--ds-space-1)',
          overflow: 'hidden',
        }}
      >
        <Box
          component='span'
          role='status'
          aria-live='polite'
          aria-label='Loading'
          sx={{
            fontFamily: 'var(--ds-font-display)',
            fontSize: 'var(--ds-text-body)',
            fontWeight: 'var(--ds-font-weight-medium)',
            lineHeight: 1,
            background: 'linear-gradient(90deg, var(--ds-brand-600), var(--ds-brand-400), var(--ds-brand-600))',
            backgroundSize: '200% 100%',
            backgroundClip: 'text',
            WebkitBackgroundClip: 'text',
            WebkitTextFillColor: 'transparent',
            '@keyframes cl-shimmer': {
              '0%': { backgroundPosition: '100% 0' },
              '100%': { backgroundPosition: '-100% 0' },
            },
            '@keyframes cl-fade': {
              '0%, 100%': { opacity: 0.6 },
              '50%': { opacity: 1 },
            },
            animation: 'cl-shimmer 2s linear infinite, cl-fade 2s ease-in-out infinite',
            '@media (prefers-reduced-motion: reduce)': {
              animation: 'none',
              WebkitTextFillColor: 'unset',
              background: 'none',
              color: 'var(--ds-brand-600)',
            },
          }}
        >
          {shuffledWords[currentWordIndex]}
        </Box>
        <Box
          component='span'
          aria-hidden='true'
          sx={{
            fontFamily: 'var(--ds-font-display)',
            fontSize: 'var(--ds-text-body)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: 'var(--ds-brand-600)',
            minWidth: '30px',
            lineHeight: 1,
          }}
        >
          {dots}
        </Box>
      </Box>
    </Box>
  );
};

export default ConversationLoader;
