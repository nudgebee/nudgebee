export const parseExecutionBatchMetadata = (metadata) => {
  if (!metadata) {
    return null;
  }
  let parsed = metadata;
  if (typeof parsed === 'string') {
    try {
      parsed = JSON.parse(parsed);
    } catch {
      return null;
    }
  }
  if (!parsed || typeof parsed !== 'object') {
    return null;
  }
  const plannerIteration = Number(parsed.planner_iteration) || 0;
  if (!parsed.execution_batch_id && plannerIteration <= 0) {
    return null;
  }
  return {
    id: parsed.execution_batch_id ? String(parsed.execution_batch_id) : `iteration-${plannerIteration}`,
    plannerIteration,
    mode: parsed.execution_mode || '',
    size: Number(parsed.execution_batch_size) || 0,
    parallelismLimit: Number(parsed.execution_parallelism_limit) || 0,
    fallbackReason: parsed.sequential_fallback_reason ? String(parsed.sequential_fallback_reason) : '',
  };
};

export const executionBatchLabel = (batch, observedSize = 0) => {
  if (!batch) {
    return '';
  }
  const size = batch.size || observedSize;
  const count = size > 0 ? `${size} direct ${size === 1 ? 'tool' : 'tools'}` : 'Tools';
  const parts = [];
  if (batch.plannerIteration > 0) {
    parts.push(`Iteration ${batch.plannerIteration}`);
  }
  parts.push(count);
  if (batch.mode === 'parallel_dispatch') {
    parts.push('In parallel');
  } else if (batch.mode === 'sequential_dispatch') {
    parts.push('One at a time');
  } else if (size > 1) {
    parts.push('Grouped');
  }
  return parts.join(' · ');
};

export const executionBatchTone = (batch) => (batch?.mode === 'parallel_dispatch' ? 'info' : 'neutral');

export const executionBatchTooltip = (batch) => {
  if (!batch) {
    return '';
  }
  const nestedNote = ' Nested rows are internal work and are not included in this count.';
  if (batch.mode === 'parallel_dispatch') {
    const dispatch =
      batch.parallelismLimit > 0
        ? `These tools were submitted together, with up to ${batch.parallelismLimit} running at once. Dependencies may still make some run one at a time.`
        : 'These tools were submitted together. Dependencies may still make some run one at a time.';
    return dispatch + nestedNote;
  }
  if (batch.mode !== 'sequential_dispatch') {
    return batch.plannerIteration > 0
      ? `These tool calls were emitted by planner iteration ${batch.plannerIteration}.`
      : 'These tool calls were emitted in one planner batch.';
  }
  if (batch.fallbackReason) {
    return `These tools ran one at a time: ${batch.fallbackReason.replace(/_/g, ' ')}.` + nestedNote;
  }
  return 'These tools ran one at a time.' + nestedNote;
};
