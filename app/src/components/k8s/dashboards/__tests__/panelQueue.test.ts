import { acquirePanelSlot, panelQueueState } from '../panelQueue';

/** Lets the promise callbacks queued by a release actually run. */
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

describe('acquirePanelSlot', () => {
  afterEach(async () => {
    // Drain, so one test's held slots cannot fail the next.
    while (panelQueueState().active > 0 || panelQueueState().waiting > 0) {
      const release = await acquirePanelSlot();
      release();
    }
  });

  it('runs four panels at once and queues the rest', async () => {
    const first = await Promise.all([acquirePanelSlot(), acquirePanelSlot(), acquirePanelSlot(), acquirePanelSlot()]);
    expect(panelQueueState().active).toBe(4);

    let fifthAdmitted = false;
    const fifth = acquirePanelSlot().then((release) => {
      fifthAdmitted = true;
      return release;
    });
    await settle();
    // The fifth panel is the whole point: it waits rather than adding a fifth
    // simultaneous provider query.
    expect(fifthAdmitted).toBe(false);
    expect(panelQueueState().waiting).toBe(1);

    first[0]();
    await settle();
    expect(fifthAdmitted).toBe(true);
    expect(panelQueueState().active).toBe(4);

    (await fifth)();
    first.slice(1).forEach((release) => release());
    await settle();
    expect(panelQueueState()).toEqual({ active: 0, waiting: 0 });
  });

  it('releases idempotently', async () => {
    // Callers release from both the settle path and an effect cleanup, in either
    // order — a double release would decrement the count twice and let the cap
    // drift upwards forever.
    const release = await acquirePanelSlot();
    release();
    release();
    expect(panelQueueState()).toEqual({ active: 0, waiting: 0 });
  });

  it('frees a slot a panel never reports back on', async () => {
    jest.useFakeTimers();
    try {
      const held = acquirePanelSlot();
      await Promise.resolve();
      expect(panelQueueState().active).toBe(1);
      // Nothing in the fetch layer times out; without this a wedged provider
      // request would starve every panel queued behind it for the life of the page.
      jest.advanceTimersByTime(15_000);
      expect(panelQueueState().active).toBe(0);
      (await held)();
    } finally {
      jest.useRealTimers();
    }
  });
});
