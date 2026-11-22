import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { ModelPickerPopover } from '@components/k8s/common/TextAreaV2';

// The modes differ in height — By-task adds a tier toggle and a per-tier
// summary. Where the surface opens and how tall it may be there are settled
// when it opens, so a mode change cannot move it. Opening beside the trigger
// and centred on it is preferred: the surface grows equally up and down, so the
// point it is pinned to never moves and it draws on the room on both sides.
describe('ModelPickerPopover placement', () => {
  const models = [{ configSource: 'env:global', configName: 'System', provider: 'googleai', model: 'gemini-3.7-flash' }] as any;

  // jsdom has no layout, so the trigger's position is supplied here. Its
  // viewport is 768 tall.
  const openWith = (rect: Partial<DOMRect>) => {
    render(<ModelPickerPopover credentials={[]} models={models} />);
    const trigger = screen.getByTestId('model-picker-trigger');
    jest.spyOn(trigger, 'getBoundingClientRect').mockReturnValue(rect as DOMRect);
    fireEvent.click(trigger);
    return screen.getByTestId('model-picker-popover');
  };

  const placement = () => screen.getByTestId('model-picker-popover').closest('[data-popper-placement]')?.getAttribute('data-popper-placement');

  it('opens beside the trigger, centred, when both sides have room', () => {
    // above = 384 - 8 = 376; below = 768 - 400 - 8 = 360; centred = 720.
    expect(openWith({ top: 384, bottom: 400 })).toHaveStyle({ maxHeight: 'min(720px, calc(100vh - 16px))' });
    expect(placement()).toBe('left');
  });

  it('falls back to opening above when there is too little room below to centre', () => {
    // below = 768 - 740 - 8 = 20, so centred = 40 — under the floor. above = 712.
    expect(openWith({ top: 720, bottom: 740 })).toHaveStyle({ maxHeight: 'min(712px, calc(100vh - 16px))' });
    expect(placement()).toBe('top-end');
  });

  it('falls back to opening below when the trigger is near the top', () => {
    // above = 20 - 8 = 12, so centred = 24 — under the floor. below = 708.
    expect(openWith({ top: 20, bottom: 52 })).toHaveStyle({ maxHeight: 'min(708px, calc(100vh - 16px))' });
    expect(placement()).toBe('bottom-end');
  });

  it('never squeezes the surface below its own chrome', () => {
    // above = 192, below = 160 — both under the floor, and so is centred (320
    // exactly is allowed, 2*160 = 320).
    expect(openWith({ top: 400, bottom: 600 })).toHaveStyle({ maxHeight: 'min(320px, calc(100vh - 16px))' });
  });

  it('keeps the surface where it is when the mode changes', () => {
    const el = openWith({ top: 384, bottom: 400 });
    const was = { placement: placement(), maxHeight: window.getComputedStyle(el).maxHeight };

    fireEvent.click(screen.getByRole('radio', { name: 'By task' }));

    // By-task is the taller mode: before this fix a height change could send
    // the surface to a different side of the trigger.
    expect(screen.getByTestId('model-picker-popover')).toBe(el);
    expect(placement()).toBe(was.placement);
    expect(window.getComputedStyle(el).maxHeight).toBe(was.maxHeight);
  });
});
