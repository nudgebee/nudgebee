import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import SkillSourcesSection, { AddSkillSourceModal, SkillDetailsModal } from '@components/llm/SkillSourcesSection';
import apiKnowledgeBase from '@api1/knowledge-base';
import { toast } from '@ui/Toast';

jest.mock('@api1/knowledge-base', () => ({
  __esModule: true,
  default: {
    createSkillSource: jest.fn(),
    updateSkillSource: jest.fn(),
    syncSkillSource: jest.fn(),
    deleteSkillSource: jest.fn(),
  },
}));
jest.mock('@api1/user', () => ({ __esModule: true, default: { listAccounts: jest.fn(() => Promise.resolve([])) } }));
jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn() } }));
jest.mock('@ui/ThreeDotsMenu', () => ({
  __esModule: true,
  default: ({ menuItems, onMenuClick }) => (
    <div>
      {menuItems.map((item) => (
        <button key={item.value} type='button' disabled={item.disabled} onClick={() => onMenuClick(item)}>
          {`menu:${item.label}`}
        </button>
      ))}
    </div>
  ),
}));
jest.mock('@components/llm/MemoryTable', () => ({
  MemoryTable: ({ columns, rows }) => (
    <div>
      {rows.map((row) => (
        <div key={row.id} data-testid={`source-${row.repo || row.name}`}>
          {columns.map((c) => (
            <span key={c.key}>{c.render ? c.render(row) : row[c.key]}</span>
          ))}
        </div>
      ))}
    </div>
  ),
}));

const ok = (data) => ({ data, errors: [] });

beforeEach(() => jest.clearAllMocks());

describe('AddSkillSourceModal', () => {
  it('submits the repository, folder, branch and the organisation by default', async () => {
    apiKnowledgeBase.createSkillSource.mockResolvedValue(ok({ id: 'src-1', repo: 'acme/skills' }));
    const onCreated = jest.fn();
    const onClose = jest.fn();
    render(<AddSkillSourceModal open onClose={onClose} onCreated={onCreated} />);

    expect(screen.getByText('Every folder with a SKILL.md becomes a skill. Private repositories use your GitHub integration.')).toBeInTheDocument();
    fireEvent.change(screen.getByPlaceholderText('owner/name or https://github.com/owner/name'), {
      target: { value: ' https://github.com/acme/skills ' },
    });
    fireEvent.change(screen.getByPlaceholderText('skills'), { target: { value: 'skills' } });
    fireEvent.change(screen.getByPlaceholderText('default branch'), { target: { value: 'main' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add repository' }));

    await waitFor(() =>
      expect(apiKnowledgeBase.createSkillSource).toHaveBeenCalledWith({
        repo: 'https://github.com/acme/skills',
        folder: 'skills',
        branch: 'main',
        visibility: 'organisation',
        accountIds: [],
      })
    );
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith({ id: 'src-1', repo: 'acme/skills' }));
    expect(onClose).toHaveBeenCalled();
  });

  it('refuses selected accounts with no account picked', async () => {
    render(<AddSkillSourceModal open onClose={jest.fn()} />);
    fireEvent.change(screen.getByPlaceholderText('owner/name or https://github.com/owner/name'), { target: { value: 'acme/skills' } });
    fireEvent.click(screen.getByRole('radio', { name: 'Selected accounts' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add repository' }));

    expect(toast.error).toHaveBeenCalledWith('Pick at least one account to use its skills');
    expect(apiKnowledgeBase.createSkillSource).not.toHaveBeenCalled();
  });
});

describe('SkillSourcesSection', () => {
  const source = {
    id: 'src-1',
    repo: 'acme/skills',
    folder: '',
    branch: '',
    visibility: 'organisation',
    status: 'active',
    last_commit: 'abcdef1234567',
    last_synced_at: '2026-10-19T10:00:00Z',
    skill_count: 3,
    sync_report: { skipped: [{ source_path: 'skills/broken', reason: 'SKILL.md has no name' }] },
  };

  it('lists a source with its link, branch, short commit and skipped skills', () => {
    render(<SkillSourcesSection sources={[source]} />);
    const row = screen.getByTestId('source-acme/skills');

    expect(within(row).getByRole('link', { name: /acme\/skills/ })).toHaveAttribute('href', 'https://github.com/acme/skills');
    expect(within(row).getByText('Whole repository · default branch')).toBeInTheDocument();
    expect(within(row).getByText('abcdef1')).toBeInTheDocument();
    fireEvent.click(within(row).getByText('1 skipped'));
    expect(within(row).getByText('skills/broken')).toBeInTheDocument();
  });

  it('syncs now, reports the counts and refreshes', async () => {
    apiKnowledgeBase.syncSkillSource.mockResolvedValue(ok({ commit: 'fedcba987', found: 3, added: 1, updated: 2, archived: 0, skipped: [] }));
    const onChanged = jest.fn();
    render(<SkillSourcesSection sources={[source]} onChanged={onChanged} />);

    fireEvent.click(screen.getByText('menu:Sync now'));

    await waitFor(() => expect(apiKnowledgeBase.syncSkillSource).toHaveBeenCalledWith('src-1'));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    expect(toast.success).toHaveBeenCalledWith('Synced acme/skills at fedcba9: 3 found, 1 added, 2 updated, 0 archived');
  });

  it('confirms before deleting a source and its skills', async () => {
    apiKnowledgeBase.deleteSkillSource.mockResolvedValue(ok({ status: 'ok', id: 'src-1' }));
    render(<SkillSourcesSection sources={[source]} onChanged={jest.fn()} />);

    fireEvent.click(screen.getByText('menu:Delete'));
    expect(await screen.findByText(/Removes the repository and its 3 skills/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(apiKnowledgeBase.deleteSkillSource).toHaveBeenCalledWith('src-1'));
  });

  it('opens the skills a repository brought in, flags missing requirements and opens a skill', async () => {
    const skills = [
      {
        id: 'kb-2',
        name: 'beyla',
        description: 'Set up Beyla',
        source_id: 'src-1',
        skill_meta: { requirements: [{ kind: 'cli', name: 'docker', provided: false }] },
      },
      {
        id: 'kb-1',
        name: 'alloy',
        description: 'Configure Alloy',
        source_id: 'src-1',
        skill_meta: { requirements: [{ kind: 'note', name: 'Needs Grafana 11', provided: false }] },
      },
      { id: 'kb-9', name: 'other', source_id: 'src-2' },
    ];
    render(<SkillSourcesSection sources={[source]} skills={skills} renderSkillSwitch={(kb) => <span>{`switch:${kb.name}`}</span>} />);

    fireEvent.click(screen.getByTestId('skill-source-skills-btn'));
    const modal = await screen.findByTestId('source-skills-modal');
    const names = within(modal)
      .getAllByTestId('source-skill-open')
      .map((b) => b.textContent);
    expect(names).toEqual(['alloy', 'beyla']);
    expect(within(modal).getAllByTestId('source-skill-warning')).toHaveLength(1);
    expect(within(within(modal).getByTestId('source-beyla')).getByTestId('source-skill-warning')).toBeInTheDocument();
    expect(within(modal).getByText('Not imported (1)')).toBeInTheDocument();
    // Each skill carries the knowledge base row's own Use in answers switch.
    expect(within(within(modal).getByTestId('source-beyla')).getByText('switch:beyla')).toBeInTheDocument();
    expect(within(modal).queryByText('switch:other')).not.toBeInTheDocument();

    fireEvent.click(within(modal).getByText('beyla'));
    expect(await screen.findByText('Skill: beyla')).toBeInTheDocument();
  });

  // The count and the list come from the same skills: an archived one isn't
  // listed, and an account that uses fewer than the repository has says so.
  it('counts the skills it lists, not archived ones, and shows when the account uses fewer', async () => {
    const skills = [
      { id: 'kb-1', name: 'alloy', source_id: 'src-1', status: 'active' },
      { id: 'kb-2', name: 'gone', source_id: 'src-1', status: 'archived' },
    ];
    render(<SkillSourcesSection sources={[source]} skills={skills} />);

    const count = screen.getByTestId('skill-source-skills-btn');
    expect(count).toHaveTextContent('1 of 3');
    fireEvent.click(count);
    const modal = await screen.findByTestId('source-skills-modal');
    expect(
      within(modal)
        .getAllByTestId('source-skill-open')
        .map((b) => b.textContent)
    ).toEqual(['alloy']);
  });

  it('says none are used here instead of none synced when the account uses none', async () => {
    render(<SkillSourcesSection sources={[source]} skills={[{ id: 'kb-2', name: 'gone', source_id: 'src-1', status: 'archived' }]} />);
    fireEvent.click(screen.getByTestId('skill-source-skills-btn'));
    const modal = await screen.findByTestId('source-skills-modal');
    expect(within(modal).getByText('None of these skills are used by this account.')).toBeInTheDocument();
  });
});

describe('SkillDetailsModal', () => {
  it('links the repository, folder, commit, SKILL.md and each stored file at the synced commit', () => {
    const kb = {
      name: 'promql',
      skill_meta: {
        source_path: 'skills/promql',
        commit: 'abcdef1234567',
        files: [
          { path: 'references/patterns.md', size: 2048 },
          { path: 'assets/huge.bin', size: 2097152, skipped: 'larger than 1 MB' },
        ],
      },
    };
    render(<SkillDetailsModal open onClose={jest.fn()} knowledgeBase={kb} source={{ repo: 'acme/skills' }} />);

    const base = 'https://github.com/acme/skills';
    expect(screen.getByRole('link', { name: /acme\/skills/ })).toHaveAttribute('href', base);
    expect(screen.getByRole('link', { name: /skills\/promql/ })).toHaveAttribute('href', `${base}/tree/abcdef1234567/skills/promql`);
    expect(screen.getByRole('link', { name: /abcdef1/ })).toHaveAttribute('href', `${base}/commit/abcdef1234567`);
    expect(screen.getByRole('link', { name: /View on GitHub/ })).toHaveAttribute('href', `${base}/blob/abcdef1234567/skills/promql/SKILL.md`);
    expect(screen.getByRole('link', { name: /references\/patterns.md/ })).toHaveAttribute(
      'href',
      `${base}/blob/abcdef1234567/skills/promql/references/patterns.md`
    );
    expect(screen.getByText('Not stored: larger than 1 MB')).toBeInTheDocument();
  });
});
