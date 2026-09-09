import { Box, Typography } from '@mui/material';
import { ds } from '@utils/colors';

/**
 * Shared building blocks for the security detail panels.
 *
 * Two panels use them — one for a CVE finding on an image or VM, one for a CIS
 * rule on a cluster. Their content models differ enough to warrant separate
 * components (a rule has no package or fixed version), but the type scale,
 * label column and section rules should not drift apart between them.
 */

export const SectionHeading = ({ children }: { children: React.ReactNode }) => (
  <Typography
    sx={{
      fontSize: ds.text.caption,
      fontWeight: ds.weight.medium,
      color: ds.gray[500],
      textTransform: 'uppercase',
      letterSpacing: '0.08em',
      pb: ds.space[1],
      mb: ds.space[2],
      borderBottom: `1px solid ${ds.gray[200]}`,
    }}
  >
    {children}
  </Typography>
);

export const Field = ({ label, children }: { label: string; children: React.ReactNode }) => (
  <>
    <Typography component='dt' sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>
      {label}
    </Typography>
    <Box component='dd' sx={{ m: 0, fontSize: ds.text.small, color: ds.gray[700], wordBreak: 'break-word' }}>
      {children}
    </Box>
  </>
);

export const FieldList = ({ children }: { children: React.ReactNode }) => (
  <Box component='dl' sx={{ display: 'grid', gridTemplateColumns: '132px 1fr', gap: `${ds.space[1]} ${ds.space[3]}`, m: 0 }}>
    {children}
  </Box>
);

export const Mono = ({ children }: { children: React.ReactNode }) => (
  <Box component='span' sx={{ fontFamily: 'var(--ds-font-mono)', fontSize: ds.text.small }}>
    {children}
  </Box>
);

export const Section = ({ title, children }: { title: string; children: React.ReactNode }) => (
  <Box sx={{ mb: ds.space[5] }}>
    <SectionHeading>{title}</SectionHeading>
    {children}
  </Box>
);

export const EmptyNote = ({ children }: { children: React.ReactNode }) => (
  <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>{children}</Typography>
);

/**
 * Footer action bar, pinned below the scroll area. Matches the cost panel's
 * ActionBar treatment (optimise-new/ActionBar.tsx) so the security panels sit
 * in the same visual family as Recommendations.
 */
export const PanelActions = ({ children, testId }: { children: React.ReactNode; testId?: string }) => (
  <Box
    data-testid={testId}
    sx={{
      borderTop: `1px solid ${ds.gray[200]}`,
      backgroundColor: ds.background[100],
      flexShrink: 0,
      px: ds.space[4],
      py: ds.space.mul(0, 6),
      display: 'flex',
      alignItems: 'center',
      gap: ds.space[2],
      flexWrap: 'wrap',
    }}
  >
    {children}
  </Box>
);
