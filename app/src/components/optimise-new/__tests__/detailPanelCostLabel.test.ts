import fs from 'fs';
import path from 'path';

// The panel header renders the savings figure through Math.abs, so the label is
// the only thing separating "you save $32" from "this costs you $32". It read
// "Projected Savings" unconditionally, which put that label directly above an
// impact line reading "Costs ~$32/mo more" for an under-provisioned workload.
//
// Asserted against the source rather than a render: the contradiction only shows
// for a recommendation with negative estimated_savings, and mounting the panel
// pulls the whole details drawer with it.
describe('RecommendationDetailPanel savings label', () => {
  const source = fs.readFileSync(path.join(__dirname, '..', 'RecommendationDetailPanel.tsx'), 'utf8');

  it('picks the label from the direction of the figure', () => {
    expect(source).toMatch(/savings\s*>\s*0\s*\?\s*'Projected Savings'\s*:\s*'Additional Cost'/);
  });

  it('never labels the header as savings unconditionally', () => {
    expect(source).not.toMatch(/^\s*Projected Savings\s*$/m);
  });
});
