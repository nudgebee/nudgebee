import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { Form } from '@shared/forms/Form';

describe('Form (root)', () => {
  it('renders children', () => {
    render(<Form>Form content</Form>);
    expect(screen.getByText('Form content')).toBeInTheDocument();
  });

  it('renders as form element when onSubmit is provided', () => {
    const { container } = render(<Form onSubmit={jest.fn()}>content</Form>);
    expect(container.querySelector('form')).toBeInTheDocument();
  });

  it('calls onSubmit when form is submitted', () => {
    const onSubmit = jest.fn((e) => e.preventDefault());
    const { container } = render(<Form onSubmit={onSubmit}>content</Form>);
    fireEvent.submit(container.querySelector('form')!);
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it('renders as div when no onSubmit', () => {
    const { container } = render(<Form>content</Form>);
    expect(container.querySelector('form')).toBeNull();
    expect(container.querySelector('div')).toBeInTheDocument();
  });
});

describe('Form.Section', () => {
  it('renders children', () => {
    render(<Form.Section>Section content</Form.Section>);
    expect(screen.getByText('Section content')).toBeInTheDocument();
  });

  it('renders section title', () => {
    render(<Form.Section title='Account Details'>content</Form.Section>);
    expect(screen.getByText('Account Details')).toBeInTheDocument();
  });

  it('renders section description', () => {
    render(<Form.Section description='Manage your account settings'>content</Form.Section>);
    expect(screen.getByText('Manage your account settings')).toBeInTheDocument();
  });
});

describe('Form.Field', () => {
  it('renders label', () => {
    render(
      <Form.Field label='Email'>
        <input />
      </Form.Field>
    );
    expect(screen.getByText('Email')).toBeInTheDocument();
  });

  it('renders required asterisk', () => {
    render(
      <Form.Field label='Name' required>
        <input />
      </Form.Field>
    );
    expect(screen.getByText('*')).toBeInTheDocument();
  });

  it('renders error message with role=alert', () => {
    render(
      <Form.Field label='Field' error='This field is required'>
        <input />
      </Form.Field>
    );
    expect(screen.getByText('This field is required')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toBeInTheDocument();
  });

  it('renders helperText', () => {
    render(
      <Form.Field label='Field' helperText='Helper text'>
        <input />
      </Form.Field>
    );
    expect(screen.getByText('Helper text')).toBeInTheDocument();
  });

  it('renders description', () => {
    render(
      <Form.Field label='Field' description='Enter your email address'>
        <input />
      </Form.Field>
    );
    expect(screen.getByText('Enter your email address')).toBeInTheDocument();
  });

  it('renders badge', () => {
    render(
      <Form.Field label='Field' badge='Optional'>
        <input />
      </Form.Field>
    );
    expect(screen.getByText('Optional')).toBeInTheDocument();
  });
});

describe('Form.Row', () => {
  it('renders children side by side', () => {
    render(
      <Form.Row>
        <span>First</span>
        <span>Last</span>
      </Form.Row>
    );
    expect(screen.getByText('First')).toBeInTheDocument();
    expect(screen.getByText('Last')).toBeInTheDocument();
  });
});

describe('Form.Actions', () => {
  it('renders action buttons', () => {
    render(
      <Form.Actions>
        <button>Cancel</button>
        <button>Save</button>
      </Form.Actions>
    );
    expect(screen.getByText('Cancel')).toBeInTheDocument();
    expect(screen.getByText('Save')).toBeInTheDocument();
  });
});
