// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';

const listPublicInterests = vi.fn();
const subscribe = vi.fn();
vi.mock('./api', async (original) => ({
  ...(await original<typeof import('./api')>()),
  listPublicInterests: () => listPublicInterests(),
  subscribe: (body: unknown) => subscribe(body),
}));
vi.mock('./router', async (original) => ({
  ...(await original<typeof import('./router')>()),
  navigate: () => {},
}));

const { default: SubscribeForm } = await import('./SubscribeForm.svelte');

describe('SubscribeForm preselect (#0662)', () => {
  let clock = 0;
  beforeEach(() => {
    clock = 1_000_000;
    vi.spyOn(Date, 'now').mockImplementation(() => clock);
    listPublicInterests.mockResolvedValue({
      interests: [
        { slug: 'soldering', name: 'Soldering' },
        { slug: 'homelab', name: 'Homelab' },
      ],
    });
    subscribe.mockResolvedValue({ message: 'ok' });
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  async function submit(): Promise<Record<string, unknown>> {
    await fireEvent.input(screen.getByLabelText('Email'), { target: { value: 'reader@example.com' } });
    clock += 5_000;
    await fireEvent.click(screen.getByRole('button', { name: 'Subscribe' }));
    await waitFor(() => expect(subscribe).toHaveBeenCalled());
    return subscribe.mock.calls[0][0];
  }

  it('starts with nothing checked when no preselect is given', async () => {
    render(SubscribeForm);
    expect(((await screen.findByLabelText('Soldering')) as HTMLInputElement).checked).toBe(false);
    expect((screen.getByLabelText('Homelab') as HTMLInputElement).checked).toBe(false);
  });

  it('pre-checks preselect slugs that are active', async () => {
    render(SubscribeForm, { preselect: ['soldering'] });
    expect(((await screen.findByLabelText('Soldering')) as HTMLInputElement).checked).toBe(true);
    expect((screen.getByLabelText('Homelab') as HTMLInputElement).checked).toBe(false);
  });

  it('posts without a pre-checked interest the reader unticked', async () => {
    render(SubscribeForm, { preselect: ['soldering', 'homelab'] });
    await fireEvent.click(await screen.findByLabelText('Homelab'));
    expect((await submit()).interests).toEqual(['soldering']);
  });

  it('never posts a preselected slug that is not active', async () => {
    render(SubscribeForm, { preselect: ['soldering', 'retired'] });
    await screen.findByLabelText('Soldering');
    expect((await submit()).interests).toEqual(['soldering']);
  });
});
