import { Box, Typography } from '@mui/material';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import KeyboardArrowRightIcon from '@mui/icons-material/KeyboardArrowRight';
import { useState } from 'react';
import { ds } from 'src/utils/colors';
import { CollapsableCard } from '@components1/ds/CollapsableCard';
import { Button } from '@components1/ds/Button';
import {
  sortInsights,
  subtotal,
  formatDollars,
  overflowSummary,
  subCategorySummaryLine,
  type InsightItem,
  type SubCategoryMeta,
  type MainCategory,
  type SortKey,
} from './insights';
import InsightCard from './InsightCard';

const overflowLabel = (overflow: { count: number; dollars: number }): string => {
  const suffix = overflow.dollars > 0 ? ` · ${formatDollars(overflow.dollars)}/mo` : '';
  return `+${overflow.count} more${suffix}`;
};

// ─── Sub-category group ────────────────────────────────────────────────────

interface SubCategoryGroupProps {
  meta: SubCategoryMeta;
  items: InsightItem[];
  sortBy: SortKey;
  onClickResource: (id: string) => void;
  onAskNubi?: (item: InsightItem) => void;
}

const SubCategoryGroup = ({ meta, items, sortBy, onClickResource, onAskNubi }: SubCategoryGroupProps) => {
  const allItems = sortInsights(
    items.filter((i) => i.subCategory === meta.key),
    sortBy
  );
  const [expanded, setExpanded] = useState(false);

  if (allItems.length === 0) return null;

  const visibleItems = expanded ? allItems : allItems.slice(0, meta.maxShown);
  const overflow = overflowSummary(allItems, meta.maxShown);
  const groupDollars = subtotal(allItems);
  const hasOverflow = overflow.count > 0;
  const summaryLine = subCategorySummaryLine(allItems);

  return (
    <Box sx={{ mb: ds.space[3], pl: ds.space[6] }}>
      <Box sx={{ px: ds.space[2], mb: ds.space[1] }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
          <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>{meta.label}</Typography>
          {groupDollars > 0 && (
            <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, color: ds.blue[600] }}>
              {formatDollars(groupDollars)}/mo
            </Typography>
          )}
          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>
            {`across ${allItems.length} resource${allItems.length === 1 ? '' : 's'}`}
          </Typography>
        </Box>
        {summaryLine && (
          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], mt: 'var(--ds-space-1)', fontStyle: 'italic' }}>{summaryLine}</Typography>
        )}
      </Box>

      {visibleItems.map((item) => (
        <InsightCard key={item.id} item={item} onClickResource={onClickResource} onAskNubi={onAskNubi} />
      ))}

      {hasOverflow && (
        <Box sx={{ px: ds.space[3], py: ds.space[1] }}>
          <Button
            tone='ghost'
            size='xs'
            icon={expanded ? <KeyboardArrowDownIcon /> : <KeyboardArrowRightIcon />}
            iconPlacement='start'
            onClick={() => setExpanded(!expanded)}
          >
            {expanded ? 'Show fewer' : overflowLabel(overflow)}
          </Button>
        </Box>
      )}
    </Box>
  );
};

// ─── Main category section ─────────────────────────────────────────────────

interface CategorySectionProps {
  category: MainCategory;
  label: string;
  oneLiner: string;
  conversationalSummary: string;
  subCategories: SubCategoryMeta[];
  items: InsightItem[];
  sortBy: SortKey;
  onClickResource: (id: string) => void;
  onAskNubi?: (item: InsightItem) => void;
}

const CategorySection = ({
  category,
  label,
  oneLiner,
  conversationalSummary,
  subCategories,
  items,
  sortBy,
  onClickResource,
  onAskNubi,
}: CategorySectionProps) => {
  if (items.length === 0) return null;

  const headerNode = (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
        <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>{label}</Typography>
        <Typography sx={{ fontSize: ds.text.body, color: ds.gray[600], flex: 1 }}>&mdash; {oneLiner}</Typography>
      </Box>
      {conversationalSummary && (
        <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[600], mt: 'var(--ds-space-1)', fontStyle: 'italic' }}>
          {conversationalSummary}
        </Typography>
      )}
    </Box>
  );

  return (
    <CollapsableCard
      id={`category-section-${category}`}
      defaultOpen={false}
      persist='local'
      composition='header+body'
      header={headerNode}
      sx={{ mb: ds.space[5] }}
    >
      <Box sx={{ pt: ds.space[3] }}>
        {subCategories.map((sub) => (
          <SubCategoryGroup key={sub.key} meta={sub} items={items} sortBy={sortBy} onClickResource={onClickResource} onAskNubi={onAskNubi} />
        ))}
      </Box>
    </CollapsableCard>
  );
};

export default CategorySection;
