import { Box } from '@mui/material';
import { useState, useEffect, useMemo, useRef, useCallback } from 'react';
import { ds } from '@utils/colors';

const NUDGEBEE_WORDS = [
  'Bee with you in a moment…',
  'Bee right back with this…',
  'Hmm, let me think on this…',
  'Just connecting a few dots…',
  'Let me consult my hive notes…',
  'Buzzing around for the answer…',
  'Flying off to grab the answer…',
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

const CATEGORY_MESSAGES = {
  small_talk: ['Bee with you in a moment…', 'Bee right back with this…', 'Hmm, let me think on this…', 'On it…', 'One sec…'],
  kubernetes: [
    'Scanning your cluster…',
    'Checking your pods…',
    'Reading the workload state…',
    'Talking to the cluster…',
    'Inspecting your namespaces…',
    'Pulling node metrics…',
  ],
  cloud: [
    'Pulling your cloud data…',
    'Checking your cloud spend…',
    'Scanning your cloud resources…',
    'Reading your account details…',
    'Fetching resource inventory…',
  ],
  logs: ['Digging through the logs…', 'Tracing that error…', 'Scanning recent events…', 'Following the trail…', 'Combing through the stack…'],
  workflow: ['Loading the runbook…', 'Checking the automation…', 'Spinning up the workflow…', 'Queuing that up…', 'Prepping the pipeline…'],
};

function classifyQuery(query) {
  if (!query) return null;
  const q = query.toLowerCase();
  if (/\b(hi|hello|hey|thanks|thank you|how are you|good morning|good afternoon|good evening|bye|goodbye)\b/.test(q)) return 'small_talk';
  if (
    /\b(pod|pods|deployment|deployments|node|nodes|namespace|namespaces|cluster|ingress|k8s|kubernetes|container|containers|statefulset|daemonset|helm|kubectl|crashloop|oomkill|workload|hpa|replica)\b/.test(
      q
    )
  )
    return 'kubernetes';
  if (/\b(aws|gcp|azure|cloud|cost|spend|bill|billing|budget|ec2|s3|rds|lambda|fargate|eks|gke|aks|cloudwatch|vpc)\b/.test(q)) return 'cloud';
  if (
    /\b(log|logs|error|errors|crash|exception|trace|debug|stacktrace|stdout|stderr|warning|alert|panic|fatal|datadog|grafana|prometheus|loki|metrics)\b/.test(
      q
    )
  )
    return 'logs';
  if (/\b(workflow|runbook|automation|trigger|automate|autopilot|pipeline|schedule)\b/.test(q)) return 'workflow';
  return null;
}

const getRandomDelay = () => Math.floor(Math.random() * (10000 - 4000 + 1)) + 4000;

const ConversationLoader = ({ query }) => {
  const [currentWordIndex, setCurrentWordIndex] = useState(0);
  const [dotCount, setDotCount] = useState(1);
  const timeoutRef = useRef(null);

  const pool = useMemo(() => {
    const category = classifyQuery(query);
    return category ? CATEGORY_MESSAGES[category] : NUDGEBEE_WORDS;
  }, [query]);

  const shuffledWords = useMemo(() => [...pool].sort(() => Math.random() - 0.5), [pool]);

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
        gridTemplateColumns: `${ds.space.mul(0, 18)} 1fr`,
        alignItems: 'center',
        gap: 'var(--ds-space-3)',
        borderRadius: 'var(--ds-radius-sm)',
      }}
    >
      {/* Ripple + pulse dot — brand yellow */}
      <Box
        sx={{
          height: ds.space.mul(0, 18),
          width: ds.space.mul(0, 18),
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
            width: ds.space[5],
            height: ds.space[5],
            borderRadius: 'var(--ds-radius-pill)',
            border: `${ds.space[0]} solid var(--ds-yellow-600)`,
            animation: 'cl-ripple 2s ease-in-out infinite',
          },
          '&::after': {
            content: '""',
            position: 'absolute',
            top: '50%',
            left: '50%',
            width: ds.space.mul(0, 7),
            height: ds.space.mul(0, 7),
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
          height: ds.space.mul(0, 20),
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
            lineHeight: 1.5,
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
            minWidth: ds.space.mul(0, 15),
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
