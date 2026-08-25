import React from 'react';
import { render, screen } from '@testing-library/react';
import { SignInProviderExtraSlot } from '@shared/auth/SignInProviderExtraSlot';
import { renderSlot } from '@lib/slots';

jest.mock('@lib/slots', () => ({
  renderSlot: jest.fn(),
}));

const mockRenderSlot = renderSlot as jest.Mock;

describe('SignInProviderExtraSlot', () => {
  beforeEach(() => {
    mockRenderSlot.mockReset();
  });

  it('renders nothing when the slot is empty', () => {
    mockRenderSlot.mockReturnValue(null);
    const { container } = render(<SignInProviderExtraSlot variant='v1' hasOtherProviders={false} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders slot content when the slot is registered', () => {
    mockRenderSlot.mockReturnValue(<div data-testid='slot-content'>Extra provider UI</div>);
    render(<SignInProviderExtraSlot variant='v1' hasOtherProviders={false} />);
    expect(screen.getByTestId('slot-content')).toBeInTheDocument();
    expect(screen.getByText('Extra provider UI')).toBeInTheDocument();
  });

  it('calls renderSlot with the slot name "SignInProviderExtra"', () => {
    mockRenderSlot.mockReturnValue(null);
    render(<SignInProviderExtraSlot variant='v1' hasOtherProviders={false} />);
    expect(mockRenderSlot).toHaveBeenCalledWith('SignInProviderExtra', expect.any(Object));
  });

  it('forwards variant="v1" to renderSlot', () => {
    mockRenderSlot.mockReturnValue(null);
    render(<SignInProviderExtraSlot variant='v1' hasOtherProviders={false} />);
    expect(mockRenderSlot).toHaveBeenCalledWith('SignInProviderExtra', expect.objectContaining({ variant: 'v1' }));
  });

  it('forwards variant="v2" to renderSlot', () => {
    mockRenderSlot.mockReturnValue(null);
    render(<SignInProviderExtraSlot variant='v2' hasOtherProviders={true} />);
    expect(mockRenderSlot).toHaveBeenCalledWith('SignInProviderExtra', expect.objectContaining({ variant: 'v2' }));
  });

  it('forwards hasOtherProviders=true to renderSlot', () => {
    mockRenderSlot.mockReturnValue(null);
    render(<SignInProviderExtraSlot variant='v1' hasOtherProviders={true} />);
    expect(mockRenderSlot).toHaveBeenCalledWith('SignInProviderExtra', expect.objectContaining({ hasOtherProviders: true }));
  });

  it('forwards hasOtherProviders=false to renderSlot', () => {
    mockRenderSlot.mockReturnValue(null);
    render(<SignInProviderExtraSlot variant='v2' hasOtherProviders={false} />);
    expect(mockRenderSlot).toHaveBeenCalledWith('SignInProviderExtra', expect.objectContaining({ hasOtherProviders: false }));
  });
});
