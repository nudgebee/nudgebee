import { getKbNameError } from '@components/llm/kbName';

describe('getKbNameError', () => {
  it('accepts hyphens and underscores, like Agent Skills names', () => {
    expect(getKbNameError('pdf-processing')).toBe('');
    expect(getKbNameError('aws_ec2-runbook2')).toBe('');
  });

  it('still requires a leading letter and only letters, numbers, hyphens and underscores', () => {
    expect(getKbNameError('-pdf')).toBe('Name must start with a letter (a-z or A-Z)');
    expect(getKbNameError('pdf processing')).toBe('Name can only contain letters, numbers, hyphens and underscores');
    expect(getKbNameError('pdf.processing')).toBe('Name can only contain letters, numbers, hyphens and underscores');
  });

  it('keeps the empty and length rules', () => {
    expect(getKbNameError('')).toBe('Please enter a valid name for the knowledge base');
    expect(getKbNameError('ab')).toBe('Name must be between 3 and 50 characters');
    expect(getKbNameError(`a${'b'.repeat(50)}`)).toBe('Name must be between 3 and 50 characters');
  });
});
