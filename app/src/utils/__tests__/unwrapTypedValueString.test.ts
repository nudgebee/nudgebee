import { unwrapTypedValueString } from '@utils/common';

describe('unwrapTypedValueString', () => {
  it('unwraps a {type,value} envelope string to its inner value', () => {
    const envelope = JSON.stringify({
      type: 'string',
      value: "Can you please provide more details about the technical issue you're experiencing?",
    });
    expect(unwrapTypedValueString(envelope)).toBe("Can you please provide more details about the technical issue you're experiencing?");
  });

  it('leaves a plain question string untouched', () => {
    expect(unwrapTypedValueString('What namespace is the failing pod in?')).toBe('What namespace is the failing pod in?');
  });

  it('leaves other JSON objects untouched (not a string envelope)', () => {
    expect(unwrapTypedValueString('{"foo":"bar"}')).toBe('{"foo":"bar"}');
    expect(unwrapTypedValueString('{"type":"array","value":[]}')).toBe('{"type":"array","value":[]}');
    // has type+value keys but type is not "string" — different semantics, don't unwrap
    expect(unwrapTypedValueString('{"type":"regex","value":"err.*"}')).toBe('{"type":"regex","value":"err.*"}');
  });

  it('passes through empty and non-string input', () => {
    expect(unwrapTypedValueString('')).toBe('');
    // @ts-expect-error — guards against a non-string slipping in at runtime
    expect(unwrapTypedValueString(undefined)).toBe(undefined);
  });

  it('does not throw on malformed JSON that looks like an envelope', () => {
    expect(unwrapTypedValueString('{"type":"string","value":')).toBe('{"type":"string","value":');
  });
});
