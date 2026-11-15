// @vitest-environment jsdom
// Smoke test: every guide demo mounts, and the main buttons respond.
import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render } from '@testing-library/svelte';
import { tick } from 'svelte';
import { TOPICS } from './topics';
import Playground from '../guide/Playground.svelte';
import CableDemo from './CableDemo.svelte';
import BaudDemo from './BaudDemo.svelte';
import PinoutExplorer from './PinoutExplorer.svelte';

afterEach(cleanup);

describe('guide demos', () => {
  for (const t of TOPICS) {
    it(`${t.slug} demo mounts`, () => {
      const { container } = render(t.demo);
      expect(container.innerHTML.length).toBeGreaterThan(100);
    });
  }

  it('cable demo: charge-only cable leaves the laptop blind', async () => {
    const r = render(CableDemo);
    await fireEvent.click(r.getByText('Plug it in'));
    expect(r.getByText('No new device')).toBeTruthy();
    await fireEvent.click(r.getByText('Data cable'));
    await fireEvent.click(r.getByText('Plug it in'));
    expect(r.getByText('/dev/cu.usbmodem101')).toBeTruthy();
  });

  it('baud demo: matching speed decodes the message', async () => {
    const r = render(BaudDemo);
    await fireEvent.click(r.getByRole('radio', { name: '115200' }));
    expect(r.container.querySelector('.mon-text')?.textContent).toBe('Hello!');
  });

  it('pinout: tapping GPIO 9 shows the BOOT button', async () => {
    const r = render(PinoutExplorer);
    await fireEvent.click(r.getByRole('button', { name: 'GPIO 9: BOOT button' }));
    expect(r.getByRole('heading', { name: 'GPIO 9: BOOT button' })).toBeTruthy();
  });

  it('playground: upload with the serial monitor open fails with a port lock', async () => {
    const r = render(Playground);
    await fireEvent.click(r.getByText(/Serial monitor/));
    await fireEvent.click(r.getByText('→ Upload'));
    await tick();
    expect(r.container.textContent).toContain('Could not exclusively lock port');
  });
});
