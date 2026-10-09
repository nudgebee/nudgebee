import { joinConfigArray, checkConditionMatch, shouldShowField, isFieldRequired, getSortedFieldKeys, buildProbeConfigValues } from '../schemaForm';
import { ENCRYPTED_MASK } from '@api1/integrations/helpers';

// Loki-shaped schema: auth_type gates username/password — the live-verified
// conditional-reveal behavior this module must preserve.
const lokiConfig = {
  required: ['url'],
  properties: {
    url: { type: 'string', priority: 19 },
    auth_type: { type: 'string', priority: 18 },
    username: { type: 'string', priority: 17, show_when: { auth_type: ['basic'] }, required_when: { auth_type: ['basic'] } },
    password: { type: 'string', priority: 16, is_encrypted: true, show_when: { auth_type: ['basic'] }, required_when: { auth_type: ['basic'] } },
    hidden_field: { type: 'string', hidden: true },
  },
};

describe('checkConditionMatch', () => {
  it('matches a scalar condition by equality', () => {
    expect(checkConditionMatch('basic', 'basic')).toBe(true);
    expect(checkConditionMatch('basic', 'none')).toBe(false);
  });
  it('matches an array condition by membership', () => {
    expect(checkConditionMatch(['basic', 'bearer'], 'bearer')).toBe(true);
    expect(checkConditionMatch(['basic', 'bearer'], 'none')).toBe(false);
  });
});

describe('shouldShowField', () => {
  it('shows an unconditioned field and hides a hidden one', () => {
    expect(shouldShowField(lokiConfig, {}, 'url', lokiConfig.properties.url)).toBe(true);
    expect(shouldShowField(lokiConfig, {}, 'hidden_field', lokiConfig.properties.hidden_field)).toBe(false);
  });

  it('reveals a show_when field only when its condition holds (Loki auth None -> Basic)', () => {
    expect(shouldShowField(lokiConfig, { auth_type: 'none' }, 'username', lokiConfig.properties.username)).toBe(false);
    expect(shouldShowField(lokiConfig, { auth_type: 'basic' }, 'username', lokiConfig.properties.username)).toBe(true);
  });

  it('keeps a field hidden when its dependency is itself hidden', () => {
    const config = {
      properties: {
        parent: { type: 'string', show_when: { mode: ['advanced'] } },
        child: { type: 'string', show_when: { parent: ['on'] } },
      },
    };
    // parent is invisible (mode != advanced), so child stays hidden even
    // though formValues carries a matching parent value.
    expect(shouldShowField(config, { parent: 'on' }, 'child', config.properties.child)).toBe(false);
    expect(shouldShowField(config, { mode: 'advanced', parent: 'on' }, 'child', config.properties.child)).toBe(true);
  });

  it('survives mutually-referencing fields (recursion guard)', () => {
    const config = {
      properties: {
        a: { type: 'string', show_when: { b: ['x'] } },
        b: { type: 'string', show_when: { a: ['y'] } },
      },
    };
    expect(() => shouldShowField(config, { a: 'y', b: 'x' }, 'a', config.properties.a)).not.toThrow();
  });
});

describe('isFieldRequired', () => {
  it('always requires account_id and integration_config_name', () => {
    expect(isFieldRequired(lokiConfig, {}, 'account_id', {})).toBe(true);
    expect(isFieldRequired(lokiConfig, {}, 'integration_config_name', {})).toBe(true);
  });

  it('honours the base required array and required_when conditions', () => {
    expect(isFieldRequired(lokiConfig, {}, 'url', lokiConfig.properties.url)).toBe(true);
    expect(isFieldRequired(lokiConfig, { auth_type: 'none' }, 'username', lokiConfig.properties.username)).toBeFalsy();
    expect(isFieldRequired(lokiConfig, { auth_type: 'basic' }, 'username', lokiConfig.properties.username)).toBe(true);
  });
});

describe('getSortedFieldKeys', () => {
  it('returns visible fields sorted by priority, highest first', () => {
    expect(getSortedFieldKeys(lokiConfig, { auth_type: 'basic' })).toEqual(['url', 'auth_type', 'username', 'password']);
    expect(getSortedFieldKeys(lokiConfig, { auth_type: 'none' })).toEqual(['url', 'auth_type']);
  });
});

describe('buildProbeConfigValues', () => {
  // Datadog-shaped: two encrypted keys plus plain fields — the live-verified
  // Test Connection payload this module must keep producing.
  const ddConfig = {
    properties: {
      site: { type: 'string' },
      api_key: { type: 'string', is_encrypted: true },
      app_key: { type: 'string', is_encrypted: true },
      default_log_provider: { type: 'boolean' },
      tags: { type: 'array' },
    },
  };
  const stored = { api_key: 'ciphertext-api', app_key: 'ciphertext-app' };

  it('excludes account/name/mapping keys and stringifies booleans', () => {
    const out = buildProbeConfigValues(
      ddConfig,
      { account_id: ['a1'], integration_config_name: 'n', account_mapping: {}, site: 'api.us5.datadoghq.com', default_log_provider: false },
      stored
    );
    expect(out.map((v) => v.name)).toEqual(['site', 'default_log_provider']);
    expect(out.find((v) => v.name === 'default_log_provider')).toEqual({
      name: 'default_log_provider',
      value: 'false',
      is_encrypted: false,
    });
  });

  it('sends the stored ciphertext with is_encrypted=true for untouched masked secrets (omit-to-keep)', () => {
    const out = buildProbeConfigValues(ddConfig, { api_key: ENCRYPTED_MASK, app_key: ENCRYPTED_MASK }, stored);
    expect(out).toEqual([
      { name: 'api_key', value: 'ciphertext-api', is_encrypted: true },
      { name: 'app_key', value: 'ciphertext-app', is_encrypted: true },
    ]);
  });

  it('sends a retyped secret as plaintext with is_encrypted=false', () => {
    const out = buildProbeConfigValues(ddConfig, { api_key: 'fresh-key' }, stored);
    expect(out).toEqual([{ name: 'api_key', value: 'fresh-key', is_encrypted: false }]);
  });

  it('joins arrays (including option objects) and never emits undefined values', () => {
    const out = buildProbeConfigValues(ddConfig, { tags: [{ value: 'a' }, 'b'], site: undefined }, {});
    expect(out.find((v) => v.name === 'tags').value).toBe('a,b');
    expect(out.find((v) => v.name === 'site').value).toBe('');
  });
});

describe('joinConfigArray', () => {
  it('joins plain values and {value} option objects alike', () => {
    expect(joinConfigArray(['a', { value: 'b' }, 'c'])).toBe('a,b,c');
  });
});
