import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import FilterDropdown from '@ui/FilterDropdown';

const options = ['Apple', 'Banana', 'Cherry', 'Date'];

// FilterDropdown uses `onSelect` (not onChange), and search only appears when options.length > 8
const manyOptions = ['Apple', 'Banana', 'Cherry', 'Date', 'Elderberry', 'Fig', 'Grape', 'Honeydew', 'Kiwi'];

describe('FilterDropdown', () => {
  it('renders the trigger button with label', () => {
    render(<FilterDropdown options={options} value={null} onSelect={jest.fn()} label='Fruit' />);
    expect(screen.getByText('Fruit')).toBeInTheDocument();
  });

  it('opens dropdown on trigger click', () => {
    render(<FilterDropdown options={options} value={null} onSelect={jest.fn()} label='Fruit' />);
    fireEvent.click(screen.getByText('Fruit'));
    expect(screen.getByText('Apple')).toBeInTheDocument();
  });

  it('calls onSelect when option selected', () => {
    const onSelect = jest.fn();
    render(<FilterDropdown options={options} value={null} onSelect={onSelect} label='Fruit' />);
    fireEvent.click(screen.getByText('Fruit'));
    fireEvent.click(screen.getByText('Banana'));
    expect(onSelect).toHaveBeenCalled();
  });

  it('renders selected value in trigger', () => {
    render(<FilterDropdown options={options} value='Apple' onSelect={jest.fn()} label='Fruit' />);
    expect(screen.getByText('Apple')).toBeInTheDocument();
  });

  it('leads the trigger with the selected option icon only when asked', () => {
    const iconOptions = [{ label: 'Apple', value: 'a', icon: <span data-testid='opt-icon'>i</span> }];

    const { unmount } = render(<FilterDropdown options={iconOptions} value={iconOptions[0]} onSelect={jest.fn()} label='Fruit' />);
    expect(screen.queryByTestId('opt-icon')).not.toBeInTheDocument();
    unmount();

    render(<FilterDropdown options={iconOptions} value={iconOptions[0]} onSelect={jest.fn()} label='Fruit' showSelectedIcon />);
    expect(screen.getByTestId('opt-icon')).toBeInTheDocument();
  });

  it('renders an icon-only trigger named by its label, and still opens the list', () => {
    render(<FilterDropdown options={options} value='Apple' onSelect={jest.fn()} label='Fruit' icon={<span data-testid='trigger-icon' />} />);
    const trigger = screen.getByRole('button', { name: 'Fruit' });
    expect(trigger).toContainElement(screen.getByTestId('trigger-icon'));
    // Neither the label nor the selection is drawn as text on the trigger.
    expect(trigger).not.toHaveTextContent(/Fruit|Apple/);
    fireEvent.click(trigger);
    expect(screen.getByText('Banana')).toBeInTheDocument();
  });

  it('offers a clear row in place of the (x) an icon-only trigger has no room for', () => {
    const onSelect = jest.fn();
    const { rerender } = render(<FilterDropdown options={options} value={null} onSelect={onSelect} label='Fruit' icon={<span />} />);
    fireEvent.click(screen.getByRole('button', { name: 'Fruit' }));
    expect(screen.queryByText('Clear selection')).not.toBeInTheDocument();

    rerender(<FilterDropdown options={options} value='Apple' onSelect={onSelect} label='Fruit' icon={<span />} />);
    fireEvent.click(screen.getByText('Clear selection'));
    expect(onSelect).toHaveBeenCalledWith(expect.anything(), null);
  });

  it('keeps the caret instead of a clear control when not clearable', () => {
    const { container } = render(<FilterDropdown options={options} value='Apple' onSelect={jest.fn()} label='Fruit' clearable={false} />);
    // The clear affordance is the only <line>-based svg in the trigger.
    expect(container.querySelectorAll('svg line')).toHaveLength(0);
  });

  it('renders multiple selected values', () => {
    render(<FilterDropdown options={options} multiple value={['Apple', 'Cherry']} onSelect={jest.fn()} label='Fruits' />);
    expect(screen.getByText(/Apple|2/)).toBeInTheDocument();
  });

  it('renders with searchable input when options > 8', () => {
    render(<FilterDropdown options={manyOptions} value={null} onSelect={jest.fn()} label='F' />);
    fireEvent.click(screen.getByText('F'));
    expect(screen.getByPlaceholderText(/search/i)).toBeInTheDocument();
  });

  it('renders with searchable input when freeSolo', () => {
    render(<FilterDropdown options={options} value={null} onSelect={jest.fn()} label='F' freeSolo />);
    fireEvent.click(screen.getByText('F'));
    expect(screen.getByPlaceholderText(/search|type/i)).toBeInTheDocument();
  });

  it('renders object options with label/value shape', () => {
    const objOptions = [
      { label: 'Production', value: 'prod' },
      { label: 'Staging', value: 'stg' },
    ];
    render(<FilterDropdown options={objOptions} value={null} onSelect={jest.fn()} label='Env' />);
    fireEvent.click(screen.getByText('Env'));
    expect(screen.getByText('Production')).toBeInTheDocument();
  });

  it('renders an option row with all four slots (icon, badge, label, type chip)', () => {
    // Shape the KG Node filter builds: the icon names the provider, the badge the
    // resource kind, the right chip the location — and the label keeps the rest.
    const rowOptions = [
      {
        label: 'otel-deployment-collector-collector',
        value: 'n1',
        badge: 'Config Map',
        type: 'k8s-prod \u00b7 otel',
        icon: (
          <span role='img' aria-label='k8s'>
            i
          </span>
        ),
      },
    ];
    render(<FilterDropdown options={rowOptions} value={[]} onSelect={jest.fn()} label='Node' multiple />);
    fireEvent.click(screen.getByText('Node'));
    expect(screen.getByText('otel-deployment-collector-collector')).toBeInTheDocument();
    expect(screen.getByText('Config Map')).toBeInTheDocument();
    expect(screen.getByText('k8s-prod \u00b7 otel')).toBeInTheDocument();
    // An element `icon` is returned verbatim by SafeIcon, so its own aria-label is
    // what names it \u2014 SafeIcon's `alt` never reaches it.
    expect(screen.getByLabelText('k8s')).toBeInTheDocument();
  });

  it('shows a long type chip in full instead of cutting it at a character count', () => {
    // #40702: same-named k8s rows differ only by namespace, which sits after the
    // cluster in the chip. A fixed 15-char cut rendered every row as
    // "iteration-test ...", so the rows looked identical without hovering.
    const rowOptions = [
      { label: 'benchmark-server', value: 'a', badge: 'Service', type: 'iteration-test \u00b7 nudgebee', typeTextTransform: 'none' },
      { label: 'benchmark-server', value: 'b', badge: 'Service', type: 'iteration-test \u00b7 benchmark', typeTextTransform: 'none' },
    ];
    render(<FilterDropdown options={rowOptions} value={[]} onSelect={jest.fn()} label='Node' multiple />);
    fireEvent.click(screen.getByText('Node'));
    expect(screen.getByText('iteration-test \u00b7 nudgebee')).toBeInTheDocument();
    expect(screen.getByText('iteration-test \u00b7 benchmark')).toBeInTheDocument();
    expect(screen.queryByText(/\.\.\.$/)).not.toBeInTheDocument();
  });

  it('puts the full type chip text in a tooltip when the chip is clipped', async () => {
    // jsdom has no layout, so fake an element that overflows its box.
    const scroll = jest.spyOn(HTMLElement.prototype, 'scrollWidth', 'get').mockReturnValue(300);
    const client = jest.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(100);
    try {
      const longType = 'a-very-long-cluster-name-for-testing \u00b7 a-very-long-namespace';
      render(<FilterDropdown options={[{ label: 'svc', value: 'a', type: longType }]} value={[]} onSelect={jest.fn()} label='Node' multiple />);
      fireEvent.click(screen.getByText('Node'));
      fireEvent.mouseOver(screen.getByText(longType));
      expect(await screen.findByRole('tooltip')).toHaveTextContent(longType);
    } finally {
      scroll.mockRestore();
      client.mockRestore();
    }
  });

  it('matches search against opt.searchText, not just the visible label', () => {
    // Short labels, but the full key lives in searchText (KG node-row shape).
    const nodeOptions = Array.from({ length: 9 }, (_, i) => ({
      label: `node-${i}`,
      value: `id-${i}`,
      searchText: `k8s:k8s-dev::Workload:${i === 0 ? 'argocd' : 'nudgebee'}:node-${i}`,
    }));
    render(<FilterDropdown options={nodeOptions} value={null} onSelect={jest.fn()} label='Node' />);
    fireEvent.click(screen.getByText('Node'));
    // Typing a namespace (present only in searchText) filters to the matching row.
    fireEvent.change(screen.getByPlaceholderText(/search/i), { target: { value: 'argocd' } });
    expect(screen.getByText('node-0')).toBeInTheDocument();
    expect(screen.queryByText('node-1')).not.toBeInTheDocument();
  });
  it('lists a freeSolo value that is not in options so it can be removed on its own', () => {
    // Shape of the workflow email "To" field: the generator saved a template
    // recipient, the user then picked a real one from the list.
    const onSelect = jest.fn();
    const template = '{{ Inputs.recipient_email }}';
    render(<FilterDropdown multiple freeSolo options={['qa@nudgebee.com']} value={[template, 'qa@nudgebee.com']} onSelect={onSelect} label='To' />);
    // Trigger only (limitTag 1) before the panel opens.
    expect(screen.getAllByText(template)).toHaveLength(1);

    fireEvent.click(screen.getByText('To'));
    const rows = screen.getAllByText(template);
    expect(rows).toHaveLength(2);

    // Unchecking the template row drops it and keeps the real recipient.
    fireEvent.click(rows[1]);
    expect(onSelect).toHaveBeenCalledWith(expect.anything(), ['qa@nudgebee.com']);
  });

  it('does not list values outside options when freeSolo is off', () => {
    render(<FilterDropdown multiple options={['Apple']} value={['Ghost']} onSelect={jest.fn()} label='Fruit' />);
    fireEvent.click(screen.getByText('Fruit'));
    expect(screen.getAllByText('Ghost')).toHaveLength(1);
  });

  // #40704: the Knowledge Graph highlights matching nodes from the search text itself,
  // so the text must reach the caller while typing — not only when an option is picked.
  it('reports search text as the user types and keeps a controlled search across close', () => {
    const Harness = ({ onSearchChange }: { onSearchChange: (q: string) => void }) => {
      const [q, setQ] = React.useState('');
      return (
        <FilterDropdown
          options={options}
          onSelect={jest.fn()}
          label='Fruit'
          searchValue={q}
          onSearchChange={(next) => {
            setQ(next);
            onSearchChange(next);
          }}
        />
      );
    };
    const onSearchChange = jest.fn();
    render(<Harness onSearchChange={onSearchChange} />);
    fireEvent.click(screen.getByText('Fruit'));
    // Shown even with fewer than 8 options, since the caller acts on the text.
    fireEvent.change(screen.getByPlaceholderText(/search/i), { target: { value: 'an' } });
    expect(onSearchChange).toHaveBeenLastCalledWith('an');
    expect(screen.getByText('Banana')).toBeInTheDocument();
    expect(screen.queryByText('Cherry')).not.toBeInTheDocument();

    fireEvent.keyDown(screen.getByPlaceholderText(/search/i), { key: 'Escape' });
    fireEvent.click(screen.getByText('Fruit'));
    expect(screen.getByPlaceholderText(/search/i)).toHaveValue('an');
    expect(onSearchChange).toHaveBeenCalledTimes(1);

    fireEvent.change(screen.getByPlaceholderText(/search/i), { target: { value: '' } });
    expect(onSearchChange).toHaveBeenLastCalledWith('');
  });
});
