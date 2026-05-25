import { render, screen } from '@testing-library/react';
import KubernetesPrometheus from '@components1/k8s/details/KubernetesPrometheus';

jest.mock('@assets/check.svg', () => ({
  default: { src: 'check.svg' },
}));

describe('KubernetesPrometheus', () => {
  const baseProps = {
    accountId: '123',
    showDrilldown: true,
    chartView: true,
    showExtraOptions: true,
    showQueryBox: true,
    preparedEvidences: [],
    showDateTime: false,
    _queriesToExecute: [],
    dateTime: {
      startTime: Date.now() - 3600000,
      endTime: Date.now(),
    },
  };

  it('renders Builder, Code and AI toggle buttons', () => {
    render(<KubernetesPrometheus {...baseProps} />);

    const buttons = ['builder', 'code', 'ai'];

    buttons.forEach((btn) => {
      expect(screen.getByRole('button', { name: new RegExp(btn, 'i') })).toBeInTheDocument();
    });
  });
});
