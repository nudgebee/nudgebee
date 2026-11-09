import React from 'react';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import '@testing-library/jest-dom';
import TenantTokenList from '@shared/settings/TenantTokenList';

jest.mock('@utils/colors');
jest.mock('@ui/Button', () => ({
  __esModule: true,
  Button: ({ children, onClick }) => (
    <button onClick={onClick} data-testid={`btn-${children}`}>
      {children}
    </button>
  ),
}));
jest.mock('@ui/EmptyState', () => ({ __esModule: true, EmptyState: ({ title }) => <div>{title}</div> }));
jest.mock('@shared/tables/CustomTable', () => ({
  __esModule: true,
  default: ({ tableData, onPageChange, pageNumber = 1, rowsPerPage, totalRows }) => (
    <>
      <table>
        <tbody>
          {tableData.map((row, i) => (
            <tr key={i}>
              {row.map((cell, j) => (
                <td key={j}>{cell.component || cell.text}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      {onPageChange && (
        <div>
          <span data-testid='table-paging'>{`page ${pageNumber} · ${rowsPerPage} rows · ${totalRows} total`}</span>
          <button data-testid='table-prev' onClick={() => onPageChange(pageNumber - 1, rowsPerPage)} />
          <button data-testid='table-next' onClick={() => onPageChange(pageNumber + 1, rowsPerPage)} />
          <button data-testid='table-rows-100' onClick={() => onPageChange(1, 100)} />
        </div>
      )}
    </>
  ),
}));

const base = { status: 'active', created_at: '2026-09-01T00:00:00Z', accessed_at: null, expires_at: null, limits: [], scopes: null };
const tokens = [
  {
    ...base,
    id: 'k1',
    name: 'ci',
    kind: 'gateway',
    owner_name: 'Asha',
    token_suffix: 'a1b2',
    last_model: 'gpt-4o-mini',
    usage: { cost_usd: 0.42, requests: 3 },
  },
  { ...base, id: 'k2', name: 'old', kind: 'platform', owner_name: 'Ravi', token_suffix: '', last_model: '' },
];

describe('TenantTokenList', () => {
  it('shows owner, suffix and last model, and "—" where a key has none', () => {
    render(<TenantTokenList tokens={tokens} total={2} page={0} pageSize={50} onPageChange={jest.fn()} canRevoke onRevoke={jest.fn()} />);
    const ci = screen.getByText('ci').closest('tr');
    expect(within(ci).getByText('Asha')).toBeInTheDocument();
    expect(within(ci).getByText('…a1b2')).toBeInTheDocument();
    expect(within(ci).getByText('gpt-4o-mini')).toBeInTheDocument();
    const old = screen.getByText('old').closest('tr');
    expect(within(old).getAllByText('—').length).toBeGreaterThanOrEqual(2);
    expect(within(old).queryByText(/undefined/)).not.toBeInTheDocument();
  });

  it('revoke passes the key, and read-only admins get no action', async () => {
    const onRevoke = jest.fn();
    const { rerender } = render(
      <TenantTokenList tokens={tokens} total={2} page={0} pageSize={50} onPageChange={jest.fn()} canRevoke onRevoke={onRevoke} />
    );
    await userEvent.click(within(screen.getByText('ci').closest('tr')).getByTestId('btn-Revoke'));
    expect(onRevoke).toHaveBeenCalledWith(tokens[0]);
    rerender(<TenantTokenList tokens={tokens} total={2} page={0} pageSize={50} onPageChange={jest.fn()} canRevoke={false} onRevoke={onRevoke} />);
    expect(screen.queryByTestId('btn-Revoke')).not.toBeInTheDocument();
  });

  it('Manage access passes the key; read-only admins get none', async () => {
    const onManageAccess = jest.fn();
    const props = { tokens, total: 2, page: 0, pageSize: 50, onPageChange: jest.fn(), onRevoke: jest.fn(), onManageAccess };
    const { rerender } = render(<TenantTokenList {...props} canRevoke />);
    await userEvent.click(within(screen.getByText('ci').closest('tr')).getByTestId('btn-Manage access'));
    expect(onManageAccess).toHaveBeenCalledWith(tokens[0]);
    rerender(<TenantTokenList {...props} canRevoke={false} />);
    expect(screen.queryByTestId('btn-Manage access')).not.toBeInTheDocument();
  });

  it('says when filters match nothing', () => {
    render(<TenantTokenList tokens={[]} total={0} page={0} pageSize={50} onPageChange={jest.fn()} filtered canRevoke={false} />);
    expect(screen.getByText('No API keys match these filters')).toBeInTheDocument();
  });

  it('pages through the table pager, 0-based outside and 1-based inside', async () => {
    const onPageChange = jest.fn();
    render(<TenantTokenList tokens={tokens} total={120} page={1} pageSize={50} onPageChange={onPageChange} canRevoke={false} onRevoke={jest.fn()} />);
    expect(screen.getByTestId('table-paging')).toHaveTextContent('page 2 · 50 rows · 120 total');
    await userEvent.click(screen.getByTestId('table-next'));
    expect(onPageChange).toHaveBeenCalledWith(2, 50);
    await userEvent.click(screen.getByTestId('table-prev'));
    expect(onPageChange).toHaveBeenCalledWith(0, 50);
    await userEvent.click(screen.getByTestId('table-rows-100'));
    expect(onPageChange).toHaveBeenLastCalledWith(0, 100);
  });
});
