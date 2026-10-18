// @vitest-environment jsdom
//
// Mounts ServerSettings.svelte under jsdom with '../../lib/api' mocked (same
// pattern as OutreachConnections.behavior.test.ts). jsdom is not a browser:
// this proves the form wiring and the notes, not layout.
import { render, cleanup, waitFor, screen, fireEvent } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ServerSettings } from '../../lib/types';

const getServerSettings = vi.fn<() => Promise<ServerSettings>>();
const updateServerSettings = vi.fn();
vi.mock('../../lib/api', () => ({
  getServerSettings: () => getServerSettings(),
  updateServerSettings: (...a: unknown[]) => updateServerSettings(...a),
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

const { default: ServerSettingsView } = await import('./ServerSettings.svelte');
const { ApiError } = await import('../../lib/api');

afterEach(() => {
  cleanup();
  getServerSettings.mockReset();
  updateServerSettings.mockReset();
});

type Over = { pending?: boolean; mediaSaved?: boolean; mediaError?: string };

function fixture(over: Over = {}): ServerSettings {
  const f = (key: string, env_var: string, source: 'database' | 'environment' | 'default' | 'unset', value: string, restart: boolean, pending = false) => ({
    key,
    env_var,
    source,
    value,
    default: '',
    restart_required: restart,
    pending_restart: pending,
  });
  return {
    groups: [
      { name: 'media', error: over.mediaError ?? '', fields: [f('media_dir', 'MEDIA_DIR', over.mediaSaved ? 'database' : 'default', '/var/www/media', false)] },
      {
        name: 'email',
        error: '',
        fields: [
          f('email_from', 'EMAIL_FROM', 'environment', 'from@example.org', true, over.pending),
          f('email_reply_to', 'EMAIL_REPLY_TO', 'unset', '', true),
          f('email_list_domain', 'EMAIL_LIST_DOMAIN', 'environment', 'lists.example.org', true),
        ],
      },
      {
        name: 'ses',
        error: '',
        fields: [f('ses_sandbox', 'SES_SANDBOX', 'default', 'true', false), f('ses_inbound_bucket', 'SES_INBOUND_BUCKET', 'unset', '', false)],
      },
      { name: 'sending', error: '', fields: [f('send_batch_size', 'SEND_BATCH_SIZE', 'default', '50', true)] },
    ],
  };
}

async function mount(data: ServerSettings = fixture()): Promise<void> {
  getServerSettings.mockResolvedValue(data);
  render(ServerSettingsView);
  await waitFor(() => expect(screen.getByText('Amazon SES')).toBeTruthy());
}

function fieldBlock(label: string): HTMLElement {
  return screen.getByLabelText(label).closest('.srv-field') as HTMLElement;
}

describe('ServerSettings', () => {
  it('renders four groups with restart and live notes beside the right fields', async () => {
    await mount();
    expect(document.querySelectorAll('fieldset').length).toBe(4);
    // One badge per group instead of a sentence per field (#0584).
    const badges = [...document.querySelectorAll('fieldset')].map((fs) => fs.querySelector('.apply-badge')?.textContent);
    expect(badges).toEqual(['Applies immediately', 'Applies at next deploy', 'Applies immediately', 'Applies at next deploy']);
    expect(document.body.textContent).not.toContain('Takes effect');
  });

  it('shows the pending-restart note only when pending', async () => {
    await mount();
    expect(screen.queryByText(/still uses the previous value/)).toBeNull();
    cleanup();
    await mount(fixture({ pending: true }));
    expect(fieldBlock('From address').textContent).toContain('Saved. The running service still uses the previous value until it restarts.');
  });

  it('renders the media group error', async () => {
    await mount(fixture({ mediaError: 'The service cannot write to this directory.' }));
    expect(screen.getByText('The service cannot write to this directory.')).toBeTruthy();
  });

  it('saves exactly the typed media directory', async () => {
    await mount();
    updateServerSettings.mockResolvedValue(fixture({ mediaSaved: true }));
    await fireEvent.input(screen.getByLabelText('Media directory'), { target: { value: '/srv/media' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save Media' }));
    await waitFor(() => expect(updateServerSettings).toHaveBeenCalledWith({ media_dir: '/srv/media' }));
    await waitFor(() => expect(screen.getByText('Media saved.')).toBeTruthy());
  });

  it('clears a saved value with an empty string', async () => {
    await mount(fixture({ mediaSaved: true }));
    updateServerSettings.mockResolvedValue(fixture());
    await fireEvent.click(screen.getByRole('button', { name: 'Clear saved value' }));
    await waitFor(() => expect(updateServerSettings).toHaveBeenCalledWith({ media_dir: '' }));
  });

  it('sends the SES sandbox select as a string', async () => {
    await mount();
    updateServerSettings.mockResolvedValue(fixture());
    await fireEvent.change(screen.getByLabelText('SES sandbox'), { target: { value: 'false' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save Amazon SES' }));
    await waitFor(() => expect(updateServerSettings).toHaveBeenCalledWith({ ses_sandbox: 'false' }));
  });

  it('disables Save for an out-of-range batch size', async () => {
    await mount();
    await fireEvent.input(screen.getByLabelText('Send batch size'), { target: { value: '900' } });
    expect((screen.getByRole('button', { name: 'Save Sending' }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText('Must be a whole number from 1 to 500.')).toBeTruthy();
  });

  it('shows a note when the server has no database', async () => {
    getServerSettings.mockRejectedValue(new ApiError(404, 'not found', null));
    render(ServerSettingsView);
    await waitFor(() => expect(screen.getByText(/not available when the server runs without a database/)).toBeTruthy());
  });
});
