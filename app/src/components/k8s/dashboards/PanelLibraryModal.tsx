import React, { useMemo, useState } from 'react';
import { Box, Stack, Typography } from '@mui/material';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';
import LockOutlinedIcon from '@mui/icons-material/LockOutlined';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import { Card } from '@ui/Card';
import { Chip } from '@ui/Chip';
import { EmptyState } from '@ui/EmptyState';
import SearchInput from '@ui/SearchInput';
import Tooltip from '@ui/Tooltip';
import { ToggleGroup } from '@ui/ToggleGroup';
import { ds } from '@utils/colors';
import type { AccountOption, Panel } from '@api1/dashboards';
import PanelPreview, { PREVIEW_RAIL_WIDTH, usePreviewRange } from './PanelPreview';
import { describePanelScope } from './panelAccounts';
import { grantTooltip, missingPanelGrant } from './panelAccess';
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
  /** Hands the copy to the panel editor, where the account is configured before it's saved. */
  onConfigure: (panel: Panel) => void;
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
const PanelLibraryModal: React.FC<Props> = ({ open, existingPanels, accountOptions, variables, startTime, endTime, onClose, onConfigure }) => {
  const [role, setRole] = useState<TemplateRole | typeof ALL_ROLES>(ALL_ROLES);
  const [search, setSearch] = useState('');
  const [selected, setSelected] = useState<PanelTemplate | null>(null);
  const fallbackRange = usePreviewRange(open);

  const roleOptions = useMemo(
    () => [
      { value: ALL_ROLES, label: 'All' },
      ...TEMPLATE_ROLES.filter((r) => r.value !== 'developer').map((r) => ({ value: r.value, label: r.label })),
    ],
    []
  );

  /**
   * Widget id → the `<module>:Read` grant its viewer is missing.
   *
   * Empty for everyone but a grants-only custom-role holder, and only ever
   * covers `nudgebee` widgets — every other datasource is gated by account
   * access, which the picker already reflects through the panel's scope. Derived
   * once: the catalogue is a module-level constant and the session does not
   * change while the modal is open.
   */
  const blockedWidgets = useMemo(() => {
    const out: Record<string, string> = {};
    for (const widget of PANEL_TEMPLATES) {
      const missing = missingPanelGrant(widget.panel);
      if (missing) out[widget.id] = missing;
    }
    return out;
  }, []);

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
    onClose();
  };

  const configure = () => {
    if (!previewPanel) return;
    onConfigure(previewPanel);
    close();
  };

  return (
    <Modal
      open={open}
      handleClose={close}
      title='Add a panel from the library'
      subtitle='Pick one to preview it with sample data. Continuing opens it in the panel editor, where you set the account before it’s added.'
      width='lg'
      maxHeight='85vh'
      contentStyles={{ padding: 0, overflow: 'hidden', display: 'flex' }}
      actionButtons={
        <Stack direction='row' gap='12px' sx={{ button: { minWidth: '140px' } }}>
          <Button tone='secondary' onClick={close} id='panel-library-close-btn'>
            Close
          </Button>
          <Button
            onClick={configure}
            disabled={!selected}
            icon={<ArrowForwardIcon sx={{ fontSize: 16 }} />}
            iconPlacement='end'
            id='panel-library-add-btn'
            data-testid='panel-library-add-btn'
          >
            Configure
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
            padding: 'var(--ds-space-5) var(--ds-space-2)',
            pb: 0,
          }}
        >
          <Stack direction='row' alignItems='center' gap={1.5} sx={{ px: ds.space[5], mb: 1.5, flexShrink: 0 }}>
            <SearchInput value={search} onChange={setSearch} label='Search widgets' id='panel-library-search' sx={{ flexShrink: 0 }} />
            <ToggleGroup
              selection='single'
              size='sm'
              ariaLabel='Filter widgets by role'
              value={role}
              options={roleOptions}
              onChange={(next) => setRole(next as TemplateRole | typeof ALL_ROLES)}
              id='panel-library-role-toggle'
            />
          </Stack>

          {byCategory.length === 0 ? (
            <EmptyState size='section' illustration='no-results' title='No widget matches' description='Try another role, or clear the search.' />
          ) : (
            // Only the list scrolls, so the filters stay put.
            <Box sx={{ flex: 1, minHeight: 0, px: ds.space[5], overflowY: { xs: 'visible', md: 'auto' } }} data-testid='panel-library-list'>
              {byCategory.map(({ category, widgets }) => (
                <Box key={category} sx={{ mb: 2 }}>
                  <Typography
                    sx={{
                      fontFamily: 'var(--ds-font-display)',
                      fontSize: 13,
                      fontWeight: ds.weight.semibold,
                      color: ds.gray[600],
                      mb: 0.75,
                    }}
                  >
                    {category}
                  </Typography>
                  <Stack gap={1.5}>
                    {widgets.map((widget) => {
                      const blocked = blockedWidgets[widget.id];
                      const card = (
                        <Card
                          variant='outlined'
                          elevation='flat'
                          size='sm'
                          interactive={!blocked}
                          onClick={blocked ? undefined : () => setSelected(widget)}
                          data-testid={`widget-${widget.id}`}
                          sx={{
                            ...(selected?.id === widget.id
                              ? { borderColor: ds.blue[300], background: ds.blue[100], boxShadow: '0 4px 8px rgba(0, 0, 0, 0.08)' }
                              : {}),
                            ...(blocked ? { opacity: 0.55, cursor: 'not-allowed', background: ds.background[200] } : {}),
                            // Card's `interactive` hover always adds a lift shadow regardless of `elevation` — flat here means flat.
                            '&:hover': { boxShadow: 'none', transform: 'none' },
                          }}
                        >
                          <Stack direction='row' alignItems='center' gap={1.5}>
                            <Box sx={{ minWidth: 0, flex: 1 }}>
                              <Typography
                                sx={{ fontFamily: 'var(--ds-font-display)', fontSize: 'var(--ds-text-body)', fontWeight: 600, color: ds.gray[700] }}
                                noWrap
                              >
                                {widget.panel.title}
                              </Typography>
                              <Typography variant='caption' sx={{ color: ds.gray[600], display: 'block' }}>
                                {widget.summary}
                              </Typography>
                            </Box>
                            <Stack direction='row' gap={0.5} sx={{ flexShrink: 0 }}>
                              {blocked ? (
                                // The permission is on the row, not only in the
                                // tooltip: which grant to ask for is the whole
                                // answer, and a hover-only answer is not one.
                                <Chip size='2xs' tone='warning' icon={<LockOutlinedIcon />} data-testid={`widget-blocked-${widget.id}`}>
                                  {blocked}
                                </Chip>
                              ) : (
                                <>
                                  <Chip size='2xs' tone='subtle'>
                                    {widget.panel.type}
                                  </Chip>
                                  <Chip size='2xs' tone='subtle'>
                                    {widget.panel.datasource}
                                  </Chip>
                                  {widget.roles
                                    .filter((r) => r !== 'developer')
                                    .slice(0, 2)
                                    .map((r) => (
                                      <Chip key={r} size='2xs' tone='info'>
                                        {roleLabel(r)}
                                      </Chip>
                                    ))}
                                </>
                              )}
                            </Stack>
                          </Stack>
                        </Card>
                      );
                      return (
                        <React.Fragment key={widget.id}>
                          {blocked ? (
                            <Tooltip title={grantTooltip(blocked)}>
                              {/* The card is inert, so the tooltip hangs off a
                                  wrapper that still receives the hover. */}
                              <Box>{card}</Box>
                            </Tooltip>
                          ) : (
                            card
                          )}
                        </React.Fragment>
                      );
                    })}
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
                forceSample
              />
              <Card
                size='sm'
                elevation='flat'
                footer={
                  queryText(previewPanel) ? (
                    <Box
                      sx={{
                        maxHeight: 140,
                        overflow: 'auto',
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
                  ) : undefined
                }
              >
                {previewPanel.description && (
                  <Typography variant='body2' sx={{ color: ds.gray[600], mb: 1.5, fontSize: ds.text.body, fontWeight: ds.weight.semibold }}>
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
              </Card>
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
                Pick a widget to see it drawn with sample data before you configure it.
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
