import { Box, Typography, Collapse } from '@mui/material';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import KeyboardArrowRightIcon from '@mui/icons-material/KeyboardArrowRight';
import { useState } from 'react';
import { colors } from 'src/utils/colors';
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
    <Box sx={{ mb: '14px', pl: '32px' }}>
      <Box sx={{ px: '8px', mb: '4px' }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
          <Typography sx={{ fontSize: '12px', fontWeight: 700, color: colors.text.secondary }}>{meta.label}</Typography>
          {groupDollars > 0 && (
            <Typography sx={{ fontSize: '12px', fontWeight: 600, color: colors.text.primary }}>{formatDollars(groupDollars)}/mo</Typography>
          )}
          <Typography sx={{ fontSize: '11px', color: colors.text.quaternary }}>
            {`across ${allItems.length} resource${allItems.length === 1 ? '' : 's'}`}
          </Typography>
        </Box>
        {summaryLine && (
          <Typography sx={{ fontSize: '11px', color: colors.text.quaternary, mt: '1px', fontStyle: 'italic' }}>{summaryLine}</Typography>
        )}
      </Box>

      {visibleItems.map((item) => (
        <InsightCard key={item.id} item={item} onClickResource={onClickResource} onAskNubi={onAskNubi} />
      ))}

      {hasOverflow && (
        <Box
          onClick={() => setExpanded(!expanded)}
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: '4px',
            px: '12px',
            py: '5px',
            cursor: 'pointer',
            '&:hover': { backgroundColor: colors.background.tertiaryLightestestest },
          }}
        >
          {expanded ? (
            <KeyboardArrowDownIcon sx={{ fontSize: '14px', color: colors.text.quaternary }} />
          ) : (
            <KeyboardArrowRightIcon sx={{ fontSize: '14px', color: colors.text.quaternary }} />
          )}
          <Typography sx={{ fontSize: '11px', color: colors.text.quaternary }}>{expanded ? 'Show fewer' : overflowLabel(overflow)}</Typography>
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
  category: _category,
  label,
  oneLiner,
  conversationalSummary,
  subCategories,
  items,
  sortBy,
  onClickResource,
  onAskNubi,
}: CategorySectionProps) => {
  const [collapsed, setCollapsed] = useState(true);
  if (items.length === 0) return null;

  return (
    <Box sx={{ mb: '24px' }}>
      <Box
        onClick={() => setCollapsed(!collapsed)}
        sx={{
          p: '12px 16px',
          cursor: 'pointer',
          position: 'sticky',
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          top: 0,
          borderRadius: '12px',
          backgroundColor: colors.background.tertiaryLightestest,
          zIndex: 1,
          borderBottom: `1px solid ${colors.border.secondaryLightest}`,
          '&:hover': { backgroundColor: colors.background.tertiaryLightestestest },
        }}
      >
        <Box sx={{}}>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
            <Typography sx={{ fontSize: '14px', fontWeight: 700, color: colors.text.secondary }}>{label}</Typography>
            <Typography sx={{ fontSize: '13px', color: colors.text.tertiary, flex: 1 }}>&mdash; {oneLiner}</Typography>
          </Box>
          {conversationalSummary && (
            <Typography sx={{ fontSize: '11.5px', color: colors.text.tertiary, mt: '2px', fontStyle: 'italic' }}>{conversationalSummary}</Typography>
          )}
        </Box>
        {collapsed ? (
          <KeyboardArrowRightIcon sx={{ fontSize: '18px', color: colors.text.tertiary }} />
        ) : (
          <KeyboardArrowDownIcon sx={{ fontSize: '18px', color: colors.text.tertiary }} />
        )}
      </Box>

      <Collapse in={!collapsed}>
        <Box sx={{ pt: '12px' }}>
          {subCategories.map((sub) => (
            <SubCategoryGroup key={sub.key} meta={sub} items={items} sortBy={sortBy} onClickResource={onClickResource} onAskNubi={onAskNubi} />
          ))}
        </Box>
      </Collapse>
    </Box>
  );
};

export default CategorySection;
