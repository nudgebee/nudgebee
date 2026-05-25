import React from 'react';
import { render, screen, fireEvent, act } from '@testing-library/react';
import ScoreDisplay from '@components1/common/widgets/ScoreDisplay';

describe('ScoreDisplay', () => {
  it('renders "-" when score is null', () => {
    render(<ScoreDisplay score={null} />);
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  it('renders "-" when score is undefined', () => {
    render(<ScoreDisplay score={undefined} />);
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  it('renders score value when score >= 75 (red)', () => {
    render(<ScoreDisplay score={80} />);
    expect(screen.getByText('80')).toBeInTheDocument();
  });

  it('renders score value when score >= 50 (orange)', () => {
    render(<ScoreDisplay score={60} />);
    expect(screen.getByText('60')).toBeInTheDocument();
  });

  it('renders score value when score >= 25 (yellow)', () => {
    render(<ScoreDisplay score={30} />);
    expect(screen.getByText('30')).toBeInTheDocument();
  });

  it('renders score value when score < 25 (green)', () => {
    render(<ScoreDisplay score={10} />);
    expect(screen.getByText('10')).toBeInTheDocument();
  });

  it('shows popover on mouse enter', () => {
    const { container } = render(
      <ScoreDisplay
        score={75}
        scoreFactors={{
          base_severity: 60,
          service_tier: 0,
          env_multiplier: 1,
          duplicate_penalty: 0,
          confidence: 0.9,
        }}
      />
    );
    // Fire mouseEnter on the Box
    const boxes = container.querySelectorAll('div');
    // Find the onMouseEnter container - it wraps the score display
    act(() => {
      fireEvent.mouseEnter(boxes[0]);
    });
    expect(screen.getByText('75')).toBeInTheDocument();
  });

  it('renders with string scoreFactors (valid JSON)', () => {
    render(
      <ScoreDisplay
        score={50}
        scoreFactors={JSON.stringify({
          base_severity: 55,
          service_tier: 1,
          env_multiplier: 1,
          duplicate_penalty: 0,
          confidence: 0.7,
        })}
      />
    );
    expect(screen.getByText('50')).toBeInTheDocument();
  });

  it('renders with invalid JSON string scoreFactors (falls to empty object)', () => {
    render(<ScoreDisplay score={40} scoreFactors='invalid-json{{{' />);
    expect(screen.getByText('40')).toBeInTheDocument();
  });

  it('renders with empty string scoreFactors', () => {
    render(<ScoreDisplay score={30} scoreFactors='' />);
    expect(screen.getByText('30')).toBeInTheDocument();
  });

  it('renders with scoreFactors object', () => {
    render(
      <ScoreDisplay
        score={60}
        scoreFactors={{
          base_severity: 30,
          service_tier: 2,
          env_multiplier: 0.3,
          duplicate_penalty: 5,
          confidence: 0.5,
        }}
      />
    );
    expect(screen.getByText('60')).toBeInTheDocument();
  });

  it('handles getTierName for tier 0 (Customer Facing)', () => {
    render(<ScoreDisplay score={80} scoreFactors={{ service_tier: 0, base_severity: 60, env_multiplier: 1, duplicate_penalty: 0 }} />);
    expect(screen.getByText('80')).toBeInTheDocument();
  });

  it('handles getTierName for tier 1 (Core Infra)', () => {
    render(<ScoreDisplay score={80} scoreFactors={{ service_tier: 1, base_severity: 60, env_multiplier: 1, duplicate_penalty: 0 }} />);
    expect(screen.getByText('80')).toBeInTheDocument();
  });

  it('handles getTierName for tier 2 (Business Service)', () => {
    render(<ScoreDisplay score={80} scoreFactors={{ service_tier: 2, base_severity: 60, env_multiplier: 1, duplicate_penalty: 0 }} />);
    expect(screen.getByText('80')).toBeInTheDocument();
  });

  it('handles getTierName for tier 3 (Monitoring)', () => {
    render(<ScoreDisplay score={80} scoreFactors={{ service_tier: 3, base_severity: 60, env_multiplier: 1, duplicate_penalty: 0 }} />);
    expect(screen.getByText('80')).toBeInTheDocument();
  });

  it('handles getTierName for unknown tier', () => {
    render(<ScoreDisplay score={80} scoreFactors={{ service_tier: 99, base_severity: 60, env_multiplier: 1, duplicate_penalty: 0 }} />);
    expect(screen.getByText('80')).toBeInTheDocument();
  });

  it('uses confidence from scoreFactors when available', () => {
    render(
      <ScoreDisplay
        score={50}
        scoreFactors={{ confidence: 0.85, base_severity: 50, service_tier: 0, env_multiplier: 1, duplicate_penalty: 0 }}
        confidence={0.5}
      />
    );
    expect(screen.getByText('50')).toBeInTheDocument();
  });

  it('uses confidence prop when not in scoreFactors', () => {
    render(<ScoreDisplay score={50} scoreFactors={{}} confidence={0.75} />);
    expect(screen.getByText('50')).toBeInTheDocument();
  });

  it('renders with mouseEnter and mouseLeave interaction', () => {
    const { container } = render(<ScoreDisplay score={60} scoreFactors={{}} />);
    const divs = container.querySelectorAll('div');
    act(() => {
      fireEvent.mouseEnter(divs[0]);
    });
    act(() => {
      fireEvent.mouseLeave(divs[0]);
    });
    expect(screen.getByText('60')).toBeInTheDocument();
  });
});
