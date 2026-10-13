import { fireEvent, render, screen } from '@testing-library/react';
import CreateTokenDialog from '../CreateTokenDialog';

jest.mock('@api1/roles', () => ({ getTokenScopeCatalog: jest.fn().mockResolvedValue([]) }));

it('labels a cost limit in US dollars', () => {
  render(<CreateTokenDialog open defaultKind='gateway' onClose={jest.fn()} onCreate={jest.fn()} />);
  fireEvent.click(screen.getByRole('button', { name: 'Add limit' }));
  expect(screen.getByLabelText('Limit (USD)')).toBeInTheDocument();
  expect(screen.queryByLabelText('Value')).not.toBeInTheDocument();
});
