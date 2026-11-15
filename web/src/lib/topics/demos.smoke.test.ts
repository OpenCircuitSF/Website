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
// Soldering guides (#0667)
import SolderWireDemo from './SolderWireDemo.svelte';
import IronTempDemo from './IronTempDemo.svelte';
import TinningDemo from './TinningDemo.svelte';
import JointQuiz from './JointQuiz.svelte';
import TipCareDemo from './TipCareDemo.svelte';
import HeaderPinsDemo from './HeaderPinsDemo.svelte';

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

  // Soldering guides (#0667)
  it('solder wire: plumbing solder is flagged, 63/37 melts at 183 °C', async () => {
    const r = render(SolderWireDemo);
    expect(r.container.textContent).toContain('183 °C');
    await fireEvent.click(r.getByRole('radio', { name: 'Plumbing solder' }));
    expect(r.container.textContent).toContain('Never use it on electronics');
  });

  it('iron temperature: 340 °C makes a good leaded joint, too cool for lead-free', async () => {
    const r = render(IronTempDemo);
    await fireEvent.input(r.getByRole('slider'), { target: { value: '340' } });
    expect(r.getByRole('heading', { name: 'Good joint' })).toBeTruthy();
    await fireEvent.click(r.getByRole('radio', { name: 'Lead-free SAC305' }));
    expect(r.getByRole('heading', { name: 'It works, slowly' })).toBeTruthy();
  });

  it('tinning: both tinned joins in about a second; bare makes a blob', async () => {
    const r = render(TinningDemo);
    await fireEvent.click(r.getByText('Join them'));
    expect(r.getByRole('heading', { name: 'A weak blob' })).toBeTruthy();
    await fireEvent.click(r.getByText('Start over'));
    await fireEvent.click(r.getByText('Tin the wire'));
    await fireEvent.click(r.getByText('Tin the pad'));
    await fireEvent.click(r.getByText('Join them'));
    expect(r.getByRole('heading', { name: 'Clean joint in about a second' })).toBeTruthy();
  });

  it('joint quiz: naming the first joint correctly scores a point', async () => {
    const r = render(JointQuiz);
    await fireEvent.click(r.getByRole('button', { name: 'Cold joint' }));
    expect(r.getByText('Correct!')).toBeTruthy();
    await fireEvent.click(r.getByText('Next joint →'));
    expect(r.container.textContent).toContain('Joint 2 of 7 · Score 1');
  });

  it('tip care: storing a bare tip twice turns it black and it stops taking solder', async () => {
    const r = render(TipCareDemo);
    await fireEvent.click(r.getByText('Switch off with a bare tip'));
    await fireEvent.click(r.getByText('Switch off with a bare tip'));
    expect(r.getByText('Black, oxidized')).toBeTruthy();
    expect(r.getByText('Won’t take solder')).toBeTruthy();
    await fireEvent.click(r.getByText('Use tip tinner paste'));
    expect(r.getByText('Shiny silver')).toBeTruthy();
  });

  it('header pins: the third step tacks one corner', async () => {
    const r = render(HeaderPinsDemo);
    await fireEvent.click(r.getByText('Next →'));
    await fireEvent.click(r.getByText('Next →'));
    expect(r.getByRole('heading', { name: 'Tack one corner' })).toBeTruthy();
  });

  // aria-disabled (not disabled) keeps focus on the button, so each handler
  // must ignore the click itself (#0667 review).
  it('aria-disabled soldering buttons ignore clicks', async () => {
    const q = render(JointQuiz);
    await fireEvent.click(q.getByRole('button', { name: 'Cold joint' }));
    await fireEvent.click(q.getByRole('button', { name: 'Good joint' }));
    expect(q.getByText('Correct!')).toBeTruthy();
    expect(q.getByRole('button', { name: 'Good joint' }).getAttribute('aria-disabled')).toBe('true');
    cleanup();

    const t = render(TinningDemo);
    await fireEvent.click(t.getByText('Join them'));
    await fireEvent.click(t.getByText('Tin the wire'));
    await fireEvent.click(t.getByText('Tin the pad'));
    expect(t.getByRole('heading', { name: 'A weak blob' })).toBeTruthy();
    cleanup();

    const h = render(HeaderPinsDemo);
    await fireEvent.click(h.getByText('← Back'));
    expect(h.container.textContent).toContain('Step 1 of 6');
    for (let i = 0; i < 8; i++) await fireEvent.click(h.getByText('Next →'));
    expect(h.container.textContent).toContain('Step 6 of 6');
  });

  it('joint quiz: focus lands on "Try again" at the end and back on a choice after restarting', async () => {
    const r = render(JointQuiz);
    for (let i = 0; i < 7; i++) {
      await fireEvent.click(r.getByRole('button', { name: 'Good joint' }));
      await fireEvent.click(r.getByRole('button', { name: /Next joint|See score/ }));
      await tick();
    }
    const again = r.getByRole('button', { name: 'Try again' });
    expect(document.activeElement).toBe(again);
    await fireEvent.click(again);
    await tick();
    expect(document.activeElement?.textContent).toBe('Good joint');
  });
});
