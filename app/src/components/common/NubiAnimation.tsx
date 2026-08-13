import React from 'react';

/**
 * Animated Nubi mascot — the Nudgebee bee performing an emotion/activity loop.
 *
 * Pure inline SVG animated by CSS classes; the motion rules live in
 * src/styles/nubi-animation.css (global import in _app.tsx, scoped under
 * `.nb-nubi`). Geometry is verbatim from public/branding/default/nubi-icon.svg
 * with extra hidden variant parts (eye/mouth expressions, fx like bubbles,
 * whoosh lines, z's) that each state's CSS reveals.
 *
 * Brand caveat: this is the Nudgebee mascot — never render it for white-label
 * tenants. Callers should gate on branding config the way Loader.tsx does.
 */

export type NubiAnimationState = 'thinking' | 'working' | 'searching' | 'loading' | 'flying' | 'excited' | 'waving' | 'alert' | 'sad' | 'sleeping';

interface NubiAnimationProps {
  state?: NubiAnimationState;
  /** Rendered width & height (square viewBox). Number = px. */
  size?: number | string;
  /** When set, the SVG is exposed to screen readers as an image with this label; otherwise it is decorative. */
  ariaLabel?: string;
  className?: string;
  style?: React.CSSProperties;
}

const NAVY = '#1b2d4a';
const HONEY = '#facf39';

const NubiAnimation: React.FC<NubiAnimationProps> = ({ state = 'flying', size = 150, ariaLabel, className, style }) => {
  return (
    <span className={`nb-nubi s-${state}${className ? ` ${className}` : ''}`} style={style} data-testid={`nubi-animation-${state}`}>
      {/* Size via CSS, not the width/height presentation attributes — those don't
          reliably accept a calc() length (e.g. ds.space.mul(...)) in stricter/older
          SVG environments. */}
      <svg
        viewBox='0 0 240 240'
        style={{ width: size, height: size }}
        xmlns='http://www.w3.org/2000/svg'
        role={ariaLabel ? 'img' : undefined}
        aria-label={ariaLabel}
        aria-hidden={ariaLabel ? undefined : true}
      >
        <ellipse className='n-shadow' cx='120' cy='228' rx='44' ry='6' fill={NAVY} />
        <g className='n-whoosh' stroke={HONEY} strokeWidth='6' strokeLinecap='round'>
          <line x1='97' y1='206' x2='93' y2='222' />
          <line x1='120' y1='211' x2='120' y2='228' />
          <line x1='143' y1='206' x2='147' y2='222' />
        </g>
        <g className='n-body'>
          <path
            className='n-head'
            fill={NAVY}
            d='M185.7,141.2c-2.1,8.1-4.7,15.5-7.5,22.1-.3.7-.6,1.3-.9,2-.2.4-.3.8-.5,1.2-1.4,3.1-2.9,6.1-4.5,8.9h0c-.7,1.2-1.3,2.4-2,3.6-.2.4-.4.7-.6,1.1-1.7,2.9-3.5,5.5-5.4,8-.6.8-1.2,1.6-1.8,2.4-1,1.3-2.1,2.5-3.1,3.8-1.4,1.7-2.9,3.2-4.4,4.7,0,0-.2.1-.2.2-1.8,1.8-3.7,3.5-5.5,5-3.3,2.7-6.6,5-9.9,6.8h0c-9.6,5.5-18.9,7.8-26.7,7.8-6.4,0-15.3-2.3-24.7-7.7-3.4-1.9-6.8-4.2-10.2-6.9-1.9-1.5-3.8-3.1-5.7-4.9,0,0-.2-.2-.3-.2-1.5-1.4-3-3-4.5-4.6-1.2-1.3-2.3-2.6-3.5-4-.6-.8-1.3-1.6-1.9-2.4-2-2.5-3.9-5.2-5.8-8.1-.2-.3-.5-.7-.7-1.1-.7-1.1-1.4-2.3-2.1-3.5,0,0,0,0,0,0-1.6-2.8-3.2-5.8-4.7-9-.2-.4-.4-.8-.6-1.2-.3-.6-.6-1.3-.9-1.9-2.9-6.7-5.5-14-7.5-22.2-10.5-41.7,9.5-91.6,72.7-90.6,63.2.9,84.2,49.1,73.3,90.6Z'
          />
          <path
            className='n-faceplate'
            fill='#fff'
            d='M102.5,72.5h19.9c25.2,0,45.6,20.5,45.6,45.6v1c0,25.2-20.5,45.6-45.6,45.6h-22.9c-23.5,0-42.7-19.1-42.7-42.7v-4c0-25.2,20.5-45.6,45.6-45.6Z'
          />
          <g className='n-eyes'>
            <rect className='n-eye' x='85.1' y='104' width='13.6' height='22' rx='6.8' fill={NAVY} />
            <rect className='n-eye' x='125' y='104' width='13.6' height='22' rx='6.8' fill={NAVY} />
            <path className='n-eye-closed' d='M85.1,115.5q6.8,6.5,13.6,0' fill='none' stroke={NAVY} strokeWidth='5' strokeLinecap='round' />
            <path className='n-eye-closed' d='M125,115.5q6.8,6.5,13.6,0' fill='none' stroke={NAVY} strokeWidth='5' strokeLinecap='round' />
            <path className='n-eye-happy' d='M85.1,120q6.8,-8,13.6,0' fill='none' stroke={NAVY} strokeWidth='6' strokeLinecap='round' />
            <path className='n-eye-happy' d='M125,120q6.8,-8,13.6,0' fill='none' stroke={NAVY} strokeWidth='6' strokeLinecap='round' />
          </g>
          <g className='n-mouths'>
            <path className='n-mouth-smile' d='M103.1,143.4c6.5,4.8,13,4.8,19.3,0' fill='none' stroke={NAVY} strokeWidth='6' strokeLinecap='round' />
            <path
              className='n-mouth-frown'
              d='M103.1,149.5c6.5,-4.8,13,-4.8,19.3,0'
              fill='none'
              stroke={NAVY}
              strokeWidth='6'
              strokeLinecap='round'
            />
            <path className='n-mouth-flat' d='M104.5,145.5h16.5' fill='none' stroke={NAVY} strokeWidth='6' strokeLinecap='round' />
            <path className='n-mouth-open' d='M100.6,140.5a12.15,11,0,0,0,24.3,0Z' fill={NAVY} />
            <ellipse className='n-mouth-o' cx='112.7' cy='146' rx='5.5' ry='6.5' fill={NAVY} />
          </g>
          <path
            className='n-band'
            fill={HONEY}
            d='M172.5,175.4c-.7,1.2-1.3,2.4-2,3.6-.2.4-.4.7-.6,1.1-1.7,2.9-3.5,5.5-5.4,8-.6.8-1.2,1.6-1.8,2.4-1,1.3-2.1,2.5-3.1,3.8-1.4,1.7-2.9,3.2-4.4,4.7-27.8,2.9-55.5,3-83.2,0-1.5-1.4-3-3-4.5-4.6-1.2-1.3-2.3-2.6-3.5-4-.6-.8-1.3-1.6-1.9-2.4-2-2.5-3.9-5.2-5.8-8.1-.2-.3-.5-.7-.7-1.1-.7-1.1-1.4-2.3-2.1-3.5,39.6,5.1,79.2,5.1,119-.1Z'
          />
          <g className='n-antenna'>
            <rect x='155.4' y='35.8' width='5.2' height='32.5' fill={NAVY} />
            <circle className='n-ball' cx='157.5' cy='30.6' r='11.5' fill={HONEY} />
            <circle className='n-ping' cx='157.5' cy='30.6' r='11.5' fill='none' stroke={HONEY} strokeWidth='5' />
          </g>
          <g className='n-wing'>
            <rect x='174.3' y='90.4' width='26.2' height='57.7' rx='13.1' fill={HONEY} />
          </g>
          <g className='n-sparks'>
            <line x1='187.5' y1='50.1' x2='197.3' y2='32.2' stroke={HONEY} strokeWidth='7' strokeLinecap='round' />
            <line x1='199.5' y1='60.5' x2='217.9' y2='52.1' stroke={HONEY} strokeWidth='7' strokeLinecap='round' />
            <line x1='200' y1='76.9' x2='219.7' y2='81.7' stroke={HONEY} strokeWidth='7' strokeLinecap='round' />
          </g>
          <g className='n-sparks-l-wrap' transform='translate(240,0) scale(-1,1)'>
            <g className='n-sparks-l'>
              <line x1='187.5' y1='50.1' x2='197.3' y2='32.2' stroke={HONEY} strokeWidth='7' strokeLinecap='round' />
              <line x1='199.5' y1='60.5' x2='217.9' y2='52.1' stroke={HONEY} strokeWidth='7' strokeLinecap='round' />
              <line x1='200' y1='76.9' x2='219.7' y2='81.7' stroke={HONEY} strokeWidth='7' strokeLinecap='round' />
            </g>
          </g>
        </g>
        <g className='n-fx'>
          <g className='n-dots' fill={NAVY}>
            <circle cx='76' cy='56' r='3.5' />
            <circle cx='61' cy='40' r='5' />
            <circle cx='44' cy='21' r='6.5' />
          </g>
          <g className='n-zzz' fill={NAVY} aria-hidden='true'>
            <text x='186' y='74' fontSize='15'>
              z
            </text>
            <text x='201' y='52' fontSize='19'>
              z
            </text>
            <text x='217' y='30' fontSize='24'>
              z
            </text>
          </g>
          <g className='n-loaddots' fill='none' stroke={NAVY} strokeWidth='7'>
            <circle cx='80' cy='66' r='6.5' />
            <circle cx='64' cy='40' r='11' />
            <circle cx='44' cy='4' r='18' />
          </g>
          <g className='n-alert'>
            <rect x='47' y='22' width='10' height='26' rx='5' fill={HONEY} />
            <circle cx='52' cy='58' r='5.5' fill={HONEY} />
          </g>
        </g>
      </svg>
    </span>
  );
};

export default NubiAnimation;
