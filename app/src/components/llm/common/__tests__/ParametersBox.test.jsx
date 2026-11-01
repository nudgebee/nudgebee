import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { ParametersBox } from '../ToolDetails';

describe('ParametersBox copy', () => {
  let writeText;
  beforeEach(() => {
    writeText = jest.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });
  });

  it('copies the formatted JSON the Query box shows', () => {
    const parameters = { type: 'preference', payload: { resource: 'checkout-service', flow: ['kubectl describe'] } };
    render(<ParametersBox parameters={JSON.stringify(parameters)} />);
    fireEvent.click(screen.getByRole('button', { name: 'Copy' }));
    expect(writeText).toHaveBeenCalledWith(JSON.stringify(parameters, null, 2));
  });

  it('copies the bare command for a command-style tool', () => {
    render(<ParametersBox parameters='{"command":"kubectl get pods -n shop"}' />);
    fireEvent.click(screen.getByRole('button', { name: 'Copy' }));
    expect(writeText).toHaveBeenCalledWith('kubectl get pods -n shop');
  });

  it('shows no copy button when there is no query', () => {
    const { container } = render(<ParametersBox parameters='{}' />);
    expect(container).toBeEmptyDOMElement();
  });
});
