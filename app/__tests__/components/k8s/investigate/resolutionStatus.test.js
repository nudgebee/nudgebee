import {
  describeResolution,
  describeActionKind,
  describeActionEffect,
  describeUndo,
  describeActionTitle,
  describeUndoTarget,
  describeBeforeStatePreview,
  describeRevertUndoPreview,
  cardOffersAction,
  RESOLUTION_SLOW_AFTER_MS,
} from '@components/k8s/investigate/resolutionStatus';

// The event page previously rendered the raw enum ("LAST DEPLOYMENT CHANGE: IN PROGRESS") and threw
// away resolver and error, so an operator could not tell who acted or why it failed.
describe('describeResolution', () => {
  const NOW = new Date('2026-08-28T10:00:00Z').getTime();
  const at = (msAgo) => new Date(NOW - msAgo).toISOString();

  it('maps stored statuses onto one vocabulary', () => {
    expect(describeResolution({ status: 'Success' }, NOW)).toMatchObject({ label: 'Done', tone: 'success' });
    expect(describeResolution({ status: 'Failed' }, NOW)).toMatchObject({ label: 'Failed', tone: 'critical' });
    expect(describeResolution({ status: 'InProgress' }, NOW)).toMatchObject({ label: 'Running', tone: 'info' });
  });

  // "Configuring" is a real stored value, and future writers may add more. None of them should reach
  // the screen as a bare enum.
  it('treats any non-terminal status as running', () => {
    expect(describeResolution({ status: 'Configuring' }, NOW)).toMatchObject({ label: 'Running', tone: 'info' });
    expect(describeResolution({ status: 'SomethingNew' }, NOW)).toMatchObject({ label: 'Running', tone: 'info' });
  });

  describe('actor', () => {
    it('names the person for a user-driven run', () => {
      expect(describeResolution({ status: 'Success', resolver_type: 'User', resolver_display_name: 'Mayank' }, NOW).actor).toBe('Mayank');
    });

    it('falls back when the display name is missing', () => {
      expect(describeResolution({ status: 'Success', resolver_type: 'User' }, NOW).actor).toBe('a user');
    });

    // Nubi and automations act without a person, and the type is the useful label there.
    it('names the system for an autonomous run', () => {
      expect(describeResolution({ status: 'Success', resolver_type: 'NBLLM' }, NOW).actor).toBe('NBLLM');
      expect(describeResolution({ status: 'InProgress', resolver_type: 'AutoRunbook' }, NOW).actor).toBe('AutoRunbook');
    });
  });

  describe('slow runs', () => {
    // Retry is offered on Failed only, so a run that never reports back would otherwise leave the
    // user with nothing to do and nothing to read.
    it('flags a run still going past the threshold', () => {
      const r = { status: 'InProgress', updated_at: at(RESOLUTION_SLOW_AFTER_MS + 1000) };
      expect(describeResolution(r, NOW).isSlow).toBe(true);
    });

    it('does not flag a run inside the threshold', () => {
      const r = { status: 'InProgress', updated_at: at(RESOLUTION_SLOW_AFTER_MS - 1000) };
      expect(describeResolution(r, NOW).isSlow).toBe(false);
    });

    it('never flags a finished run, however old', () => {
      const old = at(RESOLUTION_SLOW_AFTER_MS * 100);
      expect(describeResolution({ status: 'Success', updated_at: old }, NOW).isSlow).toBe(false);
      expect(describeResolution({ status: 'Failed', updated_at: old }, NOW).isSlow).toBe(false);
    });

    it('does not flag when there is no usable timestamp', () => {
      expect(describeResolution({ status: 'InProgress' }, NOW).isSlow).toBe(false);
      expect(describeResolution({ status: 'InProgress', updated_at: 'not-a-date' }, NOW).isSlow).toBe(false);
    });

    it('falls back to created_at when updated_at is absent', () => {
      const r = { status: 'InProgress', created_at: at(RESOLUTION_SLOW_AFTER_MS + 1000) };
      expect(describeResolution(r, NOW).isSlow).toBe(true);
    });
  });

  it('survives a missing resolution', () => {
    expect(describeResolution(undefined, NOW)).toMatchObject({ label: 'Running', isSlow: false });
  });
});

// A fix removes the cause; a mitigation only restores service. The remediation panel already tells
// operators this about Nubi's actions — the card actions should not be silent about it.
describe('describeActionKind', () => {
  it('classifies the actions we can classify honestly', () => {
    expect(describeActionKind('LastDeploymentCard')).toMatchObject({ kind: 'fix', text: 'Fix', tone: 'success' });
    expect(describeActionKind('MemoryAllocationCard')).toMatchObject({ kind: 'fix', tone: 'success' });
    // The code fix changes the cause in the source; its effect line carries the "not until it is
    // merged and deployed" caveat that the chip alone cannot.
    expect(describeActionKind('AskAiCard')).toMatchObject({ kind: 'fix', tone: 'success' });
  });

  // An unlabelled action is honest; a wrongly labelled one tells the operator the cause is gone
  // when it may not be.
  it('returns nothing rather than guessing', () => {
    expect(describeActionKind('SomeFutureCard')).toBeNull();
    expect(describeActionKind(undefined)).toBeNull();
  });

  it('explains the difference in words, not just colour', () => {
    expect(describeActionKind('LastDeploymentCard').help).toMatch(/removes the cause/i);
  });
});

// The confirmation dialog shows a diff and a Submit button, and never says the change replaces every
// running pod. The effect line is where that is stated.
describe('describeActionEffect', () => {
  it('names the consequence, not just the change', () => {
    expect(describeActionEffect('LastDeploymentCard')).toMatch(/rolling restart/i);
    expect(describeActionEffect('MemoryAllocationCard')).toMatch(/rolling restart/i);
  });

  it('says whether the action can be undone', () => {
    expect(describeActionEffect('LastDeploymentCard')).toMatch(/reversible/i);
  });

  // The code fix is the one action that changes nothing when it runs — approving it opens a PR.
  // Reading it beside actions that restart pods, an operator has to be told that.
  it('says when an action does not touch the cluster', () => {
    expect(describeActionEffect('AskAiCard')).toMatch(/pull request/i);
    expect(describeActionEffect('AskAiCard')).toMatch(/until it is merged and deployed/i);
  });

  // A wrong effect description is worse than none: it would be approved on the strength of it.
  it('returns nothing for actions we have not described', () => {
    expect(describeActionEffect('SomeFutureCard')).toBeNull();
    expect(describeActionEffect(undefined)).toBeNull();
  });
});

// The Remediation tab is the one place that claims to list every way to fix an event, and its badge
// counts what this returns.
describe('cardOffersAction', () => {
  it('counts a card that advertises a resolve button or carries a resolve component', () => {
    expect(cardOffersAction({ id: 'MemoryAllocationCard', resolveButton: true })).toBe(true);
    expect(cardOffersAction({ id: 'LastDeploymentCard_0', ResolveComponent: () => null })).toBe(true);
    expect(cardOffersAction({ id: 'MemoryAllocationCard', resolveButton: false })).toBe(false);
  });

  // AskAiCard always carries a ResolveComponent and turns resolveButton on only once log analysis
  // stored a fix diff for a mapped repo. Judged by the OR, every event would list a code-fix row
  // that opens on nothing to raise.
  it('lists the code fix only once there is a diff to raise', () => {
    expect(cardOffersAction({ id: 'AskAiCard', resolveButton: true, ResolveComponent: () => null })).toBe(true);
    expect(cardOffersAction({ id: 'AskAiCard', resolveButton: false, ResolveComponent: () => null })).toBe(false);
  });

  it('handles a missing or malformed option', () => {
    expect(cardOffersAction(undefined)).toBe(false);
    expect(cardOffersAction({})).toBe(false);
  });
});

// A verify result rides on the attempt it checked (data.verify), never as its own resolution.
// This is what makes "Done" mean "ran and was checked" rather than just "dispatched".
describe('describeResolution with verification', () => {
  const NOW = new Date('2026-08-31T10:00:00Z').getTime();
  const withVerify = (verify) => ({ status: 'Success', updated_at: new Date(NOW).toISOString(), data: { verify } });

  it('stays Done when the check confirmed the fix', () => {
    const v = describeResolution(withVerify({ ran: true, passed: true }), NOW);
    expect(v).toMatchObject({ label: 'Done', tone: 'success', verified: true });
  });

  it('is Needs checking when the check ran and failed', () => {
    const v = describeResolution(withVerify({ ran: true, passed: false }), NOW);
    expect(v).toMatchObject({ label: 'Needs checking', tone: 'warning' });
    expect(v.detail).toMatch(/did not confirm/i);
  });

  // Exit 0 having observed nothing — the distinction the whole three-valued design exists for.
  it('is Needs checking when the check proved nothing', () => {
    const v = describeResolution(withVerify({ ran: true, passed: null }), NOW);
    expect(v).toMatchObject({ label: 'Needs checking', tone: 'warning' });
    expect(v.detail).toMatch(/confirmed nothing/i);
  });

  // Card fixes have no verify command yet; they must keep reading exactly as they do today.
  it('is unchanged when no verify ran', () => {
    expect(describeResolution({ status: 'Success' }, NOW)).toMatchObject({ label: 'Done', tone: 'success' });
    expect(describeResolution({ status: 'Success', data: {} }, NOW)).toMatchObject({ label: 'Done' });
  });

  it('reads data whether it arrives parsed or as a string', () => {
    const asString = { status: 'Success', data: JSON.stringify({ verify: { ran: true, passed: false } }) };
    expect(describeResolution(asString, NOW)).toMatchObject({ label: 'Needs checking' });
  });
});

// An action is undoable only where the attempt recorded how to put things back. Offering a button
// that cannot work is worse than offering none.
describe('describeUndo', () => {
  const done = (extra = {}) => ({ id: 'res-1', status: 'Success', ...extra });

  it('offers to undo a revert from the event diff', () => {
    const u = describeUndo(done(), 'LastDeploymentCard');
    expect(u.label).toBe('Undo revert');
    expect(u.payload).toMatchObject({ revert: true, undo: true });
  });

  it('offers to undo any action that recorded a before-state', () => {
    const u = describeUndo(done({ data: { before: { containers: [{ container_name: 'app' }] } } }), 'MemoryAllocationCard');
    expect(u.label).toBe('Undo');
    // Keyed on the attempt, so the server re-dispatches that action with what it replaced.
    expect(u.payload).toMatchObject({ undo_resolution_id: 'res-1' });
  });

  // A restart has already happened; a PVC cannot shrink. Neither records a before-state.
  it('offers nothing when the attempt recorded no way back', () => {
    expect(describeUndo(done(), 'MemoryAllocationCard')).toBeNull();
    expect(describeUndo(done({ data: {} }), 'MemoryAllocationCard')).toBeNull();
    expect(describeUndo(done({ data: { before: {} } }), 'MemoryAllocationCard')).toBeNull();
  });

  it('offers nothing for an attempt that has not succeeded', () => {
    expect(describeUndo({ id: 'r', status: 'InProgress' }, 'LastDeploymentCard')).toBeNull();
    expect(describeUndo({ id: 'r', status: 'Failed' }, 'LastDeploymentCard')).toBeNull();
    expect(describeUndo(null, 'LastDeploymentCard')).toBeNull();
  });

  it('reads the record whether it arrives parsed or as a string', () => {
    const asString = done({ data: JSON.stringify({ before: { containers: [] } }) });
    // An empty containers list is still a recorded before-state.
    expect(describeUndo(asString, 'MemoryAllocationCard')).toMatchObject({ label: 'Undo' });
  });
});

// Card ids are not uniform — LastDeploymentCard carries a positional suffix, MemoryAllocationCard
// does not. Keying on the raw id silently skipped every numbered card, so the revert card lost its
// Fix label, its effect line and its Undo button while the resource card kept all three.
describe('card ids with a positional suffix', () => {
  it('classifies a suffixed card the same as a bare one', () => {
    expect(describeActionKind('LastDeploymentCard_0')).toMatchObject({ kind: 'fix' });
    expect(describeActionKind('LastDeploymentCard_3')).toMatchObject({ kind: 'fix' });
    expect(describeActionKind('LastDeploymentCard')).toMatchObject({ kind: 'fix' });
  });

  it('describes the effect of a suffixed card', () => {
    expect(describeActionEffect('LastDeploymentCard_0')).toMatch(/rolling restart/i);
  });

  it('offers Undo on a suffixed card', () => {
    const u = describeUndo({ id: 'r1', status: 'Success' }, 'LastDeploymentCard_0');
    expect(u).toMatchObject({ label: 'Undo revert' });
  });

  // Only a trailing index is stripped; an unrelated card must not be coerced into a known one.
  it('does not turn an unknown card into a known one', () => {
    expect(describeActionKind('SomeOtherCard_0')).toBeNull();
    expect(describeActionKind('LastDeploymentCardExtra')).toBeNull();
  });
});

describe('describeActionTitle', () => {
  // The card text names the finding, not the action. Attaching a state to the finding produced
  // "Last Deployment Change · Done", which reads as the deployment change being done.
  it('names what the action does, not what the card found', () => {
    expect(describeActionTitle('LastDeploymentCard_0', 'Last Deployment Change')).toBe('Revert the last deployment change');
    expect(describeActionTitle('MemoryAllocationCard', 'Check if Resource allocation is sufficient')).toBe('Adjust resource requests and limits');
  });

  it('treats a suffixed id the same as a bare one', () => {
    expect(describeActionTitle('LastDeploymentCard_3', 'x')).toBe(describeActionTitle('LastDeploymentCard', 'x'));
  });

  it('falls back to the card text for a card it does not know', () => {
    expect(describeActionTitle('SomeNewCard', 'Do the thing')).toBe('Do the thing');
  });

  it('never returns an empty label', () => {
    expect(describeActionTitle('SomeNewCard', '')).toBeTruthy();
    expect(describeActionTitle(undefined, undefined)).toBeTruthy();
  });
});

// Undo mutates a live workload. The confirmation is only worth having if it states the real change,
// so the preview must come from the same recorded data the server re-applies.
describe('undo preview', () => {
  const successfulRevert = { status: 'Success', id: 'r1' };

  it('previews an undo-revert as the diff it re-applies, oriented from what is running now', () => {
    const card = { diff: { data: { old: 'image: v1', new: 'image: v2' } } };
    const preview = describeUndo(successfulRevert, 'LastDeploymentCard_0', card).preview;
    expect(preview.kind).toBe('diff');
    expect(preview.before).toBe('image: v1'); // the reverted-to state, live right now
    expect(preview.after).toBe('image: v2'); // what undo puts back
    expect(preview.beforeLabel).toBe('Running now');
  });

  it('does not invent a diff when the event never recorded one', () => {
    expect(describeRevertUndoPreview({}).kind).toBe('unknown');
    expect(describeRevertUndoPreview({ diff: { data: { old: 'same', new: 'same' } } }).kind).toBe('unknown');
  });

  it('lists the values a generic undo restores', () => {
    const preview = describeBeforeStatePreview({
      containers: [{ container_name: 'app', cpu_request: '100m', memory_limit: '512Mi' }],
    });
    expect(preview.kind).toBe('values');
    expect(preview.groups[0].name).toBe('app');
    expect(preview.groups[0].fields).toEqual([
      { label: 'CPU request', value: '100m' },
      { label: 'Memory limit', value: '512Mi' },
    ]);
  });

  it('omits fields that were unset before the change rather than showing them as empty', () => {
    // An absent limit means "there was no limit"; restoring it removes the field. Rendering it as
    // an empty value would read as setting the limit to nothing.
    const preview = describeBeforeStatePreview({ containers: [{ container_name: 'app', cpu_request: '100m', cpu_limit: '' }] });
    expect(preview.groups[0].fields.map((f) => f.label)).toEqual(['CPU request']);
  });

  it('says so plainly when there is nothing to enumerate', () => {
    expect(describeBeforeStatePreview({ containers: [] }).kind).toBe('unknown');
    expect(describeBeforeStatePreview({}).summary).toBeTruthy();
  });

  it('names the workload being changed, not the pod the rollout replaces', () => {
    const card = { diff: { data: { resource_name: 'deployment/shop/checkout.yaml' } } };
    expect(describeUndoTarget(card, { subject_name: 'checkout-7d9f-abc' })).toBe('deployment/shop/checkout');
  });

  it('falls back to the event subject when the card carries no resource', () => {
    expect(describeUndoTarget({}, { subject_namespace: 'shop', subject_name: 'checkout-7d9f' })).toBe('shop/checkout-7d9f');
  });
});

describe('scale undo preview', () => {
  it('describes a scale undo as the replica count it restores', () => {
    const preview = describeBeforeStatePreview({ replicas: 4 });
    expect(preview.kind).toBe('values');
    expect(preview.groups[0].fields).toEqual([{ label: 'Replicas', value: '4' }]);
  });

  it('restores zero replicas rather than treating it as no value', () => {
    // A workload deliberately scaled to zero must be restorable to zero; a falsy check here would
    // silently drop it and leave the undo with nothing to apply.
    expect(describeBeforeStatePreview({ replicas: 0 }).groups[0].fields[0].value).toBe('0');
  });
});
