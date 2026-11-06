// @vitest-environment jsdom
//
// #0643: mounts CampaignEditor.svelte and drives the "Add upcoming events"
// button: the block is appended after the body (never replacing it, even text
// typed while the request is in flight), the draft is saved, the fallback
// notice is shown, and the button is disabled when the campaign cannot be
// edited. '../../lib/api' and '../../lib/events' are mocked, as
// Dashboard.behavior.test.ts does.
import { render, cleanup, waitFor, screen, fireEvent } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Campaign, UpcomingEventsResponse } from '../../lib/types';

const getCampaign = vi.fn<(id: number) => Promise<Campaign>>();
const updateCampaign = vi.fn((_id: number, fields: { body_md?: string }) =>
  Promise.resolve({ ...baseCampaign('draft'), body_md: fields.body_md ?? '' }),
);
const addUpcomingEvents = vi.fn<(id: number, month: string) => Promise<UpcomingEventsResponse>>();

vi.mock('../../lib/api', () => ({
  getCampaign: (id: number) => getCampaign(id),
  updateCampaign: (id: number, f: { body_md?: string }) => updateCampaign(id, f),
  addUpcomingEvents: (id: number, m: string) => addUpcomingEvents(id, m),
  getCampaignAudience: vi.fn(() => Promise.resolve({ mode: 'all', interest_ids: [], count: 0, sample: [], warnings: [] })),
  getCampaignPreflight: vi.fn(() =>
    Promise.resolve({ ok: true, unmet: [], summary: { subject: 's', from: 'f', recipients: 0 } }),
  ),
  previewCampaign: vi.fn(() => Promise.resolve({ subject: 's', html: '', text: '' })),
  testSendCampaign: vi.fn(),
  sendCampaign: vi.fn(),
  cancelCampaign: vi.fn(),
  resumeCampaign: vi.fn(),
  deleteCampaign: vi.fn(),
  listInterests: vi.fn(() => Promise.resolve({ interests: [] })),
  ApiError: class ApiError extends Error {},
}));

vi.mock('../../lib/events', () => ({
  subscribeEvent: vi.fn(() => () => {}),
}));

const { default: CampaignEditor } = await import('./CampaignEditor.svelte');

afterEach(() => {
  cleanup();
  getCampaign.mockReset();
  updateCampaign.mockClear();
  addUpcomingEvents.mockReset();
});

function baseCampaign(status: string): Campaign {
  return {
    id: 5,
    name: 'Newsletter',
    subject: 'News',
    body_md: 'Hello friends',
    status,
    audience_mode: 'all',
    interest_ids: [],
    created_at: '2026-11-01T00:00:00Z',
    updated_at: '2026-11-01T00:00:00Z',
    slug: 'monthly-news',
    archive_status: 'pending',
  };
}

const block = '## Upcoming events in November 2026\n\n- **[Intro](https://go.example/u/k1)**: Sunday, Nov 15 at 1:00 PM\n';

function response(over: Partial<UpcomingEventsResponse> = {}): UpcomingEventsResponse {
  return {
    month: '2026-11',
    label: 'November 2026',
    markdown: block,
    events: [{ workshop_id: 1, title: 'Intro', url: 'https://go.example/u/k1', link: 'short', reused: false, reason: '' }],
    created: 1,
    reused: 0,
    fallback: 0,
    ...over,
  };
}

function mount() {
  return render(CampaignEditor, {
    props: { campaignId: 5, onBack: () => {}, onGoToSettings: () => {}, onGoToAudit: () => {} },
  });
}

async function body(): Promise<HTMLTextAreaElement> {
  return (await screen.findByLabelText('Body (Markdown)')) as HTMLTextAreaElement;
}

describe('CampaignEditor upcoming events (#0643)', () => {
  it('appends the events block to the typed body and saves', async () => {
    getCampaign.mockResolvedValue(baseCampaign('draft'));
    addUpcomingEvents.mockResolvedValue(response());
    mount();
    const ta = await body();
    await waitFor(() => expect(ta.value).toBe('Hello friends'));
    await fireEvent.click(screen.getByRole('button', { name: 'Add upcoming events' }));
    await waitFor(() => expect(ta.value).toBe('Hello friends\n\n' + block));
    expect(addUpcomingEvents).toHaveBeenCalledWith(5, expect.stringMatching(/^\d{4}-\d{2}$/));
    await waitFor(() => expect(updateCampaign).toHaveBeenCalled());
    expect(updateCampaign.mock.calls.at(-1)![1].body_md).toBe('Hello friends\n\n' + block);
    expect(await screen.findByText(/Added 1 event \(1 new link, 0 reused\)\./)).toBeTruthy();
  });

  it('keeps text typed while the request is in flight', async () => {
    getCampaign.mockResolvedValue(baseCampaign('draft'));
    let resolve!: (r: UpcomingEventsResponse) => void;
    addUpcomingEvents.mockReturnValue(new Promise((r) => (resolve = r)));
    mount();
    const ta = await body();
    await waitFor(() => expect(ta.value).toBe('Hello friends'));
    await fireEvent.click(screen.getByRole('button', { name: 'Add upcoming events' }));
    await fireEvent.input(ta, { target: { value: 'Hello friends\nTyped meanwhile' } });
    resolve(response());
    await waitFor(() => expect(ta.value).toBe('Hello friends\nTyped meanwhile\n\n' + block));
  });

  it('shows the fallback notice', async () => {
    getCampaign.mockResolvedValue(baseCampaign('draft'));
    addUpcomingEvents.mockResolvedValue(
      response({
        events: [{ workshop_id: 1, title: 'Intro', url: 'https://site/events/intro', link: 'plain', reused: false, reason: 'no_campaign' }],
        created: 0,
        fallback: 1,
      }),
    );
    mount();
    await body();
    await waitFor(() => expect((screen.getByRole('button', { name: 'Add upcoming events' }) as HTMLButtonElement).disabled).toBe(false));
    await fireEvent.click(screen.getByRole('button', { name: 'Add upcoming events' }));
    expect(await screen.findByText(/1 event uses the plain event link: Intro \(no Outreach campaign yet\)\./)).toBeTruthy();
  });

  it('disables the button for a sent campaign', async () => {
    getCampaign.mockResolvedValue({ ...baseCampaign('sent'), archive_status: 'published' });
    mount();
    await body();
    const btn = screen.getByRole('button', { name: 'Add upcoming events' }) as HTMLButtonElement;
    await waitFor(() => expect(btn.disabled).toBe(true));
    expect(addUpcomingEvents).not.toHaveBeenCalled();
  });
});
