import React from 'react';
import { act, render } from '@testing-library/react';
import RunAutomationMenu from '../RunAutomationMenu';

jest.mock('next/router', () => ({ useRouter: () => ({ push: jest.fn(), query: {} }) }));
jest.mock('@api1/workflow', () => ({
  __esModule: true,
  default: {
    listExecutionsForEvent: jest.fn().mockResolvedValue({ data: [] }),
    listWorkflows: jest.fn().mockResolvedValue({ data: [] }),
    triggerWorkflow: jest.fn(),
  },
}));
// The menu as a plain trigger: a click on it is what opening the menu means here.
const mockTriggerClicks = jest.fn();
jest.mock('@ui/DropdownMenu', () => ({
  __esModule: true,
  DropdownMenu: ({ trigger }: { trigger: React.ReactNode }) => <div onClickCapture={() => mockTriggerClicks()}>{trigger}</div>,
}));

const menu = (openRequest: number) => <RunAutomationMenu accountId='acc-1' eventId='ev-1' canView canRun openRequest={openRequest} hideTrigger />;

describe('RunAutomationMenu openRequest', () => {
  beforeEach(() => mockTriggerClicks.mockClear());

  it('opens on a request made while it is mounted, not on the one it mounts with', async () => {
    const view = render(menu(0));
    await act(async () => {});
    expect(mockTriggerClicks).not.toHaveBeenCalled();
    view.rerender(menu(1));
    await act(async () => {});
    expect(mockTriggerClicks).toHaveBeenCalledTimes(1);

    // The person leaves the tab and comes back: the menu mounts again with the request it already answered.
    view.unmount();
    render(menu(1));
    await act(async () => {});
    expect(mockTriggerClicks).toHaveBeenCalledTimes(1);
  });
});
