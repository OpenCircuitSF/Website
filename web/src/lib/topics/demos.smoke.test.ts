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
import ProbesDemo from './ProbesDemo.svelte';
import ContinuityDemo from './ContinuityDemo.svelte';
import VoltageDemo from './VoltageDemo.svelte';
import CurrentDemo from './CurrentDemo.svelte';

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

  it('multimeter probes: black in COM, red in VΩmA, with feedback', async () => {
    const r = render(ProbesDemo);
    await fireEvent.click(r.getByRole('button', { name: '10A jack' }));
    expect(r.getByText('That jack is only for current.')).toBeTruthy();
    await fireEvent.click(r.getByRole('button', { name: 'COM jack' }));
    expect(r.getByText('Black goes in COM.')).toBeTruthy();
    await fireEvent.click(r.getByRole('button', { name: 'VΩmA jack' }));
    expect(r.getByText('Red goes in VΩmA.')).toBeTruthy();
    expect(r.getByRole('button', { name: 'VΩmA jack, red probe plugged in' })).toBeTruthy();
  });

  it('multimeter continuity: the beep setting beeps on a good wire', async () => {
    const r = render(ContinuityDemo);
    await fireEvent.click(r.getByRole('button', { name: 'Continuity and diode (beep)' }));
    expect(r.getByText('BEEP')).toBeTruthy();
    await fireEvent.click(r.getByRole('radio', { name: 'broken wire' }));
    expect(r.queryByText('BEEP')).toBeNull();
    expect(r.getByText('No beep')).toBeTruthy();
  });

  it('multimeter voltage: 9 V on the 20 range, minus sign when swapped', async () => {
    const r = render(VoltageDemo);
    await fireEvent.click(r.getByRole('button', { name: 'DC volts, 20 V range' }));
    expect(r.container.querySelector('.digits')?.textContent).toBe('9.42');
    await fireEvent.click(r.getByRole('checkbox'));
    expect(r.container.querySelector('.digits')?.textContent).toBe('-9.42');
  });

  it('multimeter current: probes across the battery blow the fuse', async () => {
    const r = render(CurrentDemo);
    await fireEvent.click(r.getByRole('button', { name: 'DC current, 20 mA range' }));
    expect(r.container.querySelector('.digits')?.textContent).toBe('17.95');
    await fireEvent.click(r.getByRole('radio', { name: 'Across the battery' }));
    await tick();
    expect(r.getByText('Fuse blown')).toBeTruthy();
    expect(r.getByRole('button', { name: 'Replace the fuse' })).toBeTruthy();
  });

  it('playground: upload with the serial monitor open fails with a port lock', async () => {
    const r = render(Playground);
    await fireEvent.click(r.getByText(/Serial monitor/));
    await fireEvent.click(r.getByText('→ Upload'));
    await tick();
    expect(r.container.textContent).toContain('Could not exclusively lock port');
  });
});
