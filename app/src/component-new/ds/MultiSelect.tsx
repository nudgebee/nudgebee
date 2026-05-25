/**
 * MultiSelect — deprecated. Merged into Select via the `multiple` prop.
 *
 *   import { Select } from '@components1/ds/Select';
 *   <Select multiple value={[...]} onChange={(arr) => ...} options={...} />
 *
 * Re-exports kept so existing imports don't break. Delete once all call sites
 * migrate to `Select` directly.
 */
export { Select as MultiSelect, Select as default } from './Select';
export type { SelectOption as MultiSelectOption, SelectSize as MultiSelectSize, SelectMultipleProps as MultiSelectProps } from './Select';
