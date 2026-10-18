// @vitest-environment jsdom
//
// #0581: mounts OutreachPostDialog.svelte under jsdom. jsdom is not a browser
// (AGENTS.md §1): focus and Escape are DOM events here, no browser was run.
import { render, cleanup, screen, fireEvent, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { OutreachChannel, OutreachPost } from '../../lib/types';
import OutreachPostDialog from './OutreachPostDialog.svelte';

afterEach(cleanup);

const channel: OutreachChannel = {
  name: 'bluesky', label: 'Bluesky', utm_source: 'bluesky', utm_medium: 'social', char_limit: 300, automatic: true, configured: true, account: '@club.test',
};
const item = { channel, post: { id: 1, body: 'Come solder with us' } as OutreachPost };

function mount(over: Record<string, unknown> = {}) {
  const onConfirm = vi.fn();
  const onClose = vi.fn();
  render(OutreachPostDialog, { mode: 'post', items: [item], inFlight: false, errorMessage: null, onConfirm, onClose, ...over });
  return { onConfirm, onClose };
}

describe('OutreachPostDialog (#0581)', () => {
  it('shows the post text, the account and that it cannot be unsent', () => {
    mount();
    const dlg = screen.getByRole('dialog');
    expect(dlg.textContent).toContain('Come solder with us');
    expect(dlg.textContent).toContain('Posts publicly as @club.test on Bluesky');
    expect(dlg.textContent).toContain('cannot be unsent');
  });

  it('takes focus on open and closes on Escape', async () => {
    const { onClose } = mount();
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('dialog')));
    await fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    expect(onClose).toHaveBeenCalled();
  });

  it('does not close on Escape while a request is in flight', async () => {
    const { onClose } = mount({ inFlight: true });
    await fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    expect(onClose).not.toHaveBeenCalled();
  });

  it('Mark posted rejects an ftp URL and passes a valid one', async () => {
    const { onConfirm } = mount({ mode: 'markPosted' });
    const input = screen.getByLabelText(/Link to the post/);
    const confirm = screen.getAllByRole('button').find((b) => b.textContent?.trim() === 'Mark posted') as HTMLButtonElement;
    await fireEvent.input(input, { target: { value: 'ftp://x.example/1' } });
    expect(confirm.disabled).toBe(true);
    await fireEvent.input(input, { target: { value: 'https://bsky.app/p/1' } });
    expect(confirm.disabled).toBe(false);
    await fireEvent.click(confirm);
    expect(onConfirm).toHaveBeenCalledWith('https://bsky.app/p/1');
  });
});
