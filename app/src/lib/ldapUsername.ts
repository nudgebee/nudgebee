/**
 * Builds the two strings the LDAP provider derives from the typed username:
 * the name it binds as and the filter it looks the entry up with.
 *
 * The username is user input and each destination has its own grammar, so it
 * is escaped for the one it lands in. Spliced in raw, `testuser+test1` made
 * the bind DN `uid=testuser+test1,dc=…` — `+` joins the parts of a
 * multi-valued RDN, so the server rejected the DN before it looked at the
 * password. The same splice let `*`, `(` and `)` rewrite the lookup filter.
 *
 * Both escapes use the `\XX` hex form, which every server must accept in a DN
 * (RFC 4514) and in a filter (RFC 4515).
 */
const hexEscape = (char: string) => `\\${char.charCodeAt(0).toString(16).padStart(2, '0')}`;

// RFC 4514 §2.4: special anywhere in a DN attribute value, plus a leading
// space or `#` and a trailing space.
const escapeDnValue = (value: string) => value.replace(/[\\,+"<>;=\0]|^[ #]| $/g, hexEscape);

// RFC 4515 §3
const escapeFilterValue = (value: string) => value.replace(/[\\*()\0]/g, hexEscape);

/**
 * The name is escaped only where the template puts it in a DN attribute value
 * (`uid=%s,dc=example,dc=com`). Every other bind form an operator may configure
 * takes it as typed, and escaping there would corrupt it: Active Directory's
 * `%s@corp.example` or `CORP\%s`, and templates that leave the attribute to the
 * user (`%s,dc=example,dc=com`, or a bare `%s` for a full DN).
 */
export function ldapBindName(template: string, username: string): string {
  const value = /=\s*%s/.test(template) ? escapeDnValue(username) : username;
  // A replacer function, so `$&` and friends in a username stay literal.
  return template.replace('%s', () => value);
}

export function ldapSearchFilter(template: string, username: string): string {
  const value = escapeFilterValue(username);
  return template.replace('%s', () => value);
}
