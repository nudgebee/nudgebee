import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import WorkflowTemplatesModal from '../WorkflowTemplatesModal';

jest.mock('@api1/workflow', () => ({
  __esModule: true,
  default: { listTemplates: jest.fn() },
}));

jest.mock('next/router', () => ({
  useRouter: () => ({ push: jest.fn(), query: {} }),
}));

import apiWorkflow from '@api1/workflow';

const mockList = (apiWorkflow as any).listTemplates as jest.Mock;

const template = (id: string, name: string, labels: string[]) => ({ id, name, description: '', category: 'monitoring', tags: { labels } });

const EC2 = template('1', 'EC2 Host Diagnostics', ['diagnostic']);
const RESTART = template('2', 'Rollout Restart', ['restart']);

const respond = (templates: any[]) => ({ data: { workflow_list_template: { templates } } });

const renderModal = () => render(<WorkflowTemplatesModal open onClose={jest.fn()} accountId='acct-1' />);

const typeSearch = (value: string) => fireEvent.change(screen.getByPlaceholderText('Search templates'), { target: { value } });

describe('WorkflowTemplatesModal search', () => {
  beforeEach(() => {
    mockList.mockReset();
  });

  it('sends the typed query to the server with the raised limit', async () => {
    mockList.mockImplementation((_category, name) => Promise.resolve(respond(name ? [EC2] : [EC2, RESTART])));

    renderModal();
    await screen.findByText('Rollout Restart');
    expect(mockList).toHaveBeenLastCalledWith(undefined, undefined, 200);

    typeSearch('  ec2 ');

    await waitFor(() => expect(mockList).toHaveBeenLastCalledWith(undefined, 'ec2', 200));
    await waitFor(() => expect(screen.queryByText('Rollout Restart')).not.toBeInTheDocument());
    expect(screen.getByText('EC2 Host Diagnostics')).toBeInTheDocument();
  });

  it('keeps the selected label chip while searching', async () => {
    mockList.mockImplementation((_category, name) => Promise.resolve(respond(name ? [EC2] : [EC2, RESTART])));

    renderModal();
    await screen.findByText('Rollout Restart');

    fireEvent.click(screen.getByRole('button', { name: 'restart' }));
    await waitFor(() => expect(screen.queryByText('EC2 Host Diagnostics')).not.toBeInTheDocument());

    typeSearch('ec2');

    // The search result has no 'restart' template: the chip stays selected and
    // still ANDs with the search, so nothing matches.
    await screen.findByText('No templates match "ec2"');
    expect(screen.getByRole('button', { name: 'restart' })).toHaveAttribute('aria-pressed', 'true');
  });

  it('clears the search and label chips when the modal is closed and reopened', async () => {
    mockList.mockImplementation((_category, name) => Promise.resolve(respond(name ? [EC2] : [EC2, RESTART])));

    const { rerender } = renderModal();
    await screen.findByText('Rollout Restart');

    fireEvent.click(screen.getByRole('button', { name: 'restart' }));
    typeSearch('ec2');
    await waitFor(() => expect(mockList).toHaveBeenLastCalledWith(undefined, 'ec2', 200));

    rerender(<WorkflowTemplatesModal open={false} onClose={jest.fn()} accountId='acct-1' />);
    rerender(<WorkflowTemplatesModal open onClose={jest.fn()} accountId='acct-1' />);

    await screen.findByText('Rollout Restart');
    expect(screen.getByText('EC2 Host Diagnostics')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('Search templates')).toHaveValue('');
    expect(screen.getByRole('button', { name: 'restart' })).toHaveAttribute('aria-pressed', 'false');
    expect(mockList).toHaveBeenLastCalledWith(undefined, undefined, 200);
  });

  it('shows a search-specific empty state', async () => {
    mockList.mockImplementation((_category, name) => Promise.resolve(respond(name ? [] : [EC2])));

    renderModal();
    await screen.findByText('EC2 Host Diagnostics');

    typeSearch('zzz');

    await screen.findByText('No templates match "zzz"');
  });
});

describe('WorkflowTemplatesModal event recommendations', () => {
  const TICKET = template('3', 'Create Ticket from Event', ['ticket']);

  const renderForEvent = () =>
    render(
      <WorkflowTemplatesModal
        open
        onClose={jest.fn()}
        accountId='acct-1'
        eventSources={['prometheus']}
        alertNames={['KubePodCrashLooping']}
        subjectTypes={['pod']}
      />
    );

  // The 5th argument (event sources) marks the event-matched request.
  const byEvent = (matched: any[], all: any[]) => (_c: any, _n: any, _l: any, _t: any, sources?: string[]) =>
    Promise.resolve(respond(sources ? matched : all));

  beforeEach(() => {
    mockList.mockReset();
  });

  it('shows matched templates first and does not repeat them in the rest', async () => {
    mockList.mockImplementation(byEvent([RESTART], [EC2, RESTART, TICKET]));

    renderForEvent();
    await screen.findByText('Recommended for this event');

    expect(mockList).toHaveBeenCalledWith(undefined, undefined, 200, undefined, ['prometheus'], ['KubePodCrashLooping'], ['pod']);
    expect(mockList).toHaveBeenCalledWith(undefined, undefined, 200);
    expect(screen.getByText('Other templates')).toBeInTheDocument();
    expect(screen.getAllByText('Rollout Restart')).toHaveLength(1);
    expect(screen.getByText('Create Ticket from Event')).toBeInTheDocument();
    expect(screen.getByText('Source: prometheus')).toBeInTheDocument();
  });

  it('falls back to every template when nothing matches the event', async () => {
    mockList.mockImplementation(byEvent([], [EC2, TICKET]));

    renderForEvent();
    await screen.findByText(/No templates matched this event/);

    expect(screen.getByText('Create Ticket from Event')).toBeInTheDocument();
    expect(screen.queryByText('Recommended for this event')).not.toBeInTheDocument();
  });

  it('does not blame the event when only the search matched nothing', async () => {
    mockList.mockImplementation((_c: any, name: any, _l: any, _t: any, sources?: string[]) =>
      Promise.resolve(respond(sources ? (name ? [] : [RESTART]) : name ? [EC2] : [EC2, RESTART]))
    );

    renderForEvent();
    await screen.findByText('Recommended for this event');
    typeSearch('ec2');

    await waitFor(() => expect(screen.queryByText('Rollout Restart')).not.toBeInTheDocument());
    expect(screen.getByText('EC2 Host Diagnostics')).toBeInTheDocument();
    expect(screen.queryByText(/No templates matched this event/)).not.toBeInTheDocument();
  });

  it('narrows both sections by the selected label', async () => {
    mockList.mockImplementation(byEvent([RESTART, EC2], [EC2, RESTART, TICKET]));

    renderForEvent();
    await screen.findByText('Recommended for this event');

    fireEvent.click(screen.getByRole('button', { name: 'restart' }));

    await waitFor(() => expect(screen.queryByText('EC2 Host Diagnostics')).not.toBeInTheDocument());
    expect(screen.getByText('Rollout Restart')).toBeInTheDocument();
    expect(screen.queryByText('Create Ticket from Event')).not.toBeInTheDocument();
  });

  it('does not show the no-match notice on a category tab', async () => {
    mockList.mockImplementation((category: any, _n: any, _l: any, _t: any, sources?: string[]) =>
      Promise.resolve(respond(sources ? (category ? [] : [RESTART]) : category ? [EC2] : [EC2, RESTART]))
    );

    renderForEvent();
    await screen.findByText('Recommended for this event');

    fireEvent.click(screen.getByRole('tab', { name: 'Monitoring' }));

    await screen.findByText('EC2 Host Diagnostics');
    expect(screen.queryByText('Recommended for this event')).not.toBeInTheDocument();
    expect(screen.queryByText(/No templates matched this event/)).not.toBeInTheDocument();
  });

  it('shows the full list without blaming the event when the matched request fails', async () => {
    mockList.mockImplementation((_c: any, _n: any, _l: any, _t: any, sources?: string[]) =>
      Promise.resolve(sources ? { data: null, errors: [new Error('boom')] } : respond([EC2, TICKET]))
    );

    renderForEvent();
    await screen.findByText('Create Ticket from Event');

    expect(screen.getByText('EC2 Host Diagnostics')).toBeInTheDocument();
    expect(screen.queryByText(/No templates matched this event/)).not.toBeInTheDocument();
    expect(screen.queryByText('Recommended for this event')).not.toBeInTheDocument();
  });

  it('makes one request and shows no section headings without an event', async () => {
    mockList.mockImplementation(() => Promise.resolve(respond([EC2, RESTART])));

    renderModal();
    await screen.findByText('Rollout Restart');

    expect(mockList).toHaveBeenCalledTimes(1);
    expect(screen.queryByText('Recommended for this event')).not.toBeInTheDocument();
    expect(screen.queryByText(/No templates matched this event/)).not.toBeInTheDocument();
  });
});
