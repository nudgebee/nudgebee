import { render, screen, fireEvent } from '@testing-library/react';
import JsonTreeView from '@shared/viewers/JsonTreeView';

Object.assign(navigator, {
  clipboard: { writeText: jest.fn().mockResolvedValue(undefined) },
});

jest.mock('@utils/colors');

describe('JsonTreeView', () => {
  describe('null / empty data', () => {
    it('renders null data without crashing', () => {
      const { container } = render(<JsonTreeView data={null} />);
      expect(container.firstChild).toBeInTheDocument();
    });

    it('renders undefined data without crashing', () => {
      const { container } = render(<JsonTreeView data={undefined} />);
      expect(container.firstChild).toBeInTheDocument();
    });

    it('renders empty object', () => {
      const { container } = render(<JsonTreeView data={{}} />);
      expect(container.firstChild).toBeInTheDocument();
    });
  });

  describe('primitive values', () => {
    it('renders a string value', () => {
      render(<JsonTreeView data={{ name: 'Alice' }} />);
      expect(screen.getByText(/"name"/i)).toBeInTheDocument();
    });

    it('renders a number value', () => {
      render(<JsonTreeView data={{ count: 42 }} />);
      expect(screen.getByText('42')).toBeInTheDocument();
    });

    it('renders a boolean value', () => {
      render(<JsonTreeView data={{ active: true }} />);
      expect(screen.getByText('true')).toBeInTheDocument();
    });

    it('renders null value', () => {
      render(<JsonTreeView data={{ empty: null }} />);
      expect(screen.getByText('null')).toBeInTheDocument();
    });
  });

  describe('nested objects', () => {
    it('renders nested object keys', () => {
      render(<JsonTreeView data={{ outer: { inner: 'value' } }} defaultExpanded={2} />);
      expect(screen.getByText(/"outer"/)).toBeInTheDocument();
    });

    it('renders arrays', () => {
      render(<JsonTreeView data={{ items: [1, 2, 3] }} defaultExpanded={2} />);
      expect(screen.getByText(/"items"/)).toBeInTheDocument();
    });
  });

  describe('copy functionality', () => {
    it('renders copy button when showCopy is true', () => {
      render(<JsonTreeView data={{ key: 'val' }} showCopy={true} />);
      expect(screen.getByLabelText('Copy JSON')).toBeInTheDocument();
    });

    it('does not show copy button in bare mode by default', () => {
      render(<JsonTreeView data={{ key: 'val' }} bare={true} />);
      expect(screen.queryByLabelText('Copy JSON')).not.toBeInTheDocument();
    });

    it('copies JSON when copy button is clicked', async () => {
      render(<JsonTreeView data={{ key: 'val' }} showCopy={true} />);
      fireEvent.click(screen.getByLabelText('Copy JSON'));
      expect(navigator.clipboard.writeText).toHaveBeenCalledWith(JSON.stringify({ key: 'val' }, null, 2));
    });
  });

  describe('template tooltips', () => {
    it('renders without errors when templatePrefix is provided', () => {
      render(<JsonTreeView data={{ field: 'value' }} templatePrefix='Tasks.output' defaultExpanded={2} />);
      expect(screen.getByText(/"field"/)).toBeInTheDocument();
    });
  });
});
