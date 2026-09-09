import { render, screen } from '@testing-library/react';
import ModelAliasList, { parseModels, serializeModels } from './ModelAliasList';

describe('ModelAliasList', () => {
  it('round-trips multiple provider-independent model mappings', () => {
    const rows = parseModels('gemini-fast=gemini-2.5-flash, gemini-smart=gemini-2.5-pro');

    expect(rows).toEqual([
      { name: 'gemini-fast', served: 'gemini-2.5-flash' },
      { name: 'gemini-smart', served: 'gemini-2.5-pro' },
    ]);
    expect(serializeModels(rows)).toBe('gemini-fast=gemini-2.5-flash, gemini-smart=gemini-2.5-pro');
  });

  it('renders the structured mapping editor', () => {
    render(<ModelAliasList value='vertex-fast=gemini-2.5-flash' onChange={jest.fn()} />);

    expect(screen.getByText('Client model name')).toBeInTheDocument();
    expect(screen.getByText('Served model (sent to provider)')).toBeInTheDocument();
    expect(screen.getByTestId('model-alias-name-0')).toBeInTheDocument();
    expect(screen.getByTestId('model-alias-served-0')).toBeInTheDocument();
  });
});
