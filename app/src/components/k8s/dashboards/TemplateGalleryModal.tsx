import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Box, Stack, Typography } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import { Card } from '@ui/Card';
import { Chip } from '@ui/Chip';
import { ToggleGroup } from '@ui/ToggleGroup';
import SearchInput from '@ui/SearchInput';
import { snackbar } from '@ui/Toast';
import { Form } from '@shared/forms/Form';
import { ds } from '@utils/colors';
import type { AccountOption, Dashboard } from '@api1/dashboards';
import { deriveAccountTypes } from './panelAccounts';
import { defaultWidgetScope, roleLabel, TEMPLATE_ROLES, type TemplateRole } from './panelTemplates';
import { convertTemplate, DASHBOARD_TEMPLATES, filterTemplatesBySearch, templateWidgets, type DashboardTemplate } from './dashboardTemplates';

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
  const [query, setQuery] = useState('');
  const [selectedId, setSelectedId] = useState('');

  const selected: DashboardTemplate | undefined = useMemo(() => DASHBOARD_TEMPLATES.find((t) => t.id === selectedId), [selectedId]);
  const visible = useMemo(() => {
    const byRole = role === ALL_ROLES ? DASHBOARD_TEMPLATES : DASHBOARD_TEMPLATES.filter((t) => t.roles.includes(role));
    return filterTemplatesBySearch(byRole, query);
  }, [role, query]);
  const roleOptions = useMemo(() => [{ value: ALL_ROLES, label: 'All' }, ...TEMPLATE_ROLES.map((r) => ({ value: r.value, label: r.label }))], []);

  const widgets = selected ? templateWidgets(selected) : [];
  const scopes = widgets.map((widget) => defaultWidgetScope(widget, accountOptions));

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
    setQuery('');
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
    // Through the same converter a pasted export takes, so a template panel is
    // validated by exactly the code that already handles imports rather than by
    // a second path kept correct by hand.
    const converted = convertTemplate(selected, '', scopes);
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
            {selected ? '' : 'Pick a template to start from.'}
          </Typography>
          <Button tone='secondary' onClick={close} id='template-cancel-btn'>
            Cancel
          </Button>
          <Button onClick={handleContinue} disabled={!selected} id='create-from-template-btn' data-testid='create-from-template-btn'>
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
            {/* Filters the static gallery live by name and description — no API,
                so it narrows as you type rather than on Enter, matching the role
                toggle beside it. */}
            <SearchInput value={query} onChange={setQuery} label='Search templates' id='template-search' ml='auto' />
          </Stack>

          {visible.length === 0 ? (
            <Typography variant='caption' sx={{ color: ds.gray[500], display: 'block', mt: 1.5 }} data-testid='template-empty'>
              No templates match your search.
            </Typography>
          ) : (
            <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: 'repeat(2, 1fr)' }, gap: 1.5, mt: 1 }} data-testid='template-gallery'>
              {visible.map((template) => (
                <Card
                  key={template.id}
                  variant='outlined'
                  elevation='flat'
                  size='sm'
                  interactive
                  selected={template.id === selectedId}
                  onClick={() => choose(template)}
                  data-testid={`template-card-${template.id}`}
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
                    {template.roles.map((r) => (
                      <Chip key={r} size='2xs' tone='info'>
                        {roleLabel(r)}
                      </Chip>
                    ))}
                  </Stack>
                </Card>
              ))}
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

                  return (
                    <Stack key={`${widget.id}-${index}`} direction='row' alignItems='center' gap={1} sx={{ minWidth: 0 }}>
                      <Typography sx={{ fontSize: 13, fontWeight: 600, color: ds.gray[700], minWidth: 0, flex: '0 1 auto' }} noWrap>
                        {widget.panel.title}
                      </Typography>
                      <Chip size='2xs' tone='subtle'>
                        {widget.panel.datasource}
                      </Chip>
                      {/* An unscoped panel is not a blocker here — the editor
                          shows the same red chip and the server rejects the save
                          — but naming it now beats discovering it there. */}
                      <Chip size='2xs' tone={unscoped ? 'critical' : 'info'}>
                        {unscoped ? 'No account — pick one in the editor' : `All ${providers.join(' + ')}`}
                      </Chip>
                      <Typography variant='caption' sx={{ color: ds.gray[500], minWidth: 0 }} noWrap title={widget.summary}>
                        {widget.summary}
                      </Typography>
                    </Stack>
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
