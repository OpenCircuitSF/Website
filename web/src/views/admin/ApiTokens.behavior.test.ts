// @vitest-environment jsdom
//
// Mounts ApiTokens.svelte under jsdom with '../../lib/api' mocked (same
// pattern as OutreachConnections.behavior.test.ts). jsdom is not a browser:
// this proves the list, create, show-once and revoke wiring, not layout.
import { render, cleanup, waitFor, screen, fireEvent } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ApiToken, ApiTokenList, CreatedApiToken } from '../../lib/types';

const listApiTokens = vi.fn<() => Promise<ApiTokenList>>();
const createApiToken = vi.fn<(name: string, scopes: string[], expiresAt: string | null) => Promise<CreatedApiToken>>();
const revokeApiToken = vi.fn<(id: number) => Promise<{ token: ApiToken }>>();
vi.mock('../../lib/api', () => ({
  listApiTokens: () => listApiTokens(),
  createApiToken: (name: string, scopes: string[], expiresAt: string | null) => createApiToken(name, scopes, expiresAt),
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
    expires_at: null,
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

  it('shows Never when a token has not been used (and, #0674, never expires)', async () => {
    listApiTokens.mockResolvedValue({ tokens: [tok()] });
    render(ApiTokensView);
    await screen.findByText('Home Assistant');
    // Last used and Expires both read Never for this token.
    expect(screen.getAllByRole('cell', { name: 'Never' })).toHaveLength(2);
  });

  it('creates a token, shows the secret once, and hides it after Done', async () => {
    listApiTokens.mockResolvedValue({ tokens: [] });
    createApiToken.mockResolvedValue({ token: tok(), secret: SECRET });
    render(ApiTokensView);
    await screen.findByText('No API tokens yet.');
    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'Home Assistant' } });
    await fireEvent.click(screen.getByLabelText(/Read engagement/));
    await fireEvent.click(screen.getByRole('button', { name: 'Create token' }));
    await screen.findByText(SECRET);
    expect(document.body.textContent).toContain('Copy this token now. It will not be shown again.');
    expect(listApiTokens).toHaveBeenCalledTimes(2);
    await fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    await waitFor(() => expect(document.body.textContent).not.toContain(SECRET));
  });

  it('sends the ticked scope and a 90-day expiry by default (#0674)', async () => {
    listApiTokens.mockResolvedValue({ tokens: [] });
    createApiToken.mockResolvedValue({ token: tok(), secret: SECRET });
    render(ApiTokensView);
    await screen.findByText('No API tokens yet.');
    expect((screen.getByLabelText('Expires') as HTMLSelectElement).value).toBe('90');
    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: '  HA ' } });
    await fireEvent.click(screen.getByLabelText(/Read engagement/));
    const before = Date.now();
    await fireEvent.click(screen.getByRole('button', { name: 'Create token' }));
    await waitFor(() => expect(createApiToken).toHaveBeenCalled());
    const [name, scopes, expiresAt] = createApiToken.mock.calls[0];
    expect(name).toBe('HA');
    expect(scopes).toEqual(['engagement:read']);
    const days = (new Date(expiresAt as string).getTime() - before) / 86_400_000;
    expect(days).toBeGreaterThan(89.99);
    expect(days).toBeLessThan(90.01);
  });

  it('the Agent preset ticks every guide and event scope and Never sends null', async () => {
    listApiTokens.mockResolvedValue({ tokens: [] });
    createApiToken.mockResolvedValue({ token: tok(), secret: SECRET });
    render(ApiTokensView);
    await screen.findByText('No API tokens yet.');
    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'Claude Code' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Agent (all guide and event scopes)' }));
    expect((screen.getByLabelText(/Write events/) as HTMLInputElement).checked).toBe(true);
    expect((screen.getByLabelText(/Read engagement/) as HTMLInputElement).checked).toBe(false);
    await fireEvent.change(screen.getByLabelText('Expires'), { target: { value: 'never' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Create token' }));
    await waitFor(() =>
      expect(createApiToken).toHaveBeenCalledWith(
        'Claude Code',
        ['guides:read', 'guide_posts:read', 'guide_posts:write', 'events:read', 'events:write', 'venues:read'],
        null,
      ),
    );
  });

  it('a custom date expires at the end of that day; a missing date is refused', async () => {
    listApiTokens.mockResolvedValue({ tokens: [] });
    createApiToken.mockResolvedValue({ token: tok(), secret: SECRET });
    render(ApiTokensView);
    await screen.findByText('No API tokens yet.');
    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'HA' } });
    await fireEvent.click(screen.getByLabelText(/Read events/));
    await fireEvent.change(screen.getByLabelText('Expires'), { target: { value: 'custom' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Create token' }));
    expect((await screen.findByRole('alert')).textContent).toBe('Pick an expiry date.');
    expect(createApiToken).not.toHaveBeenCalled();
    await fireEvent.input(screen.getByLabelText('Expiry date'), { target: { value: '2099-03-04' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Create token' }));
    await waitFor(() =>
      expect(createApiToken).toHaveBeenCalledWith('HA', ['events:read'], new Date(2099, 2, 4, 23, 59, 59).toISOString()),
    );
  });

  it('refuses to create a token with no scope', async () => {
    listApiTokens.mockResolvedValue({ tokens: [] });
    render(ApiTokensView);
    await screen.findByText('No API tokens yet.');
    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'HA' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Create token' }));
    expect((await screen.findByRole('alert')).textContent).toBe('Choose at least one scope.');
    expect(createApiToken).not.toHaveBeenCalled();
  });

  it('lists the expiry and marks an expired key', async () => {
    listApiTokens.mockResolvedValue({
      tokens: [
        tok({ id: 1, name: 'Fresh', expires_at: '2099-01-01T00:00:00Z' }),
        tok({ id: 2, name: 'Stale', expires_at: '2020-01-01T00:00:00Z' }),
      ],
    });
    render(ApiTokensView);
    await screen.findByText('Stale');
    const text = document.body.textContent ?? '';
    expect(text).toContain('Expires');
    expect(text).toContain('Expired ');
    expect(text).toContain('Active');
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
