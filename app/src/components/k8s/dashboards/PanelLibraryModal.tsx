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
import type { AccountOption, Panel } from '@api1/dashboards';
import PanelPreview, { PREVIEW_RAIL_WIDTH, usePreviewRange } from './PanelPreview';
import { describePanelScope } from './panelAccounts';
import {
  panelFromTemplate,
  PANEL_TEMPLATES,
  roleLabel,
  TEMPLATE_ROLES,
  WIDGET_CATEGORIES,
  type PanelTemplate,
  type TemplateRole,
  type WidgetCategory,
} from './panelTemplates';
import type { VariableValues } from './templating';

interface Props {
  open: boolean;
  /** The dashboard's current panels — the copy takes the next free id from them. */
  existingPanels: Panel[];
  /** The picker never asks — it pre-scopes from the datasource and previews the result. */
  accountOptions: AccountOption[];
  /** Passed to the preview so it runs the widget the way the dashboard will. */
  variables?: VariableValues;
  startTime?: number;
  endTime?: number;
  onClose: () => void;
  /** Adds the copy. May be async; the modal stays open so several can be added. */
  onAdd: (panel: Panel) => void | Promise<void>;
}

/** Role filter value meaning "every widget". */
const ALL_ROLES = 'all';

/** The widget's query as text, whichever of the two shapes it is stored in. */
function queryText(panel: Panel): string {
  const target = panel.targets?.[0];
  if (target?.expr) return target.expr;
  if (target?.query) return JSON.stringify(target.query, null, 2);
  return '';
}

/** Picks one widget out of the library: selecting previews it, the footer's Add copies it onto the dashboard. */
const PanelLibraryModal: React.FC<Props> = ({ open, existingPanels, accountOptions, variables, startTime, endTime, onClose, onAdd }) => {
  const [role, setRole] = useState<TemplateRole | typeof ALL_ROLES>(ALL_ROLES);
  const [search, setSearch] = useState('');
  const [selected, setSelected] = useState<PanelTemplate | null>(null);
  const [adding, setAdding] = useState(false);
  /** What has been added this visit — the confirmation toast renders behind the modal. */
  const [addedIds, setAddedIds] = useState<string[]>([]);
  const fallbackRange = usePreviewRange(open);

  const roleOptions = useMemo(() => [{ value: ALL_ROLES, label: 'All' }, ...TEMPLATE_ROLES.map((r) => ({ value: r.value, label: r.label }))], []);

  const matches = useMemo(() => {
    const needle = search.trim().toLowerCase();
    return PANEL_TEMPLATES.filter((t) => {
      if (role !== ALL_ROLES && !t.roles.includes(role)) return false;
      if (!needle) return true;
      // Title and summary are what someone reads; the query is what they may be hunting for by metric name
      // ("kube_pod_container_status_restarts_total").
      const haystack = `${t.panel.title} ${t.summary} ${t.category} ${JSON.stringify(t.panel.targets || [])}`.toLowerCase();
      return haystack.includes(needle);
    });
  }, [role, search]);

  const byCategory = useMemo(() => {
    const groups: { category: WidgetCategory; widgets: PanelTemplate[] }[] = [];
    for (const category of WIDGET_CATEGORIES) {
      const widgets = matches.filter((t) => t.category === category);
      if (widgets.length > 0) groups.push({ category, widgets });
    }
    return groups;
  }, [matches]);

  /** The copy as it would land — the same call Add makes. */
  const previewPanel = useMemo(
    () => (selected ? panelFromTemplate(selected, existingPanels, accountOptions) : null),
    [selected, existingPanels, accountOptions]
  );

  const close = () => {
    setSearch('');
    setRole(ALL_ROLES);
    setSelected(null);
    setAddedIds([]);
    onClose();
  };

  const add = async () => {
    if (!selected || !previewPanel) return;
    setAdding(true);
    try {
      await onAdd(previewPanel);
      setAddedIds((prev) => (prev.includes(selected.id) ? prev : [...prev, selected.id]));
    } finally {
      setAdding(false);
    }
  };

  return (
    <Modal
      open={open}
      handleClose={close}
      title='Add a panel from the library'
      subtitle='Pick one to preview it with your data. Adding puts it straight on the dashboard, scoped to the account its query needs.'
      width='lg'
      maxHeight='85vh'
      contentStyles={{ padding: 0, overflow: 'hidden', display: 'flex' }}
      actionButtons={
        <Stack direction='row' gap='12px' sx={{ button: { minWidth: '140px' } }}>
          <Button tone='secondary' onClick={close} disabled={adding} id='panel-library-close-btn'>
            Close
          </Button>
          <Button onClick={add} disabled={!selected || adding} loading={adding} id='panel-library-add-btn' data-testid='panel-library-add-btn'>
            Add to dashboard
          </Button>
        </Stack>
      }
    >
      <Box
        sx={{
          flex: 1,
          minWidth: 0,
          display: 'flex',
          flexDirection: { xs: 'column', md: 'row' },
          alignItems: 'stretch',
          overflowY: { xs: 'auto', md: 'hidden' },
        }}
      >
        <Box
          sx={{
            flex: 1,
            minWidth: 0,
            display: 'flex',
            flexDirection: 'column',
            overflow: { xs: 'visible', md: 'hidden' },
            padding: 'var(--ds-space-5) var(--ds-space-6)',
          }}
        >
          <Stack direction='row' alignItems='center' gap={1.5} flexWrap='wrap' sx={{ mb: 1.5, flexShrink: 0 }}>
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
            // Only the list scrolls, so the filters stay put.
            <Box sx={{ flex: 1, minHeight: 0, overflowY: { xs: 'visible', md: 'auto' }, pr: 0.5 }} data-testid='panel-library-list'>
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
                        onClick={() => setSelected(widget)}
                        data-testid={`widget-${widget.id}`}
                        sx={selected?.id === widget.id ? { borderColor: ds.blue[500], background: ds.blue[100] } : undefined}
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
                            {addedIds.includes(widget.id) && (
                              <Chip size='2xs' tone='success' data-testid={`widget-added-${widget.id}`}>
                                Added
                              </Chip>
                            )}
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
        </Box>

        <Box
          sx={{
            width: { xs: '100%', md: PREVIEW_RAIL_WIDTH },
            flexShrink: 0,
            overflowY: 'auto',
            padding: 'var(--ds-space-5)',
            background: ds.background[200],
            borderLeft: { xs: 'none', md: `1px solid ${ds.gray[200]}` },
            borderTop: { xs: `1px solid ${ds.gray[200]}`, md: 'none' },
          }}
        >
          {selected && previewPanel ? (
            <Stack gap={2}>
              <PanelPreview
                panel={previewPanel}
                accountOptions={accountOptions}
                variables={variables || {}}
                startTime={startTime ?? fallbackRange.start}
                endTime={endTime ?? fallbackRange.end}
              />
              {/* What the row could not fit — `description` previously surfaced only
                  as the panel's hover tooltip, after it had been added. */}
              <Box>
                {previewPanel.description && (
                  <Typography variant='body2' sx={{ color: ds.gray[600], mb: 1.5 }}>
                    {previewPanel.description}
                  </Typography>
                )}
                <Stack gap={0.75}>
                  <DetailRow label='Data source' value={previewPanel.datasource} />
                  <DetailRow label='Visualisation' value={previewPanel.type} />
                  {previewPanel.unit && <DetailRow label='Unit' value={previewPanel.unit} />}
                  {/* The one thing Add decides for you — including "No account". */}
                  <DetailRow label='Accounts' value={describePanelScope(previewPanel, accountOptions)} />
                </Stack>
                {queryText(previewPanel) && (
                  <Box
                    sx={{
                      mt: 1.5,
                      p: 1,
                      maxHeight: 140,
                      overflow: 'auto',
                      background: ds.background[100],
                      border: `1px solid ${ds.gray[200]}`,
                      borderRadius: '6px',
                      fontFamily: 'monospace',
                      fontSize: 11.5,
                      color: ds.gray[600],
                      whiteSpace: 'pre-wrap',
                      wordBreak: 'break-word',
                    }}
                    data-testid='panel-library-query'
                  >
                    {queryText(previewPanel)}
                  </Box>
                )}
              </Box>
            </Stack>
          ) : (
            <Box
              sx={{
                height: '100%',
                minHeight: 240,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                p: 2,
                textAlign: 'center',
                border: `1px dashed ${ds.gray[300]}`,
                borderRadius: '8px',
                background: ds.background[100],
              }}
              data-testid='panel-library-preview-empty'
            >
              <Typography variant='body2' sx={{ color: ds.gray[500] }}>
                Pick a widget to see it drawn with your data before you add it.
              </Typography>
            </Box>
          )}
        </Box>
      </Box>
    </Modal>
  );
};

/** One label/value line in the rail's details block. */
const DetailRow: React.FC<{ label: string; value: string }> = ({ label, value }) => (
  <Stack direction='row' alignItems='baseline' gap={1}>
    <Typography variant='caption' sx={{ color: ds.gray[500], width: 92, flexShrink: 0 }}>
      {label}
    </Typography>
    <Typography variant='caption' sx={{ color: ds.gray[700], minWidth: 0 }}>
      {value}
    </Typography>
  </Stack>
);

export default PanelLibraryModal;
