// @vitest-environment jsdom
//
// Mounts ApiTokens.svelte under jsdom with '../../lib/api' mocked (same
// pattern as OutreachConnections.behavior.test.ts). jsdom is not a browser:
// this proves the list, create, show-once and revoke wiring, not layout.
import { render, cleanup, waitFor, screen, fireEvent } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ApiToken, ApiTokenList, CreatedApiToken } from '../../lib/types';

const listApiTokens = vi.fn<() => Promise<ApiTokenList>>();
const createApiToken = vi.fn<(name: string, scopes: string[]) => Promise<CreatedApiToken>>();
const revokeApiToken = vi.fn<(id: number) => Promise<{ token: ApiToken }>>();
vi.mock('../../lib/api', () => ({
  listApiTokens: () => listApiTokens(),
  createApiToken: (name: string, scopes: string[]) => createApiToken(name, scopes),
  revokeApiToken: (id: number) => revokeApiToken(id),
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

const { default: ApiTokensView } = await import('./ApiTokens.svelte');
const { ApiError } = await import('../../lib/api');

const SECRET = 'ocsf_' + 'A'.repeat(43);

function tok(over: Partial<ApiToken> = {}): ApiToken {
  return {
    id: 1,
    name: 'Home Assistant',
    prefix: 'ocsf_abcdefg',
    scopes: ['engagement:read'],
    created_at: '2026-11-08T20:00:00Z',
    last_used_at: null,
    revoked_at: null,
    ...over,
  };
}

afterEach(() => {
  cleanup();
  listApiTokens.mockReset();
  createApiToken.mockReset();
  revokeApiToken.mockReset();
  vi.unstubAllGlobals();
});

describe('ApiTokens (#0652)', () => {
  it('lists tokens with prefix, access, last used and status', async () => {
    listApiTokens.mockResolvedValue({
      tokens: [tok({ last_used_at: '2026-11-08T21:00:00Z' }), tok({ id: 2, name: 'Old', revoked_at: '2026-11-08T22:00:00Z' })],
    });
    render(ApiTokensView);
    await screen.findByText('Old');
    const text = document.body.textContent ?? '';
    expect(text).toContain('ocsf_abcdefg…');
    expect(text).toContain('Read engagement');
    expect(text).toContain('Active');
    expect(text).toContain('Revoked ');
    expect(screen.getAllByRole('button', { name: 'Revoke' })).toHaveLength(1);
  });

  it('shows Never when a token has not been used', async () => {
    listApiTokens.mockResolvedValue({ tokens: [tok()] });
    render(ApiTokensView);
    await screen.findByText('Never');
  });

  it('creates a token, shows the secret once, and hides it after Done', async () => {
    listApiTokens.mockResolvedValue({ tokens: [] });
    createApiToken.mockResolvedValue({ token: tok(), secret: SECRET });
    render(ApiTokensView);
    await screen.findByText('No API tokens yet.');
    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'Home Assistant' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Create token' }));
    await screen.findByText(SECRET);
    expect(document.body.textContent).toContain('Copy this token now. It will not be shown again.');
    expect(listApiTokens).toHaveBeenCalledTimes(2);
    await fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    await waitFor(() => expect(document.body.textContent).not.toContain(SECRET));
  });

  it('sends engagement:read as the only scope', async () => {
    listApiTokens.mockResolvedValue({ tokens: [] });
    createApiToken.mockResolvedValue({ token: tok(), secret: SECRET });
    render(ApiTokensView);
    await screen.findByText('No API tokens yet.');
    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: '  HA ' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Create token' }));
    await waitFor(() => expect(createApiToken).toHaveBeenCalledWith('HA', ['engagement:read']));
  });

  it('shows the name error without calling the API', async () => {
    listApiTokens.mockResolvedValue({ tokens: [] });
    render(ApiTokensView);
    await screen.findByText('No API tokens yet.');
    await fireEvent.click(screen.getByRole('button', { name: 'Create token' }));
    expect((await screen.findByRole('alert')).textContent).toBe('Enter a name for the token.');
    expect(createApiToken).not.toHaveBeenCalled();
  });

  it('revokes after confirm and reloads', async () => {
    vi.stubGlobal('confirm', vi.fn(() => true));
    listApiTokens.mockResolvedValue({ tokens: [tok({ id: 9 })] });
    revokeApiToken.mockResolvedValue({ token: tok({ id: 9, revoked_at: '2026-11-08T22:00:00Z' }) });
    render(ApiTokensView);
    await fireEvent.click(await screen.findByRole('button', { name: 'Revoke' }));
    await waitFor(() => expect(revokeApiToken).toHaveBeenCalledWith(9));
    await waitFor(() => expect(listApiTokens).toHaveBeenCalledTimes(2));
  });

  it('does not revoke when confirm is cancelled', async () => {
    vi.stubGlobal('confirm', vi.fn(() => false));
    listApiTokens.mockResolvedValue({ tokens: [tok()] });
    render(ApiTokensView);
    await fireEvent.click(await screen.findByRole('button', { name: 'Revoke' }));
    expect(revokeApiToken).not.toHaveBeenCalled();
  });

  it('shows the unavailable line on 404', async () => {
    listApiTokens.mockRejectedValue(new ApiError(404, 'not found', null));
    render(ApiTokensView);
    await screen.findByText('API tokens are not available when the server runs without a database.');
  });
});
