import { fireEvent, render, screen } from '@testing-library/react';
import CreateTokenDialog from '../CreateTokenDialog';

jest.mock('@api1/roles', () => ({ getTokenScopeCatalog: jest.fn().mockResolvedValue([]) }));

it('labels a cost limit in US dollars', () => {
  render(<CreateTokenDialog open defaultKind='gateway' onClose={jest.fn()} onCreate={jest.fn()} />);
  fireEvent.click(screen.getByRole('button', { name: 'Add limit' }));
  expect(screen.getByLabelText('Limit (USD)')).toBeInTheDocument();
  expect(screen.queryByLabelText('Value')).not.toBeInTheDocument();
});

it('sends a warn level as a number and omits a blank one', async () => {
  const onCreate = jest.fn().mockResolvedValue(true);
  render(<CreateTokenDialog open defaultKind='gateway' onClose={jest.fn()} onCreate={onCreate} />);
  fireEvent.change(screen.getByLabelText(/Name/), { target: { value: 'ci' } });
  fireEvent.click(screen.getByRole('button', { name: 'Add limit' }));
  fireEvent.click(screen.getByRole('button', { name: 'Add limit' }));
  const limits = screen.getAllByLabelText('Limit (USD)');
  fireEvent.change(limits[0], { target: { value: '50' } });
  fireEvent.change(limits[1], { target: { value: '10' } });
  fireEvent.change(screen.getAllByLabelText('Warn at (%)')[0], { target: { value: '80' } });
  fireEvent.click(screen.getByRole('button', { name: /Create/ }));
  await screen.findByRole('button', { name: /Create/ });
  const sent = onCreate.mock.calls[0][0].limits;
  expect(sent[0]).toEqual(expect.objectContaining({ value: 50, soft_pct: 80 }));
  expect(sent[1]).not.toHaveProperty('soft_pct');
});

it('blocks create while a warn level is out of range', () => {
  render(<CreateTokenDialog open defaultKind='gateway' onClose={jest.fn()} onCreate={jest.fn()} />);
  fireEvent.change(screen.getByLabelText(/Name/), { target: { value: 'ci' } });
  fireEvent.click(screen.getByRole('button', { name: 'Add limit' }));
  fireEvent.change(screen.getByLabelText('Warn at (%)'), { target: { value: '100' } });
  expect(screen.getByRole('button', { name: /Create/ })).toBeDisabled();
});
