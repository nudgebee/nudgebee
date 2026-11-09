import React from 'react';
import { render, screen } from '@testing-library/react';
import LLMDefaultsSummary, { needsLLMDefaultsConfirm } from '@components/llm/LLMDefaultsSummary';

const acct = (id, after, afterSource, before) => ({
  account_id: id,
  account_name: `acct-${id}`,
  after: after ? { id: after, name: after } : undefined,
  after_source: afterSource,
  before: before ? { id: before, name: before } : undefined,
});

describe('needsLLMDefaultsConfirm', () => {
  it('asks when the tenant default is replaced', () => {
    expect(needsLLMDefaultsConfirm({ replaces_tenant_default: { name: 'x' }, affected: [] })).toBe(true);
  });

  it('asks when a config becomes organisation-wide', () => {
    expect(needsLLMDefaultsConfirm({ becomes_tenant_wide: true, affected: [] })).toBe(true);
  });

  it('asks when accounts move to the platform LLM', () => {
    expect(needsLLMDefaultsConfirm({ env_configured: true, affected: [acct('a', null, 'platform')] })).toBe(true);
  });

  it('does not ask when nothing changes', () => {
    expect(needsLLMDefaultsConfirm({ env_configured: true, affected: [], replaced_account_defaults: [], blocked: [] })).toBe(false);
  });
});

describe('LLMDefaultsSummary', () => {
  it('names the accounts that would be left with no LLM', () => {
    render(<LLMDefaultsSummary action='disable' report={{ blocked: [acct('a', null, 'none')], affected: [acct('a', null, 'none')] }} />);
    const blocked = screen.getByTestId('llm-defaults-blocked');
    expect(blocked).toHaveTextContent("Can't disable this config yet");
    expect(blocked).toHaveTextContent('acct-a');
    expect(blocked).toHaveTextContent('Make another config the organisation default');
    expect(screen.queryByTestId('llm-defaults-moves')).toBeNull();
  });

  it('caps the account chips and counts the rest', () => {
    const many = Array.from({ length: 12 }, (_, i) => acct(`x${i}`, null, 'none'));
    render(<LLMDefaultsSummary report={{ blocked: many, affected: many }} />);
    expect(screen.getByTestId('llm-defaults-blocked')).toHaveTextContent('+2 more');
    expect(screen.queryByText('acct-x10')).toBeNull();
  });

  it('groups moving accounts by where they end up', () => {
    render(
      <LLMDefaultsSummary
        report={{
          affected: [acct('a', 'gemini', 'tenant_default'), acct('b', 'gemini', 'tenant_default'), acct('c', null, 'platform')],
          blocked: [],
        }}
      />
    );
    const moves = screen.getAllByTestId('llm-defaults-moves').map((el) => el.textContent);
    expect(moves).toEqual(['Will use geminiacct-aacct-b', 'Will use the LLM provided by Nudgebeeacct-c']);
  });

  it('shows only why a blocked change is blocked', () => {
    render(
      <LLMDefaultsSummary
        report={{ blocked: [acct('a', null, 'none')], affected: [acct('a', null, 'none')], replaces_tenant_default: { name: 'old' } }}
      />
    );
    expect(screen.getByTestId('llm-defaults-blocked')).toHaveTextContent("This change can't be made yet");
    expect(screen.queryByTestId('llm-defaults-replaces-tenant')).toBeNull();
  });

  it('says which tenant default and account defaults are replaced', () => {
    render(
      <LLMDefaultsSummary
        report={{
          replaces_tenant_default: { name: 'old-default' },
          replaced_account_defaults: [acct('a', 'new', 'account_default', 'old-own')],
          affected: [],
          blocked: [],
        }}
      />
    );
    expect(screen.getByTestId('llm-defaults-replaces-tenant')).toHaveTextContent('replaces old-default as the organisation default');
    expect(screen.getByTestId('llm-defaults-replaces-account')).toHaveTextContent('acct-aold-own → new');
  });
});
