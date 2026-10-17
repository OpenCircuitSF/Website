// @vitest-environment jsdom
//
// Mounts OutreachConnections.svelte under jsdom with '../../lib/api' mocked
// (same pattern as OutreachLocations.behavior.test.ts). jsdom is not a
// browser: this proves the form wiring and the write-only secret behaviour,
// not layout.
import { render, cleanup, waitFor, screen, fireEvent } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { OutreachConnections } from '../../lib/types';

const getOutreachConnections = vi.fn<() => Promise<OutreachConnections>>();
const updateOutreachConnections = vi.fn();
const testOutreachConnection = vi.fn();
vi.mock('../../lib/api', () => ({
  getOutreachConnections: () => getOutreachConnections(),
  updateOutreachConnections: (...a: unknown[]) => updateOutreachConnections(...a),
  testOutreachConnection: (...a: unknown[]) => testOutreachConnection(...a),
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

const { default: OutreachConnectionsView } = await import('./OutreachConnections.svelte');
const { ApiError } = await import('../../lib/api');

afterEach(() => {
  cleanup();
  getOutreachConnections.mockReset();
  updateOutreachConnections.mockReset();
  testOutreachConnection.mockReset();
});

function fixture(over: { mastodonToken?: boolean; mastodonUrlSaved?: boolean } = {}): OutreachConnections {
  return {
    groups: [
      {
        name: 'shortlinks',
        configured: false,
        error: '',
        fields: [
          { key: 'shortlinks_base_url', env_var: 'SHORTLINKS_BASE_URL', secret: false, source: 'unset', value: '' },
          { key: 'shortlinks_api_token', env_var: 'SHORTLINKS_API_TOKEN', secret: true, source: 'unset', set: false },
        ],
      },
      {
        name: 'bluesky',
        configured: false,
        error: '',
        fields: [
          { key: 'bluesky_handle', env_var: 'BLUESKY_HANDLE', secret: false, source: 'unset', value: '' },
          { key: 'bluesky_app_password', env_var: 'BLUESKY_APP_PASSWORD', secret: true, source: 'unset', set: false },
          { key: 'bluesky_pds_url', env_var: 'BLUESKY_PDS_URL', secret: false, source: 'default', value: 'https://bsky.social' },
        ],
      },
      {
        name: 'mastodon',
        configured: true,
        error: '',
        fields: [
          {
            key: 'mastodon_base_url',
            env_var: 'MASTODON_BASE_URL',
            secret: false,
            source: over.mastodonUrlSaved ? 'database' : 'environment',
            value: 'https://m.example',
          },
          {
            key: 'mastodon_token',
            env_var: 'MASTODON_TOKEN',
            secret: true,
            source: over.mastodonToken === false ? 'unset' : 'environment',
            set: over.mastodonToken !== false,
          },
        ],
      },
    ],
  };
}

async function mount(data: OutreachConnections = fixture()): Promise<void> {
  getOutreachConnections.mockResolvedValue(data);
  render(OutreachConnectionsView);
  await waitFor(() => expect(screen.getByText('Mastodon')).toBeTruthy());
}

describe('OutreachConnections', () => {
  it('renders three groups and keeps every secret input a blank password field', async () => {
    await mount();
    expect(document.querySelectorAll('fieldset').length).toBe(3);
    const secrets = ['API token', 'App password', 'Access token'].map((l) => screen.getByLabelText(l) as HTMLInputElement);
    for (const input of secrets) {
      expect(input.type).toBe('password');
      expect(input.value).toBe('');
    }
    expect(screen.getByText('Set — from environment variable MASTODON_TOKEN')).toBeTruthy();
  });

  it('saves only the edited fields, then empties the secret and never shows it', async () => {
    await mount();
    updateOutreachConnections.mockResolvedValue(fixture({ mastodonUrlSaved: true }));
    await fireEvent.input(screen.getByLabelText('Instance URL'), { target: { value: 'https://new.example' } });
    await fireEvent.input(screen.getByLabelText('Access token'), { target: { value: 'typed-token-CANARY' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save Mastodon' }));
    await waitFor(() => expect(updateOutreachConnections).toHaveBeenCalled());
    expect(updateOutreachConnections).toHaveBeenCalledWith({
      mastodon_base_url: 'https://new.example',
      mastodon_token: 'typed-token-CANARY',
    });
    await waitFor(() => expect(screen.getByText('Mastodon saved.')).toBeTruthy());
    expect((screen.getByLabelText('Access token') as HTMLInputElement).value).toBe('');
    expect(document.body.textContent).not.toContain('typed-token-CANARY');
  });

  it('clears a saved value with an empty string', async () => {
    await mount(fixture({ mastodonUrlSaved: false }));
    updateOutreachConnections.mockResolvedValue(fixture());
    // The token comes from the environment, so there is nothing saved to clear.
    expect(screen.queryByRole('button', { name: 'Clear saved value' })).toBeNull();
  });

  it('offers Clear saved value for a database secret', async () => {
    const data = fixture();
    const token = data.groups[2].fields[1];
    if (token.secret) token.source = 'database';
    await mount(data);
    updateOutreachConnections.mockResolvedValue(fixture());
    await fireEvent.click(screen.getByRole('button', { name: 'Clear saved value' }));
    await waitFor(() => expect(updateOutreachConnections).toHaveBeenCalledWith({ mastodon_token: '' }));
  });

  it('writes the test result into the group status node', async () => {
    await mount();
    testOutreachConnection.mockResolvedValueOnce({ ok: true, detail: 'Signed in to Mastodon as @opencircuit' });
    const buttons = screen.getAllByRole('button', { name: 'Test connection' });
    await fireEvent.click(buttons[2]);
    await waitFor(() => expect(testOutreachConnection).toHaveBeenCalledWith('mastodon'));
    const status = document.querySelectorAll('fieldset')[2].querySelector('[role="status"]');
    await waitFor(() => expect(status?.textContent).toBe('Signed in to Mastodon as @opencircuit'));
    testOutreachConnection.mockResolvedValueOnce({ ok: false, error: 'Mastodon rejected the access token.' });
    await fireEvent.click(buttons[2]);
    await waitFor(() => expect(status?.textContent).toBe('Mastodon rejected the access token.'));
  });

  it('shows a note when the server has no database', async () => {
    getOutreachConnections.mockRejectedValue(new ApiError(404, 'not found', null));
    render(OutreachConnectionsView);
    await waitFor(() => expect(screen.getByText(/not available when the server runs without a database/)).toBeTruthy());
  });
});
