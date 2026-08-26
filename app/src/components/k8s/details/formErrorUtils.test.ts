import { addFormError, removeFormError, type FormErrors } from './formErrorUtils';

describe('formErrorUtils', () => {
  it('stores ordinary and inherited-name keys as own properties', () => {
    const errors: FormErrors = {};
    addFormError(errors, 'toString', 'valueOf', 'Required');

    expect(Object.prototype.hasOwnProperty.call(errors, 'toString')).toBe(true);
    expect(errors.toString.valueOf).toBe('Required');
  });

  it.each(['__proto__', 'constructor', 'prototype'])('rejects reserved action key %s', (key) => {
    const errors: FormErrors = {};
    addFormError(errors, key, 'field', 'Required');
    expect(Object.keys(errors)).toHaveLength(0);
  });

  it('removes an error immutably and drops an empty action entry', () => {
    const errors: FormErrors = { action: { first: 'Required', second: 'Required' } };
    const next = removeFormError(errors, 'action', 'first');

    expect(next).not.toBe(errors);
    expect(next.action).not.toBe(errors.action);
    expect(errors.action.first).toBe('Required');
    expect(next.action).toEqual({ second: 'Required' });
    expect(removeFormError(next, 'action', 'second')).toEqual({});
  });

  it('returns the original state for unsafe or missing action keys', () => {
    const errors: FormErrors = { action: { field: 'Required' } };
    expect(removeFormError(errors, '__proto__', 'field')).toBe(errors);
    expect(removeFormError(errors, 'missing', 'field')).toBe(errors);
    expect(removeFormError(errors, 'action', 'missing')).toBe(errors);
  });
});
