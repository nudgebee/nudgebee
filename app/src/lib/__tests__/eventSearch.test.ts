import { eventSearchClause } from '@lib/eventSearch';

// The events search box must find a cloud alert by its alarm name or resource,
// not only by its title: a CloudWatch title carries just the instance id, so a
// search for the host name in the alarm returned nothing (#41158).
describe('eventSearchClause', () => {
  it('matches title, resource and alert name', () => {
    expect(eventSearchClause('web-01')).toEqual({
      _or: [{ title: { _ilike: '%web-01%' } }, { subject_name: { _ilike: '%web-01%' } }, { aggregation_key: { _ilike: '%web-01%' } }],
    });
  });

  it('trims the typed text and matches ILIKE wildcards literally', () => {
    expect(eventSearchClause('  a%b_ ')?._or[0]).toEqual({ title: { _ilike: '%a\\%b\\_%' } });
  });

  it('returns null for blank input', () => {
    expect(eventSearchClause('')).toBeNull();
    expect(eventSearchClause('   ')).toBeNull();
    expect(eventSearchClause(undefined)).toBeNull();
  });
});
