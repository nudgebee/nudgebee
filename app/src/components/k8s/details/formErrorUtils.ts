export type FormErrors = Record<string, Record<string, string>>;

const RESERVED_OBJECT_KEYS = new Set(['__proto__', 'constructor', 'prototype']);

const isSafeObjectKey = (key: unknown): key is string => typeof key === 'string' && !RESERVED_OBJECT_KEYS.has(key);

export const addFormError = (errors: FormErrors, actionId: unknown, paramName: unknown, message: string): void => {
  if (!isSafeObjectKey(actionId) || !isSafeObjectKey(paramName)) return;

  if (!Object.prototype.hasOwnProperty.call(errors, actionId)) {
    errors[actionId] = {};
  }
  errors[actionId][paramName] = message;
};

export const removeFormError = (errors: FormErrors, actionId: unknown, paramName: unknown): FormErrors => {
  if (!isSafeObjectKey(actionId) || !isSafeObjectKey(paramName) || !Object.prototype.hasOwnProperty.call(errors, actionId)) {
    return errors;
  }
  if (!Object.prototype.hasOwnProperty.call(errors[actionId], paramName)) {
    return errors;
  }

  const actionErrors = { ...errors[actionId] };
  delete actionErrors[paramName];

  const nextErrors = { ...errors };
  if (Object.keys(actionErrors).length === 0) {
    delete nextErrors[actionId];
  } else {
    nextErrors[actionId] = actionErrors;
  }
  return nextErrors;
};
