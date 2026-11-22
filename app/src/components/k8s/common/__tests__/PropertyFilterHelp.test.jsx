// The KG Label / Attribute ⓘ must describe the filter the Knowledge Graph
// actually offers: one operator (=) and keys that exist on KG nodes (#40705).
import { render, screen } from '@testing-library/react';
import { PropertyFilterHelp } from '../LogQueryBuilderAutocomplete';

it.each([
  ['Label', 'pick a label', 'environment = production'],
  ['Attribute', 'pick an attribute', 'namespace = default'],
])('%s help names only the = operator and a Knowledge Graph example', (heading, pick, example) => {
  const { container } = render(<PropertyFilterHelp heading={heading} />);

  expect(container).toHaveTextContent(pick);
  expect(container).toHaveTextContent('Choose the = operator (the only one the Knowledge Graph supports)');
  expect(container).not.toHaveTextContent('!=');
  expect(container).not.toHaveTextContent('LIKE');
  expect(screen.getByText(example)).toBeInTheDocument();
  expect(container).not.toHaveTextContent('service.name = my-api');
});

it('describes labels as key/value tags, not node types', () => {
  const { container } = render(<PropertyFilterHelp heading='Label' />);
  expect(container).toHaveTextContent('Labels are the key/value tags on a node');
  expect(container).not.toHaveTextContent('classify nodes by type');
});
