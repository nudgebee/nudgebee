// Not for OSS

// Ids and copy this module asserts against, all read from the components that render
// the Infra > Cloud > AWS > EC2 surface. Kept here so a rename in app/src is one edit.
//
//   app/src/components/cloudaccount/ec2/Instances.tsx  InstancesView
//   app/src/components/cloudaccount/ec2/Summary.tsx    Ec2Summary

// InstancesView passes this to CloudAccountTable, which forwards it to CustomTable as
// `id`. CustomTable renders it on the <table>, `${id}-body` on the <tbody> and
// `${id}-no-data` on the empty panel's <h2>.
export const INSTANCES_TABLE = "ec2OptimizeInstancesTable";

// INSTANCE_HEADER in Instances.tsx. The ninth column is deliberately unnamed — it holds
// the per-row actions menu — so only the eight labelled ones are asserted.
export const INSTANCE_COLUMNS = [
  "Instance ID",
  "Instance Name",
  "CPU usage",
  "Memory usage",
  "State",
  "Cost",
  "Tags",
  "Launch Time",
];

// Zero-based index of the State column within INSTANCE_COLUMNS.
export const STATE_COLUMN = 4;

// The `id` each toolbar widget is given in InstancesView. SearchInput puts its id
// straight on the <input>; FilterDropdown builds its trigger id as
// `auto-complete-${toKebabCase(id)}` (ds/FilterDropdown.jsx:1074-1081), and these
// contain no spaces or underscores so they kebab to themselves.
export const SEARCH_INPUT_ID = "ec2-instances-search";
export const FILTER = {
  state: "ec2-filter-state",
  region: "ec2-filter-region",
  tagKey: "ec2-filter-tag-key",
  tagValue: "ec2-filter-tag-value",
} as const;

// FilterDropdown renders `label` as the trigger's leading text.
export const FILTER_LABEL: Record<keyof typeof FILTER, string> = {
  state: "State",
  region: "Region",
  tagKey: "Tag Key",
  tagValue: "Tag Value",
};

// The tabs CustomTable builds from InstancesView's `expandable.tabs`, in order.
export const DRILLDOWN_TABS = ["Details", "Monitoring", "Events", "Vulnerabilities", "Action History"];

// EC2_ACTIONS in app/src/components/cloudaccount/resourceActions.ts. Ordered least
// destructive first; the suite only ever opens their confirmation and cancels it.
// 'run_command' is deliberately excluded — it opens RunSsmCommandDialog, a bespoke
// dialog with no shared Cancel/Confirm footer.
export const LIFECYCLE_ACTIONS = ["reboot", "start", "stop"];

export const LIFECYCLE_ACTION_LABEL: Record<string, string> = {
  reboot: "Reboot Instance",
  start: "Start Instance",
  stop: "Stop Instance",
};

// getStateDropdownOptions('AmazonEC2') in app/src/components/cloudaccount/stateFilter.ts
// renders each native state Title Cased while filtering on the raw value, so the cell
// shows "running" where the dropdown option reads "Running".
export function stateOptionLabel(rawState: string): string {
  return rawState
    .toLowerCase()
    .split(/[-_\s]+/)
    .filter(Boolean)
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}

export function escapeForRegex(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
