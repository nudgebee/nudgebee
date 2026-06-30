/**
 * IconRegistry — DS unification of legacy CloudIcon, CloudProviderIcon,
 * LangTypeIcon, GetInsightIcon.
 * Spec: app/design-system/primitives/data-display/icon-registry.html
 *
 * Lookup-by-name pattern. Each registry key maps to a legacy icon component
 * that retains its original prop API. Old default-import paths continue to work.
 *
 * Usage:
 *   import { Icon } from '@ui/IconRegistry';
 *   <Icon name='cloudProvider' provider='aws' />
 *
 * To add a new icon to the registry, drop the component file under
 * app/src/components/common/ (or @assets/), import it here, and add an entry.
 */
import * as React from 'react';
import CloudIcon from '@shared/icons/CloudIcon';
import CloudProviderIcon from '@shared/icons/CloudProviderIcon';
import LangTypeIcon from '@shared/icons/LangTypeIcon';
import GetInsightIcon from '@shared/icons/GetInsightIcon';

export const IconRegistry = {
  cloud: CloudIcon,
  cloudProvider: CloudProviderIcon,
  langType: LangTypeIcon,
  insight: GetInsightIcon,
} as const;

export type IconName = keyof typeof IconRegistry;

interface IconProps {
  name: IconName;
  [key: string]: unknown;
}

export function Icon({ name, ...props }: IconProps) {
  const Component = IconRegistry[name] as React.ComponentType<Record<string, unknown>>;
  if (!Component) return null;
  return <Component {...props} />;
}

// Direct named re-exports for callers that prefer per-icon imports
export { CloudIcon, CloudProviderIcon, LangTypeIcon, GetInsightIcon };

export default Icon;
