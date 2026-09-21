import { NAMED_RCA_FORMAT_TEMPLATES, NUDGEBEE_DEFAULT_TEMPLATE_ID } from '../rcaFormatTemplates';

// The catalogue is data loaded straight into the settings editor, so these are
// the checks a type can't make: the names match the wording the product spec
// (issue #35282) asks for, ids are safe to use as Select values, and each body
// is a real fill-in-the-blank skeleton rather than prose or an empty string.
describe('RCA format templates', () => {
  it('offers exactly the three named templates from the spec', () => {
    expect(NAMED_RCA_FORMAT_TEMPLATES.map((t) => t.name)).toEqual(['Incident postmortem', 'Executive summary', 'ITIL-style problem record']);
  });

  it('has unique kebab-case ids that never collide with the default sentinel', () => {
    const ids = NAMED_RCA_FORMAT_TEMPLATES.map((t) => t.id);
    expect(new Set(ids).size).toBe(ids.length);
    for (const id of ids) {
      expect(id).toMatch(/^[a-z0-9]+(-[a-z0-9]+)*$/);
      expect(id).not.toBe(NUDGEBEE_DEFAULT_TEMPLATE_ID);
    }
  });

  it('gives every template a usable name, description, and Markdown body', () => {
    for (const template of NAMED_RCA_FORMAT_TEMPLATES) {
      expect(template.name.trim()).not.toBe('');
      expect(template.description.trim()).not.toBe('');
      expect(template.body.startsWith('# ')).toBe(true);
      expect(template.body.length).toBeGreaterThan(200);
      expect(template.body).not.toContain('TODO');
      // At least one [bracketed] placeholder to fill in.
      expect(template.body).toMatch(/\[[A-Za-z]/);
    }
  });
});
