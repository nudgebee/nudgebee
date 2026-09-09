const mockTenant: { id?: string } = { id: 'tenant-1' };
jest.mock('@lib/auth', () => ({ getCurrentTenant: (): { id?: string } => mockTenant }));

import {
  readNubiConversationPointer,
  writeNubiConversationPointer,
  clearNubiConversationPointer,
} from '@components/llm/utils/nubiConversationPointer';

describe('nubiConversationPointer', () => {
  beforeEach(() => {
    localStorage.clear();
    mockTenant.id = 'tenant-1';
  });

  it('holds one (account, session) pair under a tenant-scoped key', () => {
    writeNubiConversationPointer('acc-1', 'sess-1');
    expect(JSON.parse(localStorage.getItem('nubi_selected_conversation:tenant-1') as string)).toEqual({ accountId: 'acc-1', sessionId: 'sess-1' });
    expect(readNubiConversationPointer('acc-1')).toBe('sess-1');
  });

  it('a second write replaces the pair for that tenant (never accumulates)', () => {
    writeNubiConversationPointer('acc-1', 'sess-1');
    writeNubiConversationPointer('acc-2', 'sess-2');
    expect(localStorage.length).toBe(1);
    expect(readNubiConversationPointer('acc-1')).toBe('');
    expect(readNubiConversationPointer('acc-2')).toBe('sess-2');
  });

  it('does not honour the pair for a different account', () => {
    writeNubiConversationPointer('acc-1', 'sess-1');
    expect(readNubiConversationPointer('acc-2')).toBe('');
  });

  it('reads a different key after switching tenant', () => {
    writeNubiConversationPointer('acc-1', 'sess-1');
    mockTenant.id = 'tenant-2';
    expect(readNubiConversationPointer('acc-1')).toBe('');
    mockTenant.id = 'tenant-1';
    expect(readNubiConversationPointer('acc-1')).toBe('sess-1');
  });

  it('clear removes the current tenant key', () => {
    writeNubiConversationPointer('acc-1', 'sess-1');
    clearNubiConversationPointer();
    expect(localStorage.getItem('nubi_selected_conversation:tenant-1')).toBeNull();
  });

  it('is a no-op without an account id or session id', () => {
    writeNubiConversationPointer(undefined, 'sess-1');
    writeNubiConversationPointer('acc-1', undefined);
    expect(readNubiConversationPointer(undefined)).toBe('');
    expect(localStorage.length).toBe(0);
  });

  it('tolerates a corrupt entry', () => {
    localStorage.setItem('nubi_selected_conversation:tenant-1', '{not json');
    expect(readNubiConversationPointer('acc-1')).toBe('');
  });

  it('tolerates an entry that parses to null', () => {
    localStorage.setItem('nubi_selected_conversation:tenant-1', 'null');
    expect(readNubiConversationPointer('acc-1')).toBe('');
  });
});
