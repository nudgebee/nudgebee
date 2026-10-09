/**
 * The info icon is the only place a user learns which label names a webhook sends,
 * so the two things that must hold are: it lists what the backend declared, and it
 * disappears rather than showing an empty box when there is nothing to list.
 */
import React from 'react';
import { render, screen, within } from '@testing-library/react';
import WebhookLabelHelpTooltip, { WebhookLabelHelpContent } from '../WebhookLabelHelpTooltip';

const help = {
  summary: 'Keys under labels in the connector body become labels under their own name.',
  labels: [
    { label: '<key under labels>', source: 'labels.<key>' },
    { label: 'alertname', source: 'rule.name' },
  ],
  notes: ['status, severity, title, message and tags are not labels.'],
};

describe('WebhookLabelHelpContent', () => {
  it('lists each label with the payload field it comes from', () => {
    render(<WebhookLabelHelpContent help={help} />);

    expect(screen.getByText(help.summary)).toBeInTheDocument();
    const rows = screen.getAllByTestId('webhook-label-help-row');
    expect(rows).toHaveLength(2);
    expect(within(rows[1]).getByText('alertname')).toBeInTheDocument();
    expect(within(rows[1]).getByText('rule.name')).toBeInTheDocument();
    expect(screen.getByText(help.notes[0])).toBeInTheDocument();
  });

  it('says values must match where the section compares values', () => {
    render(<WebhookLabelHelpContent help={{ summary: 'x', labels: help.labels }} matchesValues />);

    expect(screen.getByText(/Names and values must match exactly, including case/)).toBeInTheDocument();
    expect(screen.getByText('&cluster=dev')).toBeInTheDocument();
  });

  it('speaks only of names where the section takes label keys', () => {
    render(<WebhookLabelHelpContent help={{ summary: 'x', labels: help.labels }} />);

    expect(screen.getByText(/Label names are case-sensitive/)).toBeInTheDocument();
    expect(screen.queryByText(/values must match/)).not.toBeInTheDocument();
    expect(screen.getByText('&cluster=dev')).toBeInTheDocument();
  });
});

describe('WebhookLabelHelpTooltip', () => {
  it('renders the info icon when the backend sent a label list', () => {
    render(<WebhookLabelHelpTooltip help={help} />);

    expect(screen.getByTestId('webhook-label-help')).toBeInTheDocument();
  });

  it.each([
    ['an older backend that sends no help', undefined],
    ['a webhook that produces no labels', { summary: 'This webhook produces no labels.' }],
  ])('renders nothing for %s', (_name, value) => {
    const { container } = render(<WebhookLabelHelpTooltip help={value} />);

    expect(container).toBeEmptyDOMElement();
  });
});
