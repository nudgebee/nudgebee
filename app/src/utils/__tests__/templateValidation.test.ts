import { isValidTimeZone, validateDateTemplate } from '../templateValidation';

describe('isValidTimeZone', () => {
  it('accepts valid IANA zones', () => {
    expect(isValidTimeZone('Asia/Kolkata')).toBe(true);
    expect(isValidTimeZone('America/New_York')).toBe(true);
    expect(isValidTimeZone('UTC')).toBe(true);
  });

  it('treats empty string as valid (UTC, matches backend LoadLocation)', () => {
    expect(isValidTimeZone('')).toBe(true);
  });

  it('rejects bad zones', () => {
    expect(isValidTimeZone('Asia/Kolkta')).toBe(false);
    expect(isValidTimeZone('Not/AZone')).toBe(false);
  });
});

describe('validateDateTemplate', () => {
  it('errors on an invalid tz() zone', () => {
    const res = validateDateTemplate('{{ now() | tz("Asia/Kolkta") | strftime("%H") }}');
    expect(res.error).toContain('Invalid timezone');
    expect(res.error).toContain('Asia/Kolkta');
  });

  it('passes a valid tz() zone', () => {
    const res = validateDateTemplate('{{ now() | tz("Asia/Kolkata") | strftime("%H:%M") }}');
    expect(res.error).toBeUndefined();
    expect(res.warnings).toHaveLength(0);
  });

  it('warns when date_format() uses strftime %-codes', () => {
    const res = validateDateTemplate('{{ now() | date_format("%Y-%m-%d") }}');
    expect(res.error).toBeUndefined();
    expect(res.warnings).toHaveLength(1);
    expect(res.warnings[0]).toContain('date_format');
  });

  it('warns when strftime() uses a Go reference layout', () => {
    const res = validateDateTemplate('{{ now() | strftime("2006-01-02") }}');
    expect(res.error).toBeUndefined();
    expect(res.warnings).toHaveLength(1);
    expect(res.warnings[0]).toContain('strftime');
  });

  it('returns nothing for correct usage', () => {
    const res = validateDateTemplate('{{ now() | strftime("%Y-%m-%d %H:%M") }}');
    expect(res.error).toBeUndefined();
    expect(res.warnings).toHaveLength(0);
  });

  it('ignores non-template strings', () => {
    const res = validateDateTemplate('just a plain subject line');
    expect(res.error).toBeUndefined();
    expect(res.warnings).toHaveLength(0);
  });
});
