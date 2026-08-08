import React, { useState } from 'react';
import { Box, Stack, Typography } from '@mui/material';
import SafeIcon from '@shared/icons/SafeIcon';
import { DeleteIconRed as deleteIcon, writeIconLight } from '@assets';
import { Button } from '@ui/Button';
import Tooltip from '@ui/Tooltip';
import { Card } from '@ui/Card';
import { Chip } from '@ui/Chip';
import { EmptyState } from '@ui/EmptyState';
import { Input } from '@ui/Input';
import { ListingLayout } from '@ui/ListingLayout';
import { snackbar } from '@ui/Toast';
import { Form } from '@shared/forms/Form';
import { ds } from '@utils/colors';
import apiDashboards, { EMPTY_DEFINITION, type AccountOption, type Dashboard, type Panel } from '@api1/dashboards';
import PanelEditorModal from './PanelEditorModal';
import PanelLibraryModal from './PanelLibraryModal';
import { panelScopeLabels } from './panelAccounts';
import { blankPanel } from './panelDefaults';

interface Props {
  /** Every account the author may point panels at, across providers. */
  accountOptions: AccountOption[];
  /** Null when creating a new dashboard. */
  dashboard: Dashboard | null;
  onCancel: () => void;
  onSaved: (saved: Dashboard) => void;
}

/** Beyond this many account chips a panel row stops being scannable. */
const MAX_ACCOUNT_CHIPS = 3;

const DashboardEditor: React.FC<Props> = ({ accountOptions, dashboard, onCancel, onSaved }) => {
  const [title, setTitle] = useState(dashboard?.title || '');
  const [description, setDescription] = useState(dashboard?.description || '');
  const [panels, setPanels] = useState<Panel[]>(dashboard?.definition?.panels || []);
  const [editingPanel, setEditingPanel] = useState<Panel | null>(null);
  const [panelModalOpen, setPanelModalOpen] = useState(false);
  const [libraryOpen, setLibraryOpen] = useState(false);
  const [saving, setSaving] = useState(false);

  // Guarded rather than only disabling the buttons: the empty state's action has
  // no disabled state of its own, and every path into the editor goes here.
  const openNewPanel = () => {
    if (saving) return;
    setEditingPanel(blankPanel(panels));
    setPanelModalOpen(true);
  };

  /**
   * A library widget lands in the panel editor rather than in the list: it
   * carries a query but no account, and the account is the one field only the
   * author can fill in.
   */
  const openLibraryPanel = (panel: Panel) => {
    setLibraryOpen(false);
    setEditingPanel(panel);
    setPanelModalOpen(true);
  };

  const savePanel = (panel: Panel) => {
    setPanels((prev) => {
      const idx = prev.findIndex((p) => p.id === panel.id);
      if (idx === -1) return [...prev, panel];
      const next = [...prev];
      next[idx] = panel;
      return next;
    });
    setPanelModalOpen(false);
    setEditingPanel(null);
  };

  const handleSave = async () => {
    if (!title.trim()) {
      snackbar.error('Give the dashboard a title before saving.');
      return;
    }
    setSaving(true);
    try {
      // `bindings` is deliberately omitted rather than sent empty: the service
      // replaces bindings wholesale when the field is present, so sending [] from
      // an editor that no longer edits them would silently delete any that exist.
      const res = await apiDashboards.saveDashboard({
        id: dashboard?.id,
        title: title.trim(),
        description,
        definition: { ...EMPTY_DEFINITION, panels },
      });

      // The gateway reports handler failures in `errors`, not by throwing, so a
      // success toast keyed only on the absence of an exception would lie.
      if (res.errors || !res.data) {
        const message = Array.isArray(res.errors) ? (res.errors[0] as any)?.message : null;
        snackbar.error(message || 'Could not save the dashboard.');
        return;
      }
      snackbar.success(dashboard?.id ? 'Dashboard updated' : 'Dashboard created');
      onSaved(res.data);
    } finally {
      // Guarantees the editor unfreezes on every exit — including the early
      // return above, which is what a rejected save takes.
      setSaving(false);
    }
  };

  const renderPanelRow = (p: Panel) => {
    // Type-scoped panels show the provider; id-scoped ones show the account
    // names, so a row says what it charts without opening it.
    const scope = panelScopeLabels(p, accountOptions);
    const shown = scope.slice(0, MAX_ACCOUNT_CHIPS);
    const overflow = scope.length - shown.length;

    return (
      <Card key={p.id} variant='outlined' elevation='flat' size='sm'>
        <Stack direction='row' alignItems='center' gap={2}>
          <Box sx={{ minWidth: 0, flex: 1 }}>
            <Typography sx={{ fontFamily: 'var(--ds-font-display)', fontSize: 14, fontWeight: 600, color: ds.gray[700] }} noWrap>
              {p.title || 'Untitled panel'}
            </Typography>
            <Stack direction='row' gap={0.5} alignItems='center' flexWrap='wrap' sx={{ mt: 0.5 }}>
              <Chip size='2xs' tone='subtle'>
                {p.type}
              </Chip>
              {p.type !== 'text' && (
                <Chip size='2xs' tone='subtle'>
                  {p.datasource}
                </Chip>
              )}
              {p.type !== 'text' &&
                (scope.length === 0 ? (
                  <Chip size='2xs' tone='critical'>
                    No account
                  </Chip>
                ) : (
                  <>
                    {shown.map((label) => (
                      <Chip key={label} size='2xs' tone='info'>
                        {label}
                      </Chip>
                    ))}
                    {overflow > 0 && (
                      <Chip size='2xs' tone='neutral'>
                        +{overflow}
                      </Chip>
                    )}
                  </>
                ))}
              <Chip size='2xs' tone='neutral'>
                {p.grid_pos?.w || 12}/12
              </Chip>
            </Stack>
          </Box>
          <Stack direction='row' gap={0.5} sx={{ flexShrink: 0 }}>
            {/* Matches the listing and every other icon-action row in the app. */}
            <Tooltip title='Edit panel'>
              <Button
                tone='ghost'
                composition='icon-only'
                aria-label='Edit panel'
                icon={<SafeIcon src={writeIconLight} alt='edit' width={18} height={18} />}
                onClick={() => {
                  setEditingPanel(p);
                  setPanelModalOpen(true);
                }}
                disabled={saving}
                id={`edit-panel-${p.id}`}
              />
            </Tooltip>
            <Tooltip title='Remove panel'>
              <Button
                tone='ghost'
                composition='icon-only'
                aria-label='Remove panel'
                icon={<SafeIcon src={deleteIcon} alt='delete' width={18} height={18} />}
                onClick={() => setPanels((prev) => prev.filter((x) => x.id !== p.id))}
                disabled={saving}
                id={`remove-panel-${p.id}`}
              />
            </Tooltip>
          </Stack>
        </Stack>
      </Card>
    );
  };

  return (
    <ListingLayout id='dashboard-editor'>
      <ListingLayout.Toolbar
        // ToolbarTitle hardcodes body-size text for section headings. This is a
        // page-level heading, so the inner span sets its own size — nesting
        // wins over the wrapper's fontSize without forking the primitive.
        title={
          <Box component='span' sx={{ fontFamily: 'var(--ds-font-display)', fontSize: 18, fontWeight: 650 }}>
            {dashboard?.id ? 'Edit dashboard' : 'New dashboard'}
          </Box>
        }
        actions={
          <Stack direction='row' gap={1}>
            {/* Cancelling mid-save would unmount the editor while the request is
                still in flight, so the result lands on nothing. */}
            <Button tone='secondary' onClick={onCancel} disabled={saving} id='dashboard-cancel-btn'>
              Cancel
            </Button>
            <Button onClick={handleSave} disabled={saving} id='dashboard-save-btn' data-testid='dashboard-save-btn'>
              {saving ? 'Saving…' : 'Save dashboard'}
            </Button>
          </Stack>
        }
      />
      {/* Body defaults to `0 space[5]` — no vertical padding — so the last panel
          card sits flush against the card border. Restore the bottom gap. */}
      <ListingLayout.Body padding={`0 ${ds.space[5]} ${ds.space[5]}`}>
        <Box data-testid='dashboard-editor' sx={{ maxWidth: 960 }}>
          {/* Everything below is frozen while the save is in flight: the request
              already carries a snapshot of this form, so an edit made now would
              be silently dropped by the response that replaces it. */}
          <Form variant='stacked' density='default'>
            <Form.Section>
              <Form.Row ratio={[1, 1]}>
                <Form.Field label='Title' required>
                  <Input value={title} onChange={setTitle} disabled={saving} placeholder='Checkout latency & error budget' />
                </Form.Field>
                <Form.Field label='Description'>
                  <Input value={description} onChange={setDescription} disabled={saving} placeholder='Optional' />
                </Form.Field>
              </Form.Row>
            </Form.Section>
          </Form>

          <Box sx={{ mt: 4 }}>
            <Stack direction='row' alignItems='center' gap={1} sx={{ mb: 1.5 }}>
              <Typography sx={{ fontFamily: 'var(--ds-font-display)', fontSize: 15, fontWeight: 620, color: ds.gray[700] }}>Panels</Typography>
              <Chip size='2xs' tone='neutral'>
                {panels.length}
              </Chip>
              <Box sx={{ flex: 1 }} />
              <Button
                tone='secondary'
                onClick={() => setLibraryOpen(true)}
                disabled={saving}
                id='add-panel-from-library-btn'
                data-testid='add-panel-from-library-btn'
              >
                Add from library
              </Button>
              <Button tone='secondary' onClick={openNewPanel} disabled={saving} id='add-panel-btn' data-testid='add-panel-btn'>
                Add panel
              </Button>
            </Stack>

            {panels.length === 0 ? (
              <EmptyState
                surface
                size='section'
                illustration='first-time'
                title='No panels yet'
                description='Each panel runs one query against the accounts you scope it to.'
                action={{ label: 'Add panel', onClick: openNewPanel }}
              />
            ) : (
              <Stack gap={1}>{panels.map(renderPanelRow)}</Stack>
            )}
          </Box>
        </Box>

        <PanelLibraryModal open={libraryOpen} existingPanels={panels} onClose={() => setLibraryOpen(false)} onPick={openLibraryPanel} />

        <PanelEditorModal
          open={panelModalOpen}
          panel={editingPanel}
          accountOptions={accountOptions}
          onClose={() => {
            setPanelModalOpen(false);
            setEditingPanel(null);
          }}
          onSave={savePanel}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export default DashboardEditor;
