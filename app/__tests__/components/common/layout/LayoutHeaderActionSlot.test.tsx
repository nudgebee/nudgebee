import { render, screen } from '@testing-library/react';
import { LayoutHeaderActionSlot } from '@shared/layout/LayoutHeaderActionSlot';
import { renderSlot } from '@lib/slots';

jest.mock('@lib/slots', () => ({
  renderSlot: jest.fn(),
}));

const mockRenderSlot = renderSlot as jest.Mock;
const noop = () => {};

describe('LayoutHeaderActionSlot', () => {
  beforeEach(() => {
    mockRenderSlot.mockReset();
  });

  it('renders nothing when the slot is empty', () => {
    mockRenderSlot.mockReturnValue(null);
    const { container } = render(<LayoutHeaderActionSlot open={false} title='Test' onClose={noop} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders slot content when the slot is registered', () => {
    mockRenderSlot.mockReturnValue(<div data-testid='slot-content'>Action UI</div>);
    render(<LayoutHeaderActionSlot open={true} title='Test' onClose={noop} />);
    expect(screen.getByTestId('slot-content')).toBeInTheDocument();
  });

  it('calls renderSlot with slot name "LayoutHeaderAction"', () => {
    mockRenderSlot.mockReturnValue(null);
    render(<LayoutHeaderActionSlot open={false} title='Test' onClose={noop} />);
    expect(mockRenderSlot).toHaveBeenCalledWith('LayoutHeaderAction', expect.any(Object));
  });

  it('forwards open=true', () => {
    mockRenderSlot.mockReturnValue(null);
    render(<LayoutHeaderActionSlot open={true} title='Test' onClose={noop} />);
    expect(mockRenderSlot).toHaveBeenCalledWith('LayoutHeaderAction', expect.objectContaining({ open: true }));
  });

  it('forwards open=false', () => {
    mockRenderSlot.mockReturnValue(null);
    render(<LayoutHeaderActionSlot open={false} title='Test' onClose={noop} />);
    expect(mockRenderSlot).toHaveBeenCalledWith('LayoutHeaderAction', expect.objectContaining({ open: false }));
  });

  it('forwards title prop', () => {
    mockRenderSlot.mockReturnValue(null);
    render(<LayoutHeaderActionSlot open={false} title='My Panel' onClose={noop} />);
    expect(mockRenderSlot).toHaveBeenCalledWith('LayoutHeaderAction', expect.objectContaining({ title: 'My Panel' }));
  });

  it('forwards onClose callback', () => {
    mockRenderSlot.mockReturnValue(null);
    const onClose = jest.fn();
    render(<LayoutHeaderActionSlot open={false} title='Test' onClose={onClose} />);
    expect(mockRenderSlot).toHaveBeenCalledWith('LayoutHeaderAction', expect.objectContaining({ onClose }));
  });

  it('forwards optional buttonTitle when provided', () => {
    mockRenderSlot.mockReturnValue(null);
    render(<LayoutHeaderActionSlot open={false} title='Test' onClose={noop} buttonTitle='Save' />);
    expect(mockRenderSlot).toHaveBeenCalledWith('LayoutHeaderAction', expect.objectContaining({ buttonTitle: 'Save' }));
  });

  it('does not include buttonTitle when not provided', () => {
    mockRenderSlot.mockReturnValue(null);
    render(<LayoutHeaderActionSlot open={false} title='Test' onClose={noop} />);
    const calledProps = mockRenderSlot.mock.calls[0][1];
    expect(calledProps.buttonTitle).toBeUndefined();
  });
});
