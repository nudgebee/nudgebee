import fs from 'fs';
import path from 'path';

import { ALLOWED_RELAY_ENDPOINTS, relayForwardBody, resolveRelayAccountId } from '@lib/relayEndpoints';

const SRC_ROOT = path.resolve(__dirname, '../..');

/**
 * Matches a relay path spelled as a literal, in either form the codebase might use —
 * concatenation (`getRelayServerEndpoint() + '/ws'`) or interpolation
 * (`${getRelayServerEndpoint()}/ws`) — and tolerant of whitespace, so a reformat
 * cannot quietly disable the sweep below.
 */
const RELAY_PATH_LITERAL = /(?:getRelayServerEndpoint\s*\(\s*\)\s*\+\s*['"`]|\$\{\s*getRelayServerEndpoint\s*\(\s*\)\s*\})\/([^'"`/\s]+)/g;

/**
 * The single-segment relay proxy (`/api/proxy/relay/[relay]`) rejects any segment
 * that is not on `ALLOWED_RELAY_ENDPOINTS`. Nothing else in the app knows about that
 * allowlist, so dropping an entry silently 400s an entire feature at runtime — which
 * is how the pod terminal broke (#36589: `ws` was removed on the mistaken belief that
 * relay-server's `/ws` is a WebSocket upgrade rather than a JSON POST route).
 *
 * These tests are the guard that was missing: the allowlist must stay a superset of
 * the paths the app appends to `getRelayServerEndpoint()`, without becoming a
 * pass-through.
 */
describe('relay proxy endpoint allowlist', () => {
  /**
   * Paths the app targets today. `hitRelayServer` (@lib/HttpService) selects
   * `/request` or `/grafana` through a variable and XtermTerminal appends `/ws`, so
   * a purely static scan cannot see all three. Add a path here when a new relay
   * caller lands.
   */
  const PATHS_IN_USE = ['request', 'grafana', 'ws'];

  it.each(PATHS_IN_USE)('allows %s, which the app actively targets', (segment) => {
    expect(ALLOWED_RELAY_ENDPOINTS.has(segment)).toBe(true);
  });

  it('rejects segments the app does not target, including traversal attempts', () => {
    // The allowlist exists to stop the dynamic segment reaching arbitrary upstream
    // paths (#28584); widening it must not weaken that.
    for (const segment of ['', '..', '../request', 'register', 'status', 'admin', 'ws/../register']) {
      expect(ALLOWED_RELAY_ENDPOINTS.has(segment)).toBe(false);
    }
  });

  it('allowlists every literal path appended to getRelayServerEndpoint() in src', () => {
    // Catches a new caller added with a literal path but no matching allowlist entry.
    // Callers that build the path through a variable are covered by PATHS_IN_USE.
    const files: string[] = [];
    const walk = (dir: string) => {
      for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
        const full = path.join(dir, entry.name);
        if (entry.isDirectory()) {
          if (entry.name !== 'node_modules' && entry.name !== '__tests__') walk(full);
        } else if (/\.(ts|tsx|js|jsx)$/.test(entry.name)) {
          files.push(full);
        }
      }
    };
    walk(SRC_ROOT);

    const found = new Set<string>();
    for (const file of files) {
      for (const match of fs.readFileSync(file, 'utf8').matchAll(RELAY_PATH_LITERAL)) {
        found.add(match[1]);
      }
    }

    // Guard against the scan silently matching nothing after a refactor.
    expect(found.size).toBeGreaterThan(0);
    for (const segment of found) {
      expect(ALLOWED_RELAY_ENDPOINTS.has(segment)).toBe(true);
    }
  });

  it('detects relay paths written by concatenation or interpolation', () => {
    const samples = [
      "getRelayServerEndpoint() + '/ws'",
      'getRelayServerEndpoint()   +   `/grafana`',
      '${getRelayServerEndpoint()}/request',
      '${ getRelayServerEndpoint() }/ws',
    ];
    for (const sample of samples) {
      RELAY_PATH_LITERAL.lastIndex = 0;
      expect(RELAY_PATH_LITERAL.exec(sample)?.[1]).toBeTruthy();
    }
  });
});

describe('resolveRelayAccountId', () => {
  it('rejects a body whose nested and top-level account ids differ', () => {
    // Own account nested, another tenant's at the top level: the proxy used to check
    // the nested one while relay /ws exec'd into the top-level one.
    const body = { account_id: 'victim', action: 'exec', body: { account_id: 'mine' } };
    for (const segment of ['ws', 'request', 'grafana']) {
      expect(resolveRelayAccountId(segment, body)).toEqual({ ok: false, error: expect.any(String) });
    }
  });

  it('authorises the top-level account id for ws, the only field relay /ws reads', () => {
    expect(resolveRelayAccountId('ws', { account_id: 'a1', action: 'start' })).toEqual({ ok: true, accountId: 'a1' });
    // Nested-only is not what /ws acts on, so nothing is authorised (hasAccountAccess denies).
    expect(resolveRelayAccountId('ws', { body: { account_id: 'a1' } })).toEqual({ ok: true, accountId: undefined });
  });

  it('authorises nested-then-top-level for request, matching relay /request', () => {
    expect(resolveRelayAccountId('request', { body: { account_id: 'a1' } })).toEqual({ ok: true, accountId: 'a1' });
    expect(resolveRelayAccountId('request', { account_id: 'a1' })).toEqual({ ok: true, accountId: 'a1' });
    expect(resolveRelayAccountId('request', { account_id: 'a1', body: { account_id: 'a1' } })).toEqual({ ok: true, accountId: 'a1' });
    // Relay treats an empty nested id as absent and falls back to the top level.
    expect(resolveRelayAccountId('request', { account_id: 'a1', body: { account_id: '' } })).toEqual({ ok: true, accountId: 'a1' });
  });

  it('treats a null account id as absent', () => {
    expect(resolveRelayAccountId('request', { account_id: 'a1', body: { account_id: null } })).toEqual({ ok: true, accountId: 'a1' });
  });

  it('rejects a non-string account id', () => {
    expect(resolveRelayAccountId('request', { body: { account_id: { $ne: '' } } }).ok).toBe(false);
    expect(resolveRelayAccountId('ws', { account_id: 123 }).ok).toBe(false);
  });
});

describe('relayForwardBody', () => {
  it('forwards only InteractiveShellRequest fields for ws, so a case-variant account key never reaches the relay', () => {
    // Go's encoding/json would bind ACCOUNT_ID (last case-insensitive match) while the
    // proxy authorised account_id.
    const body = {
      account_id: 'mine',
      ACCOUNT_ID: 'victim',
      Account_Id: 'victim',
      action: 'exec',
      session_id: 's',
      command: 'ls',
      track_history: true,
    };
    const forwarded = relayForwardBody('ws', body);
    expect(forwarded).toEqual({ account_id: 'mine', action: 'exec', session_id: 's', command: 'ls' });
    expect(
      JSON.stringify(forwarded)
        .toLowerCase()
        .match(/account_id/g)
    ).toHaveLength(1);
  });

  it('keeps every shell field the terminal sends', () => {
    const body = { account_id: 'a', action: 'start', session_id: 's', command: 'c', name: 'pod', namespace: 'ns', request_id: 'r', container: 'app' };
    expect(relayForwardBody('ws', body)).toEqual(body);
  });

  it('leaves other endpoints untouched', () => {
    const body = { body: { account_id: 'a', action_name: 'x' }, ACCOUNT_ID: 'b' };
    expect(relayForwardBody('request', body)).toBe(body);
  });
});
