// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';

const listPublicWorkshops = vi.fn();
const listPublicInterests = vi.fn();
const subscribe = vi.fn();
vi.mock('./api', async (original) => ({
  ...(await original<typeof import('./api')>()),
  listPublicWorkshops: () => listPublicWorkshops(),
  listPublicInterests: () => listPublicInterests(),
  subscribe: (body: unknown) => subscribe(body),
}));
vi.mock('./router', async (original) => ({
  ...(await original<typeof import('./router')>()),
  navigate: () => {},
}));

const { default: GuideEnd } = await import('./GuideEnd.svelte');

function ev(slug: string, title: string, interests: string[] = []) {
  return {
    slug,
    title,
    status: 'published',
    interests: interests.map((s) => ({ slug: s, name: s })),
    event_type: { slug: 'workshop', name: 'Workshop', sort_order: 10 },
  };
}

describe('GuideEnd (#0662)', () => {
  beforeEach(() => {
    listPublicWorkshops.mockResolvedValue({
      upcoming: [ev('hack-night', 'Hack Night Friday', ['homelab']), ev('programming-leds-2', 'Programming LEDs II', ['microcontrollers'])],
      past: [],
    });
    listPublicInterests.mockResolvedValue({
      interests: [
        { slug: 'microcontrollers', name: 'Microcontrollers' },
        { slug: 'homelab', name: 'Homelab' },
      ],
    });
    subscribe.mockResolvedValue({ message: 'ok' });
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it('renders the newsletter heading and a subscribe form', async () => {
    render(GuideEnd, { kind: 'topic', slug: 'delay-vs-millis' });
    expect(screen.getByRole('heading', { name: 'Get the newsletter' })).toBeTruthy();
    expect(await screen.findByLabelText('Email')).toBeTruthy();
  });

  it('lists related upcoming events first', async () => {
    const { container } = render(GuideEnd, { kind: 'workshop', slug: 'programming-leds' });
    await screen.findByRole('link', { name: 'Programming LEDs II' });
    const titles = Array.from(container.querySelectorAll('.workshop-cards a')).map((a) => a.textContent?.trim());
    expect(titles.indexOf('Programming LEDs II')).toBeLessThan(titles.indexOf('Hack Night Friday'));
  });

  it('shows the empty state when nothing is scheduled', async () => {
    listPublicWorkshops.mockResolvedValue({ upcoming: [], past: [] });
    render(GuideEnd, { kind: 'topic', slug: 'delay-vs-millis' });
    expect(await screen.findByText(/Nothing scheduled right now\./)).toBeTruthy();
  });

  it('keeps the See all events link when the events fetch fails', async () => {
    listPublicWorkshops.mockRejectedValue(new Error('down'));
    render(GuideEnd, { kind: 'topic', slug: 'delay-vs-millis' });
    await screen.findByLabelText('Email');
    expect(screen.getByRole('link', { name: 'See all events →' }).getAttribute('href')).toBe('/events');
    expect(screen.queryByText(/Nothing scheduled/)).toBeNull();
  });

  it('posts signup_guide with the page slug', async () => {
    let clock = 1_000_000;
    vi.spyOn(Date, 'now').mockImplementation(() => clock);
    render(GuideEnd, { kind: 'topic', slug: 'delay-vs-millis' });
    const input = await screen.findByLabelText('Email');
    await fireEvent.input(input, { target: { value: 'reader@example.com' } });
    clock += 5_000;
    await fireEvent.click(screen.getByRole('button', { name: 'Subscribe' }));
    await waitFor(() => expect(subscribe).toHaveBeenCalled());
    const body = subscribe.mock.calls[0][0];
    expect(body.signup_guide).toBe('delay-vs-millis');
    expect(body.rendered_at).toBe(1_000_000);
  });

  it('pre-checks the related workshop interests', async () => {
    render(GuideEnd, { kind: 'workshop', slug: 'programming-leds' });
    const micro = (await screen.findByLabelText('Microcontrollers')) as HTMLInputElement;
    const homelab = screen.getByLabelText('Homelab') as HTMLInputElement;
    expect(micro.checked).toBe(true);
    expect(homelab.checked).toBe(false);
  });

  it('mounts the form with nothing pre-checked when the events fetch fails', async () => {
    listPublicWorkshops.mockRejectedValue(new Error('down'));
    render(GuideEnd, { kind: 'workshop', slug: 'programming-leds' });
    const micro = (await screen.findByLabelText('Microcontrollers')) as HTMLInputElement;
    expect(micro.checked).toBe(false);
  });
});
