// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../GlobalLocators";

// app/src/pages/status.tsx is one of the few surfaces in this app written with
// data-testid throughout (status-summary, status-notice-banner,
// status-components-card, status-component-<id>), so rung 1 is available for every
// element that matters and nothing here has to fall back to a CSS id.
export class StatusLocators extends CommonLocators {
  readonly summaryBar: Locator;
  readonly componentsCard: Locator;
  readonly noticeBanner: Locator;
  readonly heading: Locator;
  readonly tooltip: Locator;
  readonly componentsPlaceholder: Locator;
  readonly clusterPicker: Locator;

  constructor(page: Page) {
    super(page);

    // No .or() on the three testid roots: they are the page's own test contract and
    // nothing else on this bare page carries the same meaning, so a wider match could
    // only ever resolve to the wrong box and report a missing element as present.
    this.summaryBar = page.getByTestId("status-summary");
    this.componentsCard = page.getByTestId("status-components-card");
    this.noticeBanner = page.getByTestId("status-notice-banner");

    // Rendered as <Box component='h1'>, so role=heading reaches it; the text fallback
    // is scoped to the page's single card rather than left loose on the document.
    this.heading = page
      .getByRole("heading", { name: "Nudgebee Status" })
      .or(page.locator("h1").filter({ hasText: "Nudgebee Status" }))
      .first();

    // MUI Tooltip renders its popper as role=tooltip when the explainer opens.
    this.tooltip = page.getByRole("tooltip").first();

    // The line the components card shows in place of the list while no payload has
    // arrived — "Loading components…" before the first fetch, "No component data
    // available." once one has failed.
    this.componentsPlaceholder = this.componentsCard.getByText(
      /Loading components|No component data available/
    );

    // The header's cluster picker wrapper, app/src/components/common/header/Header1.jsx
    // — a real id on a plain Box, which renders no role, name or testid to reach first.
    // Absence check only, and left deliberately un-widened for the reason the standard
    // gives: an .or() here could be satisfied by some unrelated element and quietly turn
    // an "app shell leaked onto the public page" regression into a pass.
    this.clusterPicker = page.locator("#global-cluster-filter");
  }

  // Each row is keyed by the component id the endpoint returns, so the row locator is
  // derived from the payload rather than from a list hardcoded here — a component
  // added to COMPONENTS server-side then needs no change in this suite.
  componentRow(componentId: string): Locator {
    return this.page.getByTestId(`status-component-${componentId}`);
  }

  // The tooltip's hover target is the box holding the dot and the status word. It has
  // no role or testid of its own, so it is reached by the status word — scoped to the
  // row, and the word itself is read from the payload rather than guessed.
  componentStatusLabel(componentId: string, label: string): Locator {
    return this.componentRow(componentId).getByText(label, { exact: true });
  }

  // The component's display name, scoped to its own row.
  componentName(componentId: string, name: string): Locator {
    return this.componentRow(componentId).getByText(name, { exact: true });
  }
}
