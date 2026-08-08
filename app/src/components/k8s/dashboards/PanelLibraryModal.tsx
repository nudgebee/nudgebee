import React, { useMemo, useState } from 'react';
import { Box, Stack, Typography } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import { Card } from '@ui/Card';
import { Chip } from '@ui/Chip';
import { EmptyState } from '@ui/EmptyState';
import SearchInput from '@ui/SearchInput';
import { ToggleGroup } from '@ui/ToggleGroup';
import { ds } from '@utils/colors';
import type { Panel } from '@api1/dashboards';
import {
  panelFromTemplate,
  PANEL_TEMPLATES,
  roleLabel,
  TEMPLATE_ROLES,
  type PanelTemplate,
  type TemplateRole,
  type WidgetCategory,
} from './panelTemplates';

interface Props {
  open: boolean;
  /** The dashboard's current panels — the copy takes the next free id from them. */
  existingPanels: Panel[];
  onClose: () => void;
  /** Handed a ready-to-edit copy; the caller opens the panel editor on it. */
  onPick: (panel: Panel) => void;
}

/** Role filter value meaning "every widget". */
const ALL_ROLES = 'all';

/** Reading order: money, then what is broken, then how it is running. */
const CATEGORY_ORDER: WidgetCategory[] = ['Cost', 'Issues', 'Reliability', 'Capacity', 'Performance', 'Workload'];

/**
 * Picks one widget out of the library.
 *
 * The chosen widget is copied, not referenced, and the copy goes straight to
 * the panel editor rather than onto the dashboard — a widget carries no account
 * scope, and the account is the one thing only the author can supply. That also
 * means the query is in front of them before anything is saved, which is the
 * point: a library panel is a starting point to edit, not a black box.
 */
const PanelLibraryModal: React.FC<Props> = ({ open, existingPanels, onClose, onPick }) => {
  const [role, setRole] = useState<TemplateRole | typeof ALL_ROLES>(ALL_ROLES);
  const [search, setSearch] = useState('');

  const roleOptions = useMemo(() => [{ value: ALL_ROLES, label: 'All' }, ...TEMPLATE_ROLES.map((r) => ({ value: r.value, label: r.label }))], []);

  const matches = useMemo(() => {
    const needle = search.trim().toLowerCase();
    return PANEL_TEMPLATES.filter((t) => {
      if (role !== ALL_ROLES && !t.roles.includes(role)) return false;
      if (!needle) return true;
      // Title and summary are what someone reads; the query is what they may be
      // hunting for by metric name ("kube_pod_container_status_restarts_total").
      // The whole target is stringified rather than just `expr`, because an
      // entity or trace widget stores a `query` object instead — searching
      // "recommendation_groupings" or a column name found nothing while those
      // were the only widgets that could not be matched on their query.
      const haystack = `${t.panel.title} ${t.summary} ${t.category} ${JSON.stringify(t.panel.targets || [])}`.toLowerCase();
      return haystack.includes(needle);
    });
  }, [role, search]);

  const byCategory = useMemo(() => {
    const groups: { category: WidgetCategory; widgets: PanelTemplate[] }[] = [];
    for (const category of CATEGORY_ORDER) {
      const widgets = matches.filter((t) => t.category === category);
      if (widgets.length > 0) groups.push({ category, widgets });
    }
    return groups;
  }, [matches]);

  const close = () => {
    setSearch('');
    setRole(ALL_ROLES);
    onClose();
  };

  const pick = (template: PanelTemplate) => {
    onPick(panelFromTemplate(template, existingPanels));
    setSearch('');
    setRole(ALL_ROLES);
  };

  return (
    <Modal
      open={open}
      handleClose={close}
      title='Add a panel from the library'
      subtitle='Pick one to open it in the panel editor, where you choose the account it queries.'
      width='md'
      actionButtons={
        <Stack direction='row' gap='12px' sx={{ button: { minWidth: '140px' } }}>
          <Button tone='secondary' onClick={close} id='panel-library-cancel-btn'>
            Cancel
          </Button>
        </Stack>
      }
    >
      <Stack gap={1.5}>
        <Stack direction='row' alignItems='center' gap={1.5} flexWrap='wrap'>
          <ToggleGroup
            selection='single'
            size='sm'
            ariaLabel='Filter widgets by role'
            value={role}
            options={roleOptions}
            onChange={(next) => setRole(next as TemplateRole | typeof ALL_ROLES)}
            id='panel-library-role-toggle'
          />
          <SearchInput value={search} onChange={setSearch} label='Search widgets' id='panel-library-search' />
        </Stack>

        {byCategory.length === 0 ? (
          <EmptyState size='section' illustration='no-results' title='No widget matches' description='Try another role, or clear the search.' />
        ) : (
          // Scrolled here rather than by the modal body so the filters stay put
          // while a long category list moves under them.
          <Box sx={{ maxHeight: 420, overflowY: 'auto', pr: 0.5 }} data-testid='panel-library-list'>
            {byCategory.map(({ category, widgets }) => (
              <Box key={category} sx={{ mb: 2 }}>
                <Typography
                  sx={{
                    fontFamily: 'var(--ds-font-display)',
                    fontSize: 13,
                    fontWeight: 620,
                    color: ds.gray[600],
                    mb: 0.75,
                    textTransform: 'uppercase',
                  }}
                >
                  {category}
                </Typography>
                <Stack gap={0.75}>
                  {widgets.map((widget) => (
                    <Card
                      key={widget.id}
                      variant='outlined'
                      elevation='flat'
                      size='sm'
                      interactive
                      onClick={() => pick(widget)}
                      data-testid={`widget-${widget.id}`}
                    >
                      <Stack direction='row' alignItems='center' gap={1.5}>
                        <Box sx={{ minWidth: 0, flex: 1 }}>
                          <Typography sx={{ fontFamily: 'var(--ds-font-display)', fontSize: 13.5, fontWeight: 600, color: ds.gray[700] }} noWrap>
                            {widget.panel.title}
                          </Typography>
                          <Typography variant='caption' sx={{ color: ds.gray[500], display: 'block' }}>
                            {widget.summary}
                          </Typography>
                        </Box>
                        <Stack direction='row' gap={0.5} sx={{ flexShrink: 0 }}>
                          <Chip size='2xs' tone='subtle'>
                            {widget.panel.type}
                          </Chip>
                          <Chip size='2xs' tone='subtle'>
                            {widget.panel.datasource}
                          </Chip>
                          {widget.roles.slice(0, 2).map((r) => (
                            <Chip key={r} size='2xs' tone='info'>
                              {roleLabel(r)}
                            </Chip>
                          ))}
                        </Stack>
                      </Stack>
                    </Card>
                  ))}
                </Stack>
              </Box>
            ))}
          </Box>
        )}
      </Stack>
    </Modal>
  );
};

export default PanelLibraryModal;
