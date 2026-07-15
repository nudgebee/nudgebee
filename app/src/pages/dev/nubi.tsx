import React, { useEffect, useState } from 'react';
import { Box, Button, Typography } from '@mui/material';
import Loader from '@shared/Loader';
import NubiAnimation, { type NubiAnimationState } from '@shared/NubiAnimation';

/**
 * Dev-only preview page for the Nubi mascot animations (/dev/nubi).
 *
 * Exists because the real full-screen Loader unmounts as soon as its parent
 * finishes loading — usually too fast to visually review. Here it can be held
 * on screen for a fixed duration or indefinitely, and every animation state
 * can be compared side by side at full and indicator size.
 *
 * 404s in production builds via getStaticProps.
 */

const STATES: NubiAnimationState[] = ['flying', 'thinking', 'working', 'searching', 'loading', 'excited', 'waving', 'alert', 'sad', 'sleeping'];

export async function getStaticProps() {
  if (process.env.NODE_ENV === 'production') return { notFound: true };
  return { props: {} };
}

export default function NubiAnimationPreviewPage() {
  // null = hidden, Infinity = until clicked, otherwise auto-hides after `ms`.
  const [loaderMs, setLoaderMs] = useState<number | null>(null);

  useEffect(() => {
    if (loaderMs === null || loaderMs === Infinity) return;
    const t = setTimeout(() => setLoaderMs(null), loaderMs);
    return () => clearTimeout(t);
  }, [loaderMs]);

  if (loaderMs !== null) {
    return (
      <Box
        onClick={() => setLoaderMs(null)}
        sx={{ position: 'fixed', inset: 0, zIndex: 9999, bgcolor: 'background.default', cursor: 'pointer' }}
        title='Click to dismiss'
      >
        <Loader />
      </Box>
    );
  }

  return (
    <Box sx={{ p: 3, maxWidth: 1100, mx: 'auto' }}>
      <Typography variant='h5' sx={{ fontWeight: 700, mb: 0.5 }}>
        Nubi animation preview
      </Typography>
      <Typography variant='body2' color='text.secondary' sx={{ mb: 2 }}>
        Dev-only page. Simulate the full-screen app loader (the flying state), or compare all states below.
      </Typography>

      <Box sx={{ display: 'flex', gap: 1, mb: 4, flexWrap: 'wrap' }}>
        <Button variant='contained' data-testid='simulate-loader-5s-btn' onClick={() => setLoaderMs(5000)}>
          Show loader 5s
        </Button>
        <Button variant='contained' data-testid='simulate-loader-15s-btn' onClick={() => setLoaderMs(15000)}>
          Show loader 15s
        </Button>
        <Button variant='outlined' data-testid='simulate-loader-hold-btn' onClick={() => setLoaderMs(Infinity)}>
          Hold loader (click to dismiss)
        </Button>
      </Box>

      <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))', gap: 2 }}>
        {STATES.map((state) => (
          <Box
            key={state}
            sx={{ border: 1, borderColor: 'divider', borderRadius: 2, p: 2, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 1 }}
          >
            <NubiAnimation state={state} size={130} />
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5 }}>
              <NubiAnimation state={state} size={40} />
              <Typography variant='subtitle2' sx={{ textTransform: 'capitalize' }}>
                {state}
              </Typography>
            </Box>
          </Box>
        ))}
      </Box>
    </Box>
  );
}
