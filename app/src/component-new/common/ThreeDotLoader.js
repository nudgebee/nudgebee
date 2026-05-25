/**
 * ThreeDotLoader — small inline dot-pulse animation for "data is loading"
 * states. Domain composition, not a generic primitive.
 *
 * Use cases:
 *   - Short async operations where a content-shaped Skeleton would be visual
 *     overkill and a measurable ProgressLinear is wrong (no progress to show).
 *   - "AI/LLM is generating" small inline indicator, often paired with a label.
 *
 * Previously deprecated 2026-05-07 → demoted to domain composition 2026-05-07.
 * Distinct from:
 *   - `Skeleton` (@components1/ds/Skeleton)  — content-shaped placeholder
 *   - `ProgressLinear` (@components1/k8s/common/LinearLoader) — measurable ops
 *
 * `ThreeDotsLoaderText` is the inline-text variant rendering "." → ".." → "..."
 * — pair with any "Loading"-style label.
 */
import React, { useState, useEffect } from 'react';

const ThreeDotLoader = () => {
  return (
    <div className='dot-pulse-container'>
      <div className='dot-pulse' />
    </div>
  );
};

const ThreeDotsLoaderText = () => {
  const [dots, setDots] = useState('');

  useEffect(() => {
    const interval = setInterval(() => {
      setDots((prevDots) => {
        if (prevDots === '...') {
          return '';
        }
        return prevDots + '.';
      });
    }, 500);

    return () => clearInterval(interval);
  }, []);

  return <span className='text-dot-loader'>{dots}</span>;
};

export default ThreeDotLoader;
export { ThreeDotsLoaderText };
