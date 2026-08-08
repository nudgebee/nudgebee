import React, { useEffect, useMemo, useState } from 'react';
import { Box, Stack, Typography } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import { Card } from '@ui/Card';
import { Chip } from '@ui/Chip';
import { Input } from '@ui/Input';
import { Select } from '@ui/Select';
import { ToggleGroup } from '@ui/ToggleGroup';
import { snackbar } from '@ui/Toast';
import { Form } from '@shared/forms/Form';
import { ds } from '@utils/colors';
import apiDashboards, { type AccountOption, type Dashboard } from '@api1/dashboards';
import { panelScope } from './panelAccounts';
import { convertNativeDashboard } from './nativeImport';
import { roleLabel, TEMPLATE_ROLES, type TemplateRole } from './panelTemplates';
import { buildTemplateDocument, DASHBOARD_TEMPLATES, templateVariableDefaults, templateWidgets, type DashboardTemplate } from './dashboardTemplates';

interface Props {
  open: boolean;
  /** Every account the author may scope the created panels to. */
  accountOptions: AccountOption[];
  onClose: () => void;
  onCreated: (dashboard: Dashboard) => void;
}

/** Role filter value meaning "every template". */
const ALL_ROLES = 'all';

/**
 * Creates a dashboard from one of the shipped templates.
 *
 * The template is rendered into this app's own export format and run through
 * the SAME converter a pasted export takes, so a template panel is validated
 * and scoped by exactly the code that already handles imports — there is no
 * second path into the panel model to keep correct.
 *
 * What comes out is an ordinary dashboard the tenant owns: editable, deletable,
 * with no link back to the template it started from.
 */
const TemplateGalleryModal: React.FC<Props> = ({ open, accountOptions, onClose, onCreated }) => {
  const [role, setRole] = useState<TemplateRole | typeof ALL_ROLES>(ALL_ROLES);
  const [selectedId, setSelectedId] = useState('');
  const [title, setTitle] = useState('');
  const [accountType, setAccountType] = useState('');
  const [accountIds, setAccountIds] = useState<string[]>([]);
  const [variables, setVariables] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);

  const selected: DashboardTemplate | undefined = useMemo(() => DASHBOARD_TEMPLATES.find((t) => t.id === selectedId), [selectedId]);

  const visible = useMemo(() => (role === ALL_ROLES ? DASHBOARD_TEMPLATES : DASHBOARD_TEMPLATES.filter((t) => t.roles.includes(role))), [role]);

  const accountTypes = useMemo(() => {
    const seen = new Set<string>();
    for (const o of accountOptions) {
      if (o.cloud_provider) seen.add(o.cloud_provider);
    }
    return [...seen].sort().map((t) => ({ label: t, value: t }));
  }, [accountOptions]);

  const accountsForType = useMemo(
    () => accountOptions.filter((o) => o.cloud_provider === accountType).map((o) => ({ label: o.label, value: o.value })),
    [accountOptions, accountType]
  );

  const roleOptions = useMemo(() => [{ value: ALL_ROLES, label: 'All' }, ...TEMPLATE_ROLES.map((r) => ({ value: r.value, label: r.label }))], []);

  // Picking a template resets what belongs to the template — its name and its
  // variables — but deliberately not the account choice, which is the same
  // answer whichever template someone lands on.
  const choose = (template: DashboardTemplate) => {
    setSelectedId(template.id);
    setTitle(template.title);
    setVariables(templateVariableDefaults(template));
  };

  const reset = () => {
    setRole(ALL_ROLES);
    setSelectedId('');
    setTitle('');
    setAccountType('');
    setAccountIds([]);
    setVariables({});
  };

  // The filter can hide the selected template, and a Create button acting on a
  // card the author can no longer see is worse than making them pick again.
  useEffect(() => {
    if (selectedId && !visible.some((t) => t.id === selectedId)) setSelectedId('');
  }, [visible, selectedId]);

  const close = () => {
    reset();
    onClose();
  };

  const widgets = selected ? templateWidgets(selected) : [];

  /**
   * What kind of account this template wants, said plainly.
   *
   * Its PromQL is written against the metrics the Kubernetes agent ships, and
   * the picker will happily offer an AWS or Azure account — whose metrics come
   * from CloudWatch or Datadog under entirely different names. Scoping a
   * template there produces a dashboard of empty charts, which reads as "no
   * data" rather than "wrong account". Counting the panels is provider-agnostic
   * and cannot go stale the way matching on a `cloud_provider` string would.
   */
  const promqlPanelCount = widgets.filter((w) => w.panel.datasource === 'metrics').length;
  const scopeAdvice =
    promqlPanelCount === 0
      ? 'Every panel reads Nudgebee findings, so any account type works.'
      : `${promqlPanelCount} of ${widgets.length} panels are PromQL queries against the Kubernetes agent's metrics — point this at a cluster account, or edit those panels afterwards. The rest read Nudgebee findings and work anywhere.`;

  const hasScope = Boolean(accountType || accountIds.length > 0);
  const canCreate = Boolean(selected && hasScope && title.trim() && !saving);

  /** Why Create is disabled, shown beside the button. */
  const blockedReason = (() => {
    if (saving) return '';
    if (!selected) return 'Pick a template to start from.';
    if (!title.trim()) return 'Give the dashboard a name.';
    if (!hasScope) return 'Choose an account type — every panel must name one.';
    return '';
  })();

  const handleCreate = async () => {
    if (!selected) return;
    setSaving(true);
    try {
      const document = buildTemplateDocument(selected, variables, title);
      const converted = convertNativeDashboard(document, panelScope(accountType, accountIds));
      if (converted.definition.panels.length === 0) {
        snackbar.error('No panel in this template could be created.');
        return;
      }

      const res = await apiDashboards.saveDashboard({
        title: converted.title,
        description: converted.description,
        definition: converted.definition,
      });
      // The gateway reports handler failures in `errors` rather than throwing,
      // so its message is the one worth showing — it names the panel that failed
      // validation.
      if (res.errors || !res.data) {
        const message = Array.isArray(res.errors) ? (res.errors[0] as any)?.message : null;
        snackbar.error(message || 'Could not create the dashboard.');
        return;
      }
      snackbar.success(`Created ${converted.definition.panels.length} panels from ${selected.title}`);
      reset();
      onCreated(res.data);
    } finally {
      // Also on the early returns above, which is what a rejected create takes.
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      handleClose={saving ? undefined : close}
      title='Start from a template'
      subtitle='Each template creates a normal dashboard you can edit, extend or delete. Nothing links back.'
      width='lg'
      backdropClickClose={false}
      actionButtons={
        // Full width so the reason can sit left of the buttons — DialogActions
        // right-aligns its single child.
        <Stack direction='row' gap='12px' alignItems='center' sx={{ width: '100%', button: { minWidth: '140px' } }}>
          <Typography variant='caption' sx={{ color: ds.amber[600], mr: 'auto' }} data-testid='template-blocked-reason'>
            {blockedReason}
          </Typography>
          <Button tone='secondary' onClick={close} disabled={saving} id='template-cancel-btn'>
            Cancel
          </Button>
          <Button onClick={handleCreate} disabled={!canCreate} loading={saving} id='create-from-template-btn' data-testid='create-from-template-btn'>
            {saving ? 'Creating…' : 'Create dashboard'}
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
        </Form.Section>

        {selected && (
          <>
            <Form.Section title='Name' divider>
              <Form.Field label='Dashboard title' required>
                <Input value={title} onChange={setTitle} disabled={saving} placeholder={selected.title} />
              </Form.Field>
            </Form.Section>

            {/* Accounts are per panel, so this answers the question once for
                every panel the template creates. Each one can be re-pointed
                afterwards from the panel editor. */}
            <Form.Section
              title='Accounts'
              description={`Every panel in the template is scoped to what you choose here; leave Accounts empty to chart every account of the type. ${scopeAdvice}`}
              divider
            >
              <Form.Row ratio={[1, 1]}>
                <Form.Field label='Account type' required>
                  <Select
                    value={accountType}
                    options={accountTypes}
                    onChange={(next: string) => {
                      setAccountType(next);
                      // Selections from the previous provider would be invisible
                      // in the filtered list yet still scope every panel.
                      setAccountIds((prev) => prev.filter((id) => accountOptions.some((o) => o.value === id && o.cloud_provider === next)));
                    }}
                    placeholder='Select…'
                    id='template-account-type'
                  />
                </Form.Field>
                <Form.Field label='Accounts'>
                  <Select
                    multiple
                    value={accountIds}
                    options={accountsForType}
                    onChange={setAccountIds}
                    disabled={!accountType}
                    placeholder={accountType ? `All ${accountType} accounts` : 'Select…'}
                    id='template-accounts'
                  />
                </Form.Field>
              </Form.Row>
            </Form.Section>

            {(selected.variables || []).length > 0 && (
              <Form.Section
                title='Narrow it down'
                description='Substituted into the panel queries when the dashboard is created. Every value is a regular expression, and the defaults match everything.'
                divider
              >
                {(selected.variables || []).map((variable) => (
                  <Form.Field key={variable.name} label={variable.label} description={variable.help}>
                    <Input
                      value={variables[variable.name] ?? variable.defaultValue}
                      onChange={(next: string) => setVariables((prev) => ({ ...prev, [variable.name]: next }))}
                      disabled={saving}
                      placeholder={variable.defaultValue}
                      id={`template-variable-${variable.name}`}
                    />
                  </Form.Field>
                ))}
              </Form.Section>
            )}

            <Form.Section title={`Panels (${widgets.length})`} divider>
              <Stack gap={0.75}>
                {widgets.map((widget, index) => (
                  <Stack key={`${widget.id}-${index}`} direction='row' alignItems='center' gap={1} sx={{ minWidth: 0 }}>
                    <Typography sx={{ fontSize: 13, color: ds.gray[700], minWidth: 0 }} noWrap>
                      {widget.panel.title}
                    </Typography>
                    <Chip size='2xs' tone='subtle'>
                      {widget.panel.datasource}
                    </Chip>
                    <Typography variant='caption' sx={{ color: ds.gray[500], minWidth: 0 }} noWrap>
                      {widget.summary}
                    </Typography>
                  </Stack>
                ))}
              </Stack>
            </Form.Section>
          </>
        )}
      </Form>
    </Modal>
  );
};

export default TemplateGalleryModal;
