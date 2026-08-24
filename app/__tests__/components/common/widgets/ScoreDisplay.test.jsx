import { render, screen, fireEvent } from '@testing-library/react';
import ScoreDisplay from '@shared/widgets/ScoreDisplay';

jest.mock('@utils/colors');

describe('ScoreDisplay', () => {
  describe('null/undefined score', () => {
    it('renders "-" when score is null', () => {
      render(<ScoreDisplay score={null} />);
      expect(screen.getByText('-')).toBeInTheDocument();
    });

    it('renders "-" when score is undefined', () => {
      render(<ScoreDisplay score={undefined} />);
      expect(screen.getByText('-')).toBeInTheDocument();
    });

    it('does not render score-display container when score is null', () => {
      render(<ScoreDisplay score={null} />);
      expect(screen.queryByTestId('score-display')).not.toBeInTheDocument();
    });
  });

  describe('with score', () => {
    it('renders the score value', () => {
      render(<ScoreDisplay score={80} />);
      expect(screen.getByText('80')).toBeInTheDocument();
    });

    it('renders score-display container', () => {
      render(<ScoreDisplay score={50} />);
      expect(screen.getByTestId('score-display')).toBeInTheDocument();
    });

    it('renders score 0', () => {
      render(<ScoreDisplay score={0} />);
      expect(screen.getByText('0')).toBeInTheDocument();
    });
  });

  describe('popover on hover', () => {
    it('shows "Priority Score" popover on mouse enter', () => {
      render(<ScoreDisplay score={60} scoreFactors={{ base_severity: 60, service_tier: 0, env_multiplier: 1 }} />);
      fireEvent.mouseEnter(screen.getByTestId('score-display'));
      expect(screen.getByText('Priority Score')).toBeInTheDocument();
    });

    it('calls mouseLeave handler without throwing', () => {
      render(<ScoreDisplay score={60} scoreFactors={{ base_severity: 60, service_tier: 0, env_multiplier: 1 }} />);
      fireEvent.mouseEnter(screen.getByTestId('score-display'));
      expect(() => fireEvent.mouseLeave(screen.getByTestId('score-display'))).not.toThrow();
    });
  });

  describe('scoreFactors as string', () => {
    it('parses JSON string scoreFactors', () => {
      const factors = JSON.stringify({ base_severity: 60, service_tier: 1, env_multiplier: 1 });
      render(<ScoreDisplay score={60} scoreFactors={factors} />);
      fireEvent.mouseEnter(screen.getByTestId('score-display'));
      expect(screen.getByText('Core Infra')).toBeInTheDocument();
    });

    it('handles invalid JSON string gracefully', () => {
      render(<ScoreDisplay score={40} scoreFactors='invalid json' />);
      expect(screen.getByTestId('score-display')).toBeInTheDocument();
    });
  });

  describe('service tier names', () => {
    it('shows "Customer Facing" for tier 0', () => {
      render(<ScoreDisplay score={50} scoreFactors={{ base_severity: 50, service_tier: 0, env_multiplier: 1 }} />);
      fireEvent.mouseEnter(screen.getByTestId('score-display'));
      expect(screen.getByText('Customer Facing')).toBeInTheDocument();
    });

    it('shows "Core Infra" for tier 1', () => {
      render(<ScoreDisplay score={50} scoreFactors={{ base_severity: 50, service_tier: 1, env_multiplier: 1 }} />);
      fireEvent.mouseEnter(screen.getByTestId('score-display'));
      expect(screen.getByText('Core Infra')).toBeInTheDocument();
    });

    it('shows "Business Service" for tier 2', () => {
      render(<ScoreDisplay score={50} scoreFactors={{ base_severity: 50, service_tier: 2, env_multiplier: 1 }} />);
      fireEvent.mouseEnter(screen.getByTestId('score-display'));
      expect(screen.getByText('Business Service')).toBeInTheDocument();
    });

    it('shows "Monitoring" for tier 3', () => {
      render(<ScoreDisplay score={50} scoreFactors={{ base_severity: 50, service_tier: 3, env_multiplier: 1 }} />);
      fireEvent.mouseEnter(screen.getByTestId('score-display'));
      expect(screen.getByText('Monitoring')).toBeInTheDocument();
    });
  });
});
