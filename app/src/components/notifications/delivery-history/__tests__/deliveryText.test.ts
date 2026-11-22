import { destinationLabel, messageLink, reasonWords, routedBy, statusLine, typeLabel } from '../deliveryText';
import type { Delivery } from '@api1/notification/deliveries';

const row = (fields: Partial<Delivery>): Delivery => ({ id: 'd1', created_at: '2026-10-23T10:42:07Z', ...fields });

describe('message links', () => {
  it('links a Slack post through its workspace URL, and a reply into its thread', () => {
    const post = row({ platform: 'slack', status: 'sent', destination: 'C1', message_id: '1700000000.000200', team_url: 'https://acme.slack.com/' });

    expect(messageLink(post)?.href).toBe('https://acme.slack.com/archives/C1/p1700000000000200');
    expect(messageLink({ ...post, thread_id: '1700000000.000100' })?.href).toBe(
      'https://acme.slack.com/archives/C1/p1700000000000200?thread_ts=1700000000.000100&cid=C1'
    );
  });

  it('opens the channel when the workspace URL is not saved yet', () => {
    const post = row({ platform: 'slack', status: 'sent', destination: 'C1', message_id: '1.2', team_id: 'T1' });

    expect(messageLink(post)).toEqual({ href: 'https://slack.com/app_redirect?team=T1&channel=C1', label: 'Open the channel in Slack' });
  });

  it('links a Teams card by channel, message and team', () => {
    const card = row({ platform: 'ms_teams', status: 'sent', destination: '19:ops@thread.tacv2', message_id: '1800', team_id: 'group-1' });

    expect(messageLink(card)?.href).toBe('https://teams.microsoft.com/l/message/19%3Aops%40thread.tacv2/1800?groupId=group-1');
  });

  it('has no link for a failed send or a platform without one', () => {
    expect(messageLink(row({ platform: 'slack', status: 'failed', destination: 'C1', message_id: '1.2', team_id: 'T1' }))).toBeNull();
    expect(messageLink(row({ platform: 'discord', status: 'sent', destination: 'D1', message_id: '9' }))).toBeNull();
    expect(messageLink(row({ platform: 'email', status: 'sent', destination: 'a@x.io', message_id: 'm' }))).toBeNull();
  });
});

describe('reason words', () => {
  it('says what went wrong and the fix, with the brand', () => {
    const failed = row({ platform: 'slack', status: 'failed', reason: 'not_in_channel', destination: 'C1', destination_name: 'alerts' });

    expect(reasonWords(failed, 'Acme Ops')).toEqual({ says: 'Acme Ops was removed from #alerts.', fix: 'Re-invite it.' });
  });

  it('names the app when branding has not resolved', () => {
    const failed = row({ platform: 'slack', status: 'failed', reason: 'not_in_channel', destination_name: 'alerts' });

    expect(reasonWords(failed, '').says).toBe('The app was removed from #alerts.');
  });

  it("uses the platform's own words where they differ", () => {
    const failed = row({ platform: 'ms_teams', status: 'failed', reason: 'not_in_channel', destination_name: 'Incidents' });

    expect(reasonWords(failed, 'Acme')).toEqual({ says: 'Acme was removed from this team.', fix: 'Add the app to the team again.' });
  });

  it('says how many attempts a temporary failure got', () => {
    const failed = row({ platform: 'ms_teams', status: 'failed', reason: 'platform_unavailable', attempt: 4 });

    expect(reasonWords(failed, 'Acme').says).toBe("Microsoft Teams didn't respond to 4 attempts.");
    expect(reasonWords({ ...failed, attempt: 1 }, 'Acme').says).toBe("Microsoft Teams didn't respond.");
  });

  it('names the rule that stopped a send', () => {
    const stopped = row({ status: 'not_sent', reason: 'suppressed', routed_by: JSON.stringify([{ id: 'r1', name: 'Quiet hours' }]) });

    expect(reasonWords(stopped, 'Acme')).toEqual({ says: "Rule Quiet hours says Don't send.", fix: 'Edit Quiet hours if this should go out.' });
  });
});

describe('status line', () => {
  it('reads as the last attempt', () => {
    expect(statusLine(row({ status: 'sent', attempt: 3 }), 'Acme')).toEqual({ label: 'Sent', tone: 'success', detail: 'Sent on attempt 3.' });
    expect(statusLine(row({ status: 'sent', attempt: 1 }), 'Acme')).toEqual({ label: 'Sent', tone: 'success', detail: undefined });
    expect(statusLine(row({ status: 'not_sent', reason: 'held_for_summary' }), 'Acme')).toEqual({
      label: 'Not sent',
      tone: 'neutral',
      detail: 'Held for the hourly summary.',
    });
  });
});

describe('labels', () => {
  it('reads routed_by as rules, default, requested or nothing', () => {
    expect(routedBy(row({ routed_by: JSON.stringify([{ id: 'r1', name: 'Prod critical' }]) }))).toEqual({
      kind: 'rules',
      rules: [{ id: 'r1', name: 'Prod critical' }],
    });
    expect(routedBy(row({ routed_by: '"default"' })).kind).toBe('default');
    expect(routedBy(row({ routed_by: '"requested"' })).kind).toBe('requested');
    expect(routedBy(row({ routed_by: null })).kind).toBe('none');
  });

  it('names types and destinations', () => {
    expect(typeLabel('grouped_slo_alert')).toBe('Hourly summary');
    expect(typeLabel('nubi_message')).toBe('Sent from Nubi');
    expect(typeLabel('email:daily_recap')).toBe('Email');
    expect(destinationLabel({ platform: 'slack', destination: 'C1', destination_name: 'alerts' })).toBe('#alerts');
    expect(destinationLabel({ platform: 'ms_teams', destination: '19:x', destination_name: 'Incidents' })).toBe('Incidents');
    expect(destinationLabel({ platform: 'slack', destination: 'C1', destination_name: null })).toBe('C1');
  });
});
