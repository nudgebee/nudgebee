import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import AutoSuggestTextarea from '@components/k8s/common/TextAreaV2';

it('restores an edited message even when its text equals the previous submitted value', async () => {
  const props = {
    onDraftSubmitted: jest.fn(),
    value: 'same message',
    suggestionsAt: [],
    placeholder: 'Ask',
    maxRows: 8,
    maxLength: 1000,
    onKeyDown: jest.fn(),
    fontSize: '14px',
    fontWeight: '400',
    onClick: jest.fn(),
    buttonProperties: { show: true, enable: true, onClick: jest.fn().mockResolvedValue(true), onClickStop: jest.fn() },
  };
  const { rerender } = render(<AutoSuggestTextarea {...props} />);
  expect(screen.getByPlaceholderText('Ask')).toHaveValue('same message');
  fireEvent.click(screen.getByRole('button', { name: 'Send' }));
  await waitFor(() => expect(screen.getByPlaceholderText('Ask')).toHaveValue(''));
  rerender(<AutoSuggestTextarea {...props} imageDraft={{ id: 'edited-message', text: 'same message', images: [] }} />);
  expect(screen.getByPlaceholderText('Ask')).toHaveValue('same message');
  fireEvent.click(screen.getByRole('button', { name: 'Send' }));
  await waitFor(() => expect(props.onDraftSubmitted).toHaveBeenCalledWith('edited-message'));
});

it.each([false, true])('keeps Stop available without clearing a draft (chat screen: %s)', (chatScreen) => {
  const onClickStop = jest.fn();
  render(
    <AutoSuggestTextarea
      value='keep this draft'
      suggestionsAt={[]}
      placeholder='Ask'
      maxRows={8}
      maxLength={1000}
      onKeyDown={jest.fn()}
      fontSize='14px'
      fontWeight='400'
      onClick={jest.fn()}
      chatScreen={chatScreen}
      isFollowUp
      allowStop
      buttonProperties={{ show: true, enable: true, onClick: jest.fn(), onClickStop }}
    />
  );
  expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Stop' }));
  expect(onClickStop).toHaveBeenCalledTimes(1);
  expect(screen.getByPlaceholderText('Ask')).toHaveValue('keep this draft');
});

it('reports a populated draft so restoring another message can preserve it', () => {
  const onDraftChange = jest.fn();
  render(
    <AutoSuggestTextarea
      value=''
      suggestionsAt={[]}
      placeholder='Ask'
      maxRows={8}
      maxLength={1000}
      onKeyDown={jest.fn()}
      fontSize='14px'
      fontWeight='400'
      onClick={jest.fn()}
      onDraftChange={onDraftChange}
      buttonProperties={{ show: true, enable: true, onClick: jest.fn() }}
    />
  );
  expect(onDraftChange).toHaveBeenLastCalledWith(false);
  fireEvent.change(screen.getByPlaceholderText('Ask'), { target: { value: 'new draft' } });
  expect(onDraftChange).toHaveBeenLastCalledWith(true);
});

describe('/ skill suggestions', () => {
  const skills = [
    { name: 'DB failover', description: 'Fail the primary over' },
    { name: 'disk-cleanup', description: 'Free node disk' },
  ];
  const renderWithSkills = () =>
    render(
      <AutoSuggestTextarea
        value=''
        suggestionsAt={[]}
        skillSuggestions={skills}
        placeholder='Ask'
        maxRows={8}
        maxLength={1000}
        onKeyDown={jest.fn()}
        fontSize='14px'
        fontWeight='400'
        onClick={jest.fn()}
        buttonProperties={{ show: true, enable: true, onClick: jest.fn() }}
      />
    );

  it('suggests skills by command-name prefix and inserts /<command-name>', () => {
    renderWithSkills();
    const input = screen.getByPlaceholderText('Ask');
    fireEvent.change(input, { target: { value: '/db-' } });
    expect(screen.getByText('DB failover')).toBeInTheDocument();
    expect(screen.getByText('Fail the primary over')).toBeInTheDocument();
    expect(screen.queryByText('disk-cleanup')).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('DB failover'));
    expect(input).toHaveValue('/db-failover ');
  });

  it('shows nothing when no skill matches or once the command is followed by a space', () => {
    renderWithSkills();
    const input = screen.getByPlaceholderText('Ask');
    fireEvent.change(input, { target: { value: '/var/log' } });
    expect(screen.queryByText('DB failover')).not.toBeInTheDocument();
    expect(screen.queryByText('disk-cleanup')).not.toBeInTheDocument();
    fireEvent.change(input, { target: { value: '/d' } });
    expect(screen.getByText('disk-cleanup')).toBeInTheDocument();
    fireEvent.change(input, { target: { value: '/disk-cleanup now' } });
    expect(screen.queryByText('disk-cleanup')).not.toBeInTheDocument();
    fireEvent.change(input, { target: { value: 'check /var/log/d' } });
    expect(screen.queryByText('disk-cleanup')).not.toBeInTheDocument();
  });

  it('suggests a skill typed in the middle of the message and replaces only that word', () => {
    renderWithSkills();
    const input = screen.getByPlaceholderText('Ask') as HTMLTextAreaElement;
    fireEvent.change(input, { target: { value: 'node is full, use /disk' } });
    expect(screen.getByText('disk-cleanup')).toBeInTheDocument();
    fireEvent.click(screen.getByText('disk-cleanup'));
    expect(input).toHaveValue('node is full, use /disk-cleanup ');
  });

  it('keeps a line break after the replaced word', () => {
    renderWithSkills();
    const input = screen.getByPlaceholderText('Ask') as HTMLTextAreaElement;
    // Caret right after "/dis", before the line break.
    fireEvent.change(input, { target: { value: 'use /dis\nnext line', selectionStart: 8, selectionEnd: 8 } });
    fireEvent.click(screen.getByText('disk-cleanup'));
    expect(input).toHaveValue('use /disk-cleanup \nnext line');
  });

  it('suggests skills after an @agent mention', () => {
    render(
      <AutoSuggestTextarea
        value=''
        suggestionsAt={[{ name: 'k8s_debug', display_name: 'k8s_debug' }]}
        skillSuggestions={skills}
        placeholder='Ask'
        maxRows={8}
        maxLength={1000}
        onKeyDown={jest.fn()}
        fontSize='14px'
        fontWeight='400'
        onClick={jest.fn()}
        buttonProperties={{ show: true, enable: true, onClick: jest.fn() }}
      />
    );
    fireEvent.change(screen.getByPlaceholderText('Ask'), { target: { value: '@k8s_debug /dis' } });
    expect(screen.getByText('disk-cleanup')).toBeInTheDocument();
  });
});
