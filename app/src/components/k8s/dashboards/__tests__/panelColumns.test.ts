import {
  addedColumns,
  columnSettings,
  isCompleteColumn,
  isSafeLinkUrl,
  panelColumnsOf,
  referencedColumns,
  renderRowUrl,
  setHiddenColumns,
} from '../panelColumns';
import type { Panel } from '@api1/dashboards';

const panel = (options?: Record<string, unknown>): Panel =>
  ({ id: 1, title: 'Events', type: 'table', datasource: 'nudgebee', grid_pos: { x: 0, y: 0, w: 6, h: 6 }, options } as Panel);

describe('isSafeLinkUrl', () => {
  it('takes an in-app path', () => {
    expect(isSafeLinkUrl('/investigate?id={{id}}&accountId={{account_id}}')).toBe(true);
    expect(isSafeLinkUrl('/kubernetes/details/{{id}}#events/all-events')).toBe(true);
  });

  it('refuses anything that leaves the product', () => {
    // A panel is authored by one user and rendered by every viewer, so these
    // are an XSS and a phishing vector respectively — not merely odd choices.
    expect(isSafeLinkUrl('javascript:alert(1)')).toBe(false);
    expect(isSafeLinkUrl('https://evil.test/steal?id={{id}}')).toBe(false);
    // Protocol-relative: another origin wearing a path's clothes.
    expect(isSafeLinkUrl('//evil.test/steal')).toBe(false);
    // Browsers strip these before parsing the scheme, so a prefix check alone
    // would pass a link that navigates off-product.
    expect(isSafeLinkUrl('/\tjavascript:alert(1)')).toBe(false);
    // The URL spec treats a backslash as `/` for http(s), so this parses as
    // `//evil.test` — protocol-relative in disguise.
    expect(isSafeLinkUrl('/\\evil.test')).toBe(false);
    expect(isSafeLinkUrl('')).toBe(false);
  });

  it('trims, because the server does', () => {
    // Otherwise a stray space saves happily server-side and then renders as no
    // link at all.
    expect(isSafeLinkUrl('  /investigate?id={{id}}  ')).toBe(true);
  });
});

describe('renderRowUrl', () => {
  const columns = ['title', 'id', 'account_id'];

  it('fills placeholders from the row', () => {
    expect(renderRowUrl('/investigate?id={{id}}&accountId={{account_id}}', columns, ['OOMKilled', 'evt-1', 'acc-1'])).toBe(
      '/investigate?id=evt-1&accountId=acc-1'
    );
  });

  it('renders from the trimmed path', () => {
    expect(renderRowUrl('  /investigate?id={{id}}  ', columns, ['t', 'evt-1', 'acc-1'])).toBe('/investigate?id=evt-1');
  });

  it('encodes the value', () => {
    // A raw `&` here would rewrite the query string rather than fill it in.
    expect(renderRowUrl('/investigate?id={{id}}', columns, ['t', 'a&b=c', 'acc-1'])).toBe('/investigate?id=a%26b%3Dc');
  });

  it('gives no link rather than a broken one', () => {
    // Half-substituted reads as a live link and lands nowhere.
    expect(renderRowUrl('/investigate?id={{id}}', columns, ['t', '', 'acc-1'])).toBeNull();
    expect(renderRowUrl('/investigate?id={{missing}}', columns, ['t', 'evt-1', 'acc-1'])).toBeNull();
    expect(renderRowUrl('https://evil.test/{{id}}', columns, ['t', 'evt-1', 'acc-1'])).toBeNull();
  });
});

describe('referencedColumns', () => {
  it('lists each placeholder once', () => {
    expect(referencedColumns('/investigate?id={{id}}&accountId={{account_id}}&again={{ id }}')).toEqual(['id', 'account_id']);
  });
});

describe('panelColumnsOf', () => {
  it('reads the settings an author configured', () => {
    const columns = panelColumnsOf(
      panel({
        columns: [
          { name: 'subject_name', link: { url: '/investigate?id={{id}}' } },
          { name: 'id', visibility: 'hidden' },
          { title: 'Investigate', link: { url: '/investigate?id={{id}}' } },
        ],
      })
    );
    expect(columnSettings(columns).get('id')?.visibility).toBe('hidden');
    expect(columnSettings(columns).get('subject_name')?.link?.url).toBe('/investigate?id={{id}}');
    expect(addedColumns(columns).map((c) => c.title)).toEqual(['Investigate']);
  });

  it('survives whatever an import stored', () => {
    expect(panelColumnsOf(panel())).toEqual([]);
    expect(panelColumnsOf(panel({ columns: 'nope' }))).toEqual([]);
    expect(panelColumnsOf(panel({ columns: [null, 'x'] }))).toEqual([]);
  });

  it('folds the two arrays it replaced', () => {
    // An OLD EXPORT arrives as raw JSON and never passes through the server's
    // upgrade, so the renderer has to fold it the same way — including merging
    // a link onto the hidden entry for the same column rather than emitting two.
    const columns = panelColumnsOf(
      panel({
        link_columns: [
          { column: 'subject_name', url: '/investigate?id={{id}}' },
          { title: 'Investigate', url: '/investigate?id={{id}}&accountId={{account_id}}' },
          { column: 'id', url: '/investigate?id={{id}}' },
        ],
        hidden_columns: ['id', 'account_id'],
      })
    );
    expect(columns).toEqual([
      { name: 'id', visibility: 'hidden', link: { url: '/investigate?id={{id}}' } },
      { name: 'account_id', visibility: 'hidden' },
      { name: 'subject_name', link: { url: '/investigate?id={{id}}' } },
      { title: 'Investigate', link: { url: '/investigate?id={{id}}&accountId={{account_id}}' } },
    ]);
  });

  it('prefers the current shape when a panel somehow carries both', () => {
    const columns = panelColumnsOf(panel({ columns: [{ name: 'id', visibility: 'hidden' }], hidden_columns: ['title'] }));
    expect(columns).toEqual([{ name: 'id', visibility: 'hidden' }]);
  });
});

describe('setHiddenColumns', () => {
  it('keeps whatever else a column says', () => {
    const columns = setHiddenColumns([{ name: 'id', link: { url: '/investigate?id={{id}}' } }], ['id']);
    expect(columns).toEqual([{ name: 'id', link: { url: '/investigate?id={{id}}' }, visibility: 'hidden' }]);
  });

  it('drops an entry that only ever said hidden', () => {
    // Otherwise unhiding leaves `{ name }` behind, and the list accumulates
    // entries that configure nothing.
    expect(setHiddenColumns([{ name: 'id', visibility: 'hidden' }], [])).toEqual([]);
  });

  it('leaves added columns alone', () => {
    const added = { title: 'Investigate', link: { url: '/investigate?id={{id}}' } };
    expect(setHiddenColumns([added], ['id'])).toEqual([added, { name: 'id', visibility: 'hidden' }]);
  });
});

describe('isCompleteColumn', () => {
  it('wants somewhere to go and something to click', () => {
    expect(isCompleteColumn({ title: 'Investigate', link: { url: '/investigate?id={{id}}' } })).toBe(true);
    expect(isCompleteColumn({ name: 'title', link: { url: '/investigate?id={{id}}' } })).toBe(true);
    // Settings-only entries need no link at all.
    expect(isCompleteColumn({ name: 'id', visibility: 'hidden' })).toBe(true);
    expect(isCompleteColumn({ title: 'Investigate', link: { url: '' } })).toBe(false);
    expect(isCompleteColumn({ link: { url: '/investigate' } })).toBe(false);
  });
});
