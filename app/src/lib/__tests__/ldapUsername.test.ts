import { EqualityFilter, FilterParser } from 'ldapts';
import { ldapBindName, ldapSearchFilter } from '@lib/ldapUsername';

const DN_TEMPLATE = 'uid=%s,dc=example,dc=com';

// The typed username lands in a bind DN and in a search filter. Each has its own
// special characters, so the name is escaped for the one it is placed in.
// Regression guard for #25979 (`testuser+test1` could not sign in).
describe('ldapBindName', () => {
  it('escapes + so a plus-addressed username stays one RDN value', () => {
    expect(ldapBindName(DN_TEMPLATE, 'testuser+test1')).toBe('uid=testuser\\2btest1,dc=example,dc=com');
  });

  it('leaves an ordinary username untouched', () => {
    expect(ldapBindName(DN_TEMPLATE, 'raman.kharche01')).toBe('uid=raman.kharche01,dc=example,dc=com');
    expect(ldapBindName(DN_TEMPLATE, 'first-last_01')).toBe('uid=first-last_01,dc=example,dc=com');
    expect(ldapBindName(DN_TEMPLATE, 'josé')).toBe('uid=josé,dc=example,dc=com');
  });

  it('escapes every character that is special inside a DN value', () => {
    expect(ldapBindName('uid=%s', '\\,+"<>;=\0')).toBe('uid=\\5c\\2c\\2b\\22\\3c\\3e\\3b\\3d\\00');
  });

  it('escapes a leading space or #, and a trailing space, but not inner ones', () => {
    expect(ldapBindName('uid=%s', '#a#b')).toBe('uid=\\23a#b');
    expect(ldapBindName('uid=%s', ' a b ')).toBe('uid=\\20a b\\20');
    expect(ldapBindName('uid=%s', ' ')).toBe('uid=\\20');
  });

  it('does not let a username add RDNs of its own', () => {
    expect(ldapBindName(DN_TEMPLATE, 'x,ou=admins')).toBe('uid=x\\2cou\\3dadmins,dc=example,dc=com');
  });

  it('escapes when the template has a space after the equals sign', () => {
    expect(ldapBindName('uid = %s,dc=example,dc=com', 'testuser+test1')).toBe('uid = testuser\\2btest1,dc=example,dc=com');
  });

  it('passes the name as typed to a template that is not a DN', () => {
    expect(ldapBindName('%s@corp.example', 'first+tag')).toBe('first+tag@corp.example');
    expect(ldapBindName('CORP\\%s', 'first+tag')).toBe('CORP\\first+tag');
  });

  it('passes the name as typed when the template leaves the attribute to the user', () => {
    expect(ldapBindName('%s,dc=example,dc=com', 'uid=bob')).toBe('uid=bob,dc=example,dc=com');
    expect(ldapBindName('%s', 'uid=bob,dc=example,dc=com')).toBe('uid=bob,dc=example,dc=com');
  });

  it('keeps replacement patterns in a username literal', () => {
    expect(ldapBindName(DN_TEMPLATE, 'a$&b')).toBe('uid=a$&b,dc=example,dc=com');
  });
});

describe('ldapSearchFilter', () => {
  it('leaves + alone, which is not special in a filter', () => {
    expect(ldapSearchFilter('(uid=%s)', 'testuser+test1')).toBe('(uid=testuser+test1)');
    expect(ldapSearchFilter('uid=%s', 'raman.kharche01')).toBe('uid=raman.kharche01');
  });

  it('escapes every character that is special inside a filter value', () => {
    expect(ldapSearchFilter('(uid=%s)', '\\*()\0')).toBe('(uid=\\5c\\2a\\28\\29\\00)');
  });

  it('does not let a username widen the filter', () => {
    expect(ldapSearchFilter('(uid=%s)', '*')).toBe('(uid=\\2a)');
    expect(ldapSearchFilter('(uid=%s)', 'a)(uid=*')).toBe('(uid=a\\29\\28uid=\\2a)');
  });

  it('reaches the LDAP client as one equality match on the name as typed', () => {
    const usernames = ['testuser+test1', '*', 'a)(uid=*', ')(|(uid=*', 'a\\2ab', 'a\0b', 'josé'];
    for (const template of ['(uid=%s)', 'uid=%s']) {
      for (const username of usernames) {
        const filter = FilterParser.parseString(ldapSearchFilter(template, username));
        expect(filter).toBeInstanceOf(EqualityFilter);
        expect((filter as EqualityFilter).value).toBe(username);
      }
    }
  });

  it('keeps replacement patterns in a username literal', () => {
    expect(ldapSearchFilter('(uid=%s)', 'a$&b')).toBe('(uid=a$&b)');
  });
});
