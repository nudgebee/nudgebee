import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Box, Stack, Typography } from '@mui/material';
import LockOutlinedIcon from '@mui/icons-material/LockOutlined';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import { Card } from '@ui/Card';
import { Chip } from '@ui/Chip';
import Tooltip from '@ui/Tooltip';
import { ToggleGroup } from '@ui/ToggleGroup';
import { snackbar } from '@ui/Toast';
import { Form } from '@shared/forms/Form';
import { ds } from '@utils/colors';
import type { AccountOption, Dashboard } from '@api1/dashboards';
import { deriveAccountTypes } from './panelAccounts';
import { defaultWidgetScope, findPanelTemplate, roleLabel, TEMPLATE_ROLES, type TemplateRole } from './panelTemplates';
import { convertTemplate, DASHBOARD_TEMPLATES, templateWidgets, type DashboardTemplate } from './dashboardTemplates';
import { grantTooltip, missingPanelGrant } from './panelAccess';

interface Props {
  open: boolean;
  /** Every account the panels may be scoped to; the pre-fill resolves against it. */
  accountOptions: AccountOption[];
  onClose: () => void;
  /**
   * Handed an UNSAVED dashboard built from the template. The caller opens the
   * dashboard editor on it — nothing is written until the author saves there.
   */
  onDraft: (draft: Dashboard) => void;
}

/** Role filter value meaning "every template". */
const ALL_ROLES = 'all';

/**
 * The template minus the panels this viewer cannot read.
 *
 * Dropping them is what stops "start from a template" handing someone a
 * dashboard half full of Access Denied. Unknown widget ids go too, so the
 * remaining list indexes 1:1 with the scopes built from it — `buildTemplateDocument`
 * counts RESOLVED panels, and a gap would shift every later scope onto the wrong
 * panel.
 */
function readableTemplate(template: DashboardTemplate): DashboardTemplate {
  return {
    ...template,
    panels: template.panels.filter((entry) => {
      const widget = findPanelTemplate(entry.widget);
      if (!widget) return false;
      return !missingPanelGrant(widget.panel);
    }),
  };
}

/**
 * Picks a template and turns it into a dashboard draft.
 *
 * This modal deliberately does NOT save, and asks nothing it does not have to.
 * It answers one question — which template — and then shows what that choice
 * produces. The title, each panel's accounts, adding or removing panels: all of
 * that is the dashboard editor's job, and the lesser copy of it that used to
 * live here meant an author who wanted to change one panel's account had to
 * create the dashboard first and then go fix it.
 *
 * Panels arrive pre-scoped to an account TYPE resolved from what the widget is
 * about — PromQL and events onto the cluster, savings onto a cloud account — so
 * the editor opens on something already coherent rather than a column of red
 * "No account" chips.
 */
const TemplateGalleryModal: React.FC<Props> = ({ open, accountOptions, onClose, onDraft }) => {
  const [role, setRole] = useState<TemplateRole | typeof ALL_ROLES>(ALL_ROLES);
  const [selectedId, setSelectedId] = useState('');

  const selected: DashboardTemplate | undefined = useMemo(() => DASHBOARD_TEMPLATES.find((t) => t.id === selectedId), [selectedId]);
  const visible = useMemo(() => (role === ALL_ROLES ? DASHBOARD_TEMPLATES : DASHBOARD_TEMPLATES.filter((t) => t.roles.includes(role))), [role]);
  const roleOptions = useMemo(() => [{ value: ALL_ROLES, label: 'All' }, ...TEMPLATE_ROLES.map((r) => ({ value: r.value, label: r.label }))], []);

  const widgets = selected ? templateWidgets(selected) : [];
  const scopes = widgets.map((widget) => defaultWidgetScope(widget, accountOptions));

  /**
   * Per template: the distinct grants its panels need and how many panels remain
   * after the unreadable ones are dropped.
   *
   * Empty for everyone but a grants-only custom-role holder (see panelAccess.ts),
   * so every other viewer sees the gallery exactly as before. Derived once — the
   * catalogue is a module-level constant and the session does not change while
   * the modal is open.
   */
  const access = useMemo(() => {
    const out: Record<string, { missing: string[]; readable: number; total: number }> = {};
    for (const template of DASHBOARD_TEMPLATES) {
      const all = templateWidgets(template);
      // `missing` is deduped only for the tooltip — the count below must use the
      // per-panel list, or two panels needing the same grant would count as one.
      const missing = all.map((w) => missingPanelGrant(w.panel)).filter((p): p is string => Boolean(p));
      out[template.id] = { missing: [...new Set(missing)], readable: all.length - missing.length, total: all.length };
    }
    return out;
  }, []);

  /** The grant each widget in the SELECTED template needs, by position. */
  const widgetGrants = widgets.map((widget) => missingPanelGrant(widget.panel));
  const selectedAccess = selected ? access[selected.id] : undefined;

  /**
   * The details below the gallery, scrolled to when a card is picked.
   *
   * The cards fill the modal on a normal window, so what the author is about to
   * get sat below the fold with nothing indicating it was there. Picking a card
   * looked like it did nothing.
   */
  const detailsRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    if (!selectedId || !detailsRef.current) return;
    detailsRef.current.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }, [selectedId]);

  const choose = (template: DashboardTemplate) => setSelectedId(template.id);

  const reset = () => {
    setRole(ALL_ROLES);
    setSelectedId('');
  };

  // The filter can hide the selected template, and continuing with a card the
  // author can no longer see is worse than making them pick again.
  useEffect(() => {
    if (selectedId && !visible.some((t) => t.id === selectedId)) setSelectedId('');
  }, [visible, selectedId]);

  const close = () => {
    reset();
    onClose();
  };

  const handleContinue = () => {
    if (!selected) return;
    // Panels the viewer's role cannot read are dropped here rather than carried
    // into the draft: the panel would render Access Denied for the person who
    // just created the dashboard, and they have no way to fix it.
    const readable = readableTemplate(selected);
    const readableScopes = templateWidgets(readable).map((widget) => defaultWidgetScope(widget, accountOptions));
    // Through the same converter a pasted export takes, so a template panel is
    // validated by exactly the code that already handles imports rather than by
    // a second path kept correct by hand.
    const converted = convertTemplate(readable, '', readableScopes);
    if (converted.definition.panels.length === 0) {
      snackbar.error('No panel in this template could be created.');
      return;
    }
    // No id: this is a draft. The editor's Save is what creates it, which is
    // also what makes Cancel mean "never mind" rather than leaving a dashboard
    // behind that has to be deleted.
    onDraft({ id: '', title: converted.title, description: converted.description, definition: converted.definition });
    reset();
  };

  return (
    <Modal
      open={open}
      handleClose={close}
      title='Start from a template'
      subtitle='Pick one to open it in the dashboard editor, where every panel can be changed before anything is saved.'
      width='lg'
      backdropClickClose={false}
      actionButtons={
        // Full width so the hint can sit left of the buttons — DialogActions
        // right-aligns its single child.
        <Stack direction='row' gap='12px' alignItems='center' sx={{ width: '100%', button: { minWidth: '140px' } }}>
          <Typography variant='caption' sx={{ color: ds.amber[600], mr: 'auto' }} data-testid='template-blocked-reason'>
            {!selected
              ? 'Pick a template to start from.'
              : selectedAccess && selectedAccess.missing.length > 0
              ? `${widgets.length - selectedAccess.readable} panel(s) your role cannot read will be left out.`
              : ''}
          </Typography>
          <Button tone='secondary' onClick={close} id='template-cancel-btn'>
            Cancel
          </Button>
          <Button
            onClick={handleContinue}
            disabled={!selected || (selectedAccess !== undefined && selectedAccess.total > 0 && selectedAccess.readable === 0)}
            id='create-from-template-btn'
            data-testid='create-from-template-btn'
          >
            Open in editor
          </Button>
        </Stack>
      }
    >
      <Form variant='stacked' density='default'>
        <Form.Section>
          <Stack direction='row' alignItems='center' gap={1.5} flexWrap='wrap'>
            <Typography
              component='label'
              htmlFor='template-role-toggle'
              sx={{
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: 'var(--ds-gray-700)',
                fontFamily: 'var(--ds-font-display)',
              }}
            >
              Built for
            </Typography>
            <ToggleGroup
              selection='single'
              size='sm'
              ariaLabel='Filter templates by role'
              value={role}
              options={roleOptions}
              onChange={(next) => setRole(next as TemplateRole | typeof ALL_ROLES)}
              id='template-role-toggle'
            />
          </Stack>

          {visible.length === 0 ? (
            <Typography variant='caption' sx={{ color: ds.gray[500], display: 'block', mt: 1.5 }} data-testid='template-empty'>
              No templates for this role.
            </Typography>
          ) : (
            <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: 'repeat(2, 1fr)' }, gap: 1.5, mt: 1 }} data-testid='template-gallery'>
              {visible.map((template) => {
                const { missing, readable, total } = access[template.id];
                // Nothing in it is readable, so there is no dashboard to build —
                // the card is inert and names the grants instead. `total > 0`
                // guards a template whose widget ids all failed to resolve:
                // readable is 0 there too, but no grant is missing, so the card
                // would go inert with nothing explaining why. That case stays
                // clickable and falls through to the existing "No panel in this
                // template could be created" error.
                const unusable = total > 0 && readable === 0;
                const card = (
                  <Card
                    variant='outlined'
                    elevation='flat'
                    size='sm'
                    interactive={!unusable}
                    selected={template.id === selectedId}
                    onClick={unusable ? undefined : () => choose(template)}
                    data-testid={`template-card-${template.id}`}
                    sx={unusable ? { opacity: 0.55, cursor: 'not-allowed', background: ds.background[200] } : undefined}
                  >
                    <Typography sx={{ fontFamily: 'var(--ds-font-display)', fontSize: 14, fontWeight: 620, color: ds.gray[700] }}>
                      {template.title}
                    </Typography>
                    <Typography variant='caption' sx={{ color: ds.gray[500], display: 'block', mt: 0.5 }}>
                      {template.description}
                    </Typography>
                    <Stack direction='row' gap={0.5} flexWrap='wrap' sx={{ mt: 1 }}>
                      <Chip size='2xs' tone='neutral'>
                        {template.panels.length} panels
                      </Chip>
                      {/* What the author loses before they commit to the template,
                          rather than after opening the editor and counting. */}
                      {missing.length > 0 && (
                        <Chip size='2xs' tone='warning' icon={<LockOutlinedIcon />} data-testid={`template-blocked-${template.id}`}>
                          {unusable ? 'No panel you can read' : `${template.panels.length - readable} panel(s) left out`}
                        </Chip>
                      )}
                      {template.roles.map((r) => (
                        <Chip key={r} size='2xs' tone='info'>
                          {roleLabel(r)}
                        </Chip>
                      ))}
                    </Stack>
                  </Card>
                );
                return (
                  <React.Fragment key={template.id}>
                    {missing.length > 0 ? (
                      <Tooltip title={grantTooltip(missing)}>
                        {/* The card can be inert, so the tooltip hangs off a
                            wrapper that still receives the hover. The wrapper takes
                            the card's place as the grid item, so it has to pass the
                            row's stretch through — otherwise a gated card is short
                            next to its neighbours. */}
                        <Box sx={{ display: 'flex', '& > *': { flex: 1, minWidth: 0 } }}>{card}</Box>
                      </Tooltip>
                    ) : (
                      card
                    )}
                  </React.Fragment>
                );
              })}
            </Box>
          )}
        </Form.Section>

        {selected && (
          // The scroll target, so picking a card visibly moves to what it will
          // build rather than leaving the author looking at an unchanged grid.
          <Box ref={detailsRef} sx={{ scrollMarginTop: ds.space[3] }} data-testid='template-details'>
            {/* Read-only: the editor is where these get changed, and a second
                set of controls here would be a lesser copy of it. Showing the
                resolved account per panel is what makes "Open in editor" a
                predictable next step rather than a leap. */}
            <Form.Section
              title={`${selected.title} — panels (${widgets.length})`}
              description='Created with the accounts below, then yours to edit. Change any panel, add or remove one, and rename the dashboard in the editor.'
              divider
            >
              <Stack gap={0.75} data-testid='template-panel-preview'>
                {widgets.map((widget, index) => {
                  // Providers, not account names: the scope IS a set of types,
                  // and a cost panel spanning two clouds reading "4 accounts"
                  // tells the author nothing about which clouds.
                  const providers = deriveAccountTypes(scopes[index], accountOptions);
                  const unscoped = providers.length === 0;
                  const blocked = widgetGrants[index];

                  const row = (
                    <Stack
                      direction='row'
                      alignItems='center'
                      gap={1}
                      sx={{ minWidth: 0, ...(blocked ? { opacity: 0.55 } : {}) }}
                      data-testid={blocked ? `template-panel-blocked-${widget.id}` : undefined}
                    >
                      <Typography
                        sx={{
                          fontSize: 13,
                          fontWeight: 600,
                          color: ds.gray[700],
                          minWidth: 0,
                          flex: '0 1 auto',
                          ...(blocked ? { textDecoration: 'line-through' } : {}),
                        }}
                        noWrap
                      >
                        {widget.panel.title}
                      </Typography>
                      <Chip size='2xs' tone='subtle'>
                        {widget.panel.datasource}
                      </Chip>
                      {/* A blocked panel's account scope is moot — it is not
                          going into the dashboard — so the grant replaces it. */}
                      {blocked ? (
                        <Chip size='2xs' tone='warning' icon={<LockOutlinedIcon />}>
                          {blocked}
                        </Chip>
                      ) : (
                        /* An unscoped panel is not a blocker here — the editor
                           shows the same red chip and the server rejects the save
                           — but naming it now beats discovering it there. */
                        <Chip size='2xs' tone={unscoped ? 'critical' : 'info'}>
                          {unscoped ? 'No account — pick one in the editor' : `All ${providers.join(' + ')}`}
                        </Chip>
                      )}
                      <Typography variant='caption' sx={{ color: ds.gray[500], minWidth: 0 }} noWrap title={widget.summary}>
                        {widget.summary}
                      </Typography>
                    </Stack>
                  );

                  return (
                    <React.Fragment key={`${widget.id}-${index}`}>
                      {blocked ? <Tooltip title={grantTooltip(blocked)}>{row}</Tooltip> : row}
                    </React.Fragment>
                  );
                })}
              </Stack>
            </Form.Section>
          </Box>
        )}
      </Form>
    </Modal>
  );
};

export default TemplateGalleryModal;
