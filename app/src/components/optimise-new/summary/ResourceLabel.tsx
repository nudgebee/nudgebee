import { Box } from '@mui/material';
import { CodeBlock } from '@ui/CodeBlock';
import Tooltip from '@ui/Tooltip';

/**
 * A resource / cluster / application name inside running prose or a card's meta
 * row. Uses the DS inline code chip (design-system.md §3: "Inline `code` chip
 * inside a sentence") rather than a hand-rolled pill.
 *
 * Live resource names run to full ARNs
 * (arn:aws:rds:us-east-1:740395098545:rdsreservedinstances:6be9a4d44a341064),
 * which would swamp a sentence or a card row. The middle is truncated so both the
 * service prefix and the identifying suffix survive; the full value goes in a
 * tooltip.
 */
const DEFAULT_MAX = 44;

/**
 * The identifying tail of a resource id — the part a reader actually recognises
 * and uses to find the thing. K8s ids are `namespace/Kind/name`
 * (`nudgebee/Deployment/ml-server`) and cloud ids are ARNs
 * (`arn:aws:rds:us-east-1:740395098545:db:main-rackspace`); in both, the final
 * `/`- or `:`-delimited segment is the resource's own name. The full id stays in
 * the hover tooltip.
 *
 * Note this is not unique on its own — the same workload name recurs across
 * namespaces — so only use it where the account/provider context sits beside it.
 */
export const resourceAnchorName = (resourceId: string): string => {
  if (!resourceId) return '';
  const trimmed = resourceId.replace(/[/:]+$/, '');
  const cut = Math.max(trimmed.lastIndexOf('/'), trimmed.lastIndexOf(':'));
  return trimmed.slice(cut + 1) || resourceId;
};

export const truncateMiddle = (text: string, max: number): string => {
  if (text.length <= max) return text;
  const head = Math.ceil((max - 1) / 2);
  const tail = Math.floor((max - 1) / 2);
  return `${text.slice(0, head)}…${text.slice(text.length - tail)}`;
};

export const ResourceLabel = ({ name, max = DEFAULT_MAX }: { name: string; max?: number }) => {
  const shown = truncateMiddle(name, max);
  const chip = <CodeBlock inline code={shown} />;
  if (shown === name) return chip;
  return (
    <Tooltip title={name} placement='top'>
      <Box component='span' sx={{ minWidth: 0 }}>
        {chip}
      </Box>
    </Tooltip>
  );
};

export default ResourceLabel;
