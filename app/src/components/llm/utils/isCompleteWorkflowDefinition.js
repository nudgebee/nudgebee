// Returns true only for a genuine, complete workflow object that is safe to auto-apply to the
// AI-builder canvas: a `definition` wrapper, or a non-empty `tasks` array WITH at least one
// trigger. This guards against a read-only answer (issue #30825 — explain / diagnose) quoting a
// partial task fragment, which would otherwise silently overwrite the canvas. Note: empty arrays
// are truthy in JS, so `triggers` must be checked for non-empty length explicitly.
export const isCompleteWorkflowDefinition = (parsed) => {
  if (!parsed || typeof parsed !== 'object') {
    return false;
  }
  if (parsed.definition) {
    return true;
  }
  const hasTasks = Array.isArray(parsed.tasks) && parsed.tasks.length > 0;
  const hasTrigger = (Array.isArray(parsed.triggers) && parsed.triggers.length > 0) || Boolean(parsed.trigger);
  return hasTasks && hasTrigger;
};
