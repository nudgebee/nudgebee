// Frontend extension-point registry. Optional plugin bundles register
// components into neutrally-named slots from package init; renderSlot
// materializes whatever's registered, or returns null when nothing is.
//
// Slot names describe layout position, not feature, so the surrounding
// code stays neutral whether a slot is filled or empty.
import React, { type ComponentType, type ReactElement } from 'react';

export type SlotName = 'LayoutHeaderAction' | 'LayoutHeadExtras' | 'LayoutFloatingOverlay' | 'SignInProviderExtra' | 'SignInBelowTitle' | 'Branding';

const slots = new Map<SlotName, ComponentType<any>>();

export function registerSlot<P>(name: SlotName, component: ComponentType<P>): void {
  slots.set(name, component as ComponentType<any>);
}

export function renderSlot<P = Record<string, never>>(name: SlotName, props?: P): ReactElement | null {
  const Component = slots.get(name);
  if (!Component) return null;
  return React.createElement(Component, props as Record<string, unknown>);
}

export function hasSlot(name: SlotName): boolean {
  return slots.has(name);
}
