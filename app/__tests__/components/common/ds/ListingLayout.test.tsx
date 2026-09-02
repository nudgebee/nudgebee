import React from 'react';
import { render, screen } from '@testing-library/react';
import { ListingLayout } from '@ui/ListingLayout';

describe('ListingLayout', () => {
  it('renders children', () => {
    render(<ListingLayout>Main content</ListingLayout>);
    expect(screen.getByText('Main content')).toBeInTheDocument();
  });

  it('renders with custom id', () => {
    render(<ListingLayout id='my-layout'>content</ListingLayout>);
    expect(document.getElementById('my-layout')).toBeInTheDocument();
  });
});

describe('ListingLayout.Toolbar', () => {
  it('renders toolbar with title', () => {
    render(
      <ListingLayout>
        <ListingLayout.Toolbar title='Recommendations'>filters here</ListingLayout.Toolbar>
      </ListingLayout>
    );
    expect(screen.getByText('Recommendations')).toBeInTheDocument();
  });

  it('renders children in toolbar', () => {
    render(
      <ListingLayout>
        <ListingLayout.Toolbar>
          <button>Filter</button>
        </ListingLayout.Toolbar>
      </ListingLayout>
    );
    expect(screen.getByText('Filter')).toBeInTheDocument();
  });

  it('renders actions in toolbar', () => {
    render(
      <ListingLayout>
        <ListingLayout.Toolbar actions={<button>Export</button>}>filters</ListingLayout.Toolbar>
      </ListingLayout>
    );
    expect(screen.getByText('Export')).toBeInTheDocument();
  });
});

describe('ListingLayout.Body', () => {
  it('renders body content', () => {
    render(
      <ListingLayout>
        <ListingLayout.Body>Table goes here</ListingLayout.Body>
      </ListingLayout>
    );
    expect(screen.getByText('Table goes here')).toBeInTheDocument();
  });
});

describe('ListingLayout.Footer', () => {
  it('renders footer content', () => {
    render(
      <ListingLayout>
        <ListingLayout.Footer>Pagination</ListingLayout.Footer>
      </ListingLayout>
    );
    expect(screen.getByText('Pagination')).toBeInTheDocument();
  });
});

describe('ListingLayout.ToolbarSpacer', () => {
  it('renders spacer without crashing', () => {
    const { container } = render(
      <ListingLayout>
        <ListingLayout.Toolbar>
          <ListingLayout.ToolbarSpacer />
        </ListingLayout.Toolbar>
      </ListingLayout>
    );
    expect(container.firstChild).toBeInTheDocument();
  });
});
