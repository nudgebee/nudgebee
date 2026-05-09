import React from 'react';
import { render, screen } from '@testing-library/react';
import StatusBadge from '@components1/common/StatusBadge';

describe('StatusBadge', () => {
  describe('basic rendering', () => {
    it('renders the label text', () => {
      render(<StatusBadge label='Active' />);
      expect(screen.getByText('Active')).toBeInTheDocument();
    });

    it('renders without crashing with only label prop', () => {
      expect(() => render(<StatusBadge label='Test' />)).not.toThrow();
    });

    it('renders the correct label text for each variant', () => {
      const variants = ['success', 'error', 'warning', 'info', 'grey', 'purple'];
      variants.forEach((variant) => {
        render(<StatusBadge label={variant} variant={variant} />);
        expect(screen.getAllByText(variant).length).toBeGreaterThan(0);
      });
    });
  });

  describe('dot=false (default badge mode)', () => {
    it('renders badge mode by default (dot=false)', () => {
      render(<StatusBadge label='Running' />);
      expect(screen.getByText('Running')).toBeInTheDocument();
    });

    it('renders badge with success variant', () => {
      render(<StatusBadge label='Success' variant='success' />);
      expect(screen.getByText('Success')).toBeInTheDocument();
    });

    it('renders badge with error variant', () => {
      render(<StatusBadge label='Error' variant='error' />);
      expect(screen.getByText('Error')).toBeInTheDocument();
    });

    it('renders badge with warning variant', () => {
      render(<StatusBadge label='Warning' variant='warning' />);
      expect(screen.getByText('Warning')).toBeInTheDocument();
    });

    it('renders badge with info variant', () => {
      render(<StatusBadge label='Info' variant='info' />);
      expect(screen.getByText('Info')).toBeInTheDocument();
    });

    it('renders badge with grey variant (default)', () => {
      render(<StatusBadge label='Unknown' variant='grey' />);
      expect(screen.getByText('Unknown')).toBeInTheDocument();
    });

    it('renders badge with purple variant', () => {
      render(<StatusBadge label='Purple' variant='purple' />);
      expect(screen.getByText('Purple')).toBeInTheDocument();
    });
  });

  describe('dot=true mode', () => {
    it('renders in dot mode when dot=true', () => {
      render(<StatusBadge label='Online' dot={true} />);
      expect(screen.getByText('Online')).toBeInTheDocument();
    });

    it('renders dot element alongside label', () => {
      const { container } = render(<StatusBadge label='Online' dot={true} />);
      // In dot mode the wrapper has 2 children: the dot Box and the Typography
      const wrapper = container.firstChild;
      expect(wrapper.children.length).toBe(2);
    });

    it('does not render dot element in badge mode', () => {
      const { container } = render(<StatusBadge label='Online' dot={false} />);
      // In badge mode, the wrapper has 1 child: the Typography
      const wrapper = container.firstChild;
      expect(wrapper.children.length).toBe(1);
    });

    it('renders dot mode with success variant', () => {
      render(<StatusBadge label='Healthy' variant='success' dot={true} />);
      expect(screen.getByText('Healthy')).toBeInTheDocument();
    });

    it('renders dot mode with error variant', () => {
      render(<StatusBadge label='Failed' variant='error' dot={true} />);
      expect(screen.getByText('Failed')).toBeInTheDocument();
    });

    it('renders dot mode with all variants without crashing', () => {
      const variants = ['success', 'error', 'warning', 'info', 'grey', 'purple'];
      variants.forEach((variant) => {
        expect(() => render(<StatusBadge label='Label' variant={variant} dot={true} />)).not.toThrow();
      });
    });
  });

  describe('size variants', () => {
    it('renders with default medium size', () => {
      render(<StatusBadge label='Medium' />);
      expect(screen.getByText('Medium')).toBeInTheDocument();
    });

    it('renders with small size', () => {
      render(<StatusBadge label='Small' size='small' />);
      expect(screen.getByText('Small')).toBeInTheDocument();
    });

    it('renders with medium size explicitly', () => {
      render(<StatusBadge label='Medium' size='medium' />);
      expect(screen.getByText('Medium')).toBeInTheDocument();
    });

    it('renders dot mode with small size', () => {
      render(<StatusBadge label='Small Dot' size='small' dot={true} />);
      expect(screen.getByText('Small Dot')).toBeInTheDocument();
    });

    it('renders dot mode with medium size', () => {
      render(<StatusBadge label='Medium Dot' size='medium' dot={true} />);
      expect(screen.getByText('Medium Dot')).toBeInTheDocument();
    });
  });

  describe('combinations', () => {
    it('renders success + small without crashing', () => {
      expect(() => render(<StatusBadge label='Active' variant='success' size='small' />)).not.toThrow();
    });

    it('renders error + medium + dot without crashing', () => {
      expect(() => render(<StatusBadge label='Failed' variant='error' size='medium' dot={true} />)).not.toThrow();
    });

    it('renders warning + small + dot without crashing', () => {
      expect(() => render(<StatusBadge label='Caution' variant='warning' size='small' dot={true} />)).not.toThrow();
    });

    it('renders purple + medium + no dot without crashing', () => {
      expect(() => render(<StatusBadge label='Special' variant='purple' size='medium' dot={false} />)).not.toThrow();
    });
  });
});
