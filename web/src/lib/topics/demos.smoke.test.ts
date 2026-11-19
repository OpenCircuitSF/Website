// @vitest-environment jsdom
// Smoke test: every guide demo mounts, and the main buttons respond.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import { TOPICS } from './topics';
import { TOPIC_DEMOS } from './demoLoaders';
import { resetTempUnitForTest, setTempUnit } from '../tempUnit.svelte';
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
// ESP32-C3: code on the board (#0670)
import BootButtonsDemo from './BootButtonsDemo.svelte';
import UploadDoctor from './UploadDoctor.svelte';
import SerialOutputDemo from './SerialOutputDemo.svelte';
import IniBuilderDemo from './IniBuilderDemo.svelte';
import OtaDemo from './OtaDemo.svelte';
// ESP32-C3: wireless and sleep (#0672)
import WifiConnectDemo from './WifiConnectDemo.svelte';
import BleWifiPicker from './BleWifiPicker.svelte';
import BleBasicsDemo from './BleBasicsDemo.svelte';
import BrownoutDemo from './BrownoutDemo.svelte';
import DeepSleepDemo from './DeepSleepDemo.svelte';
// ESP32-C3: pins and power (#0671)
import C3BootPinsDemo from './C3BootPinsDemo.svelte';
import C3PinChooserDemo from './C3PinChooserDemo.svelte';
import C3AnalogDemo from './C3AnalogDemo.svelte';
import C3PwmDemo from './C3PwmDemo.svelte';
import C3PowerDemo from './C3PowerDemo.svelte';
// Components: capacitors (#0691)
import CapCodeDemo from './CapCodeDemo.svelte';
import RcChargeDemo from './RcChargeDemo.svelte';
import DecouplingDemo from './DecouplingDemo.svelte';
// Components: transistors and MOSFETs (#0692)
import BjtSwitchDemo from './BjtSwitchDemo.svelte';
import MosfetSwitchDemo from './MosfetSwitchDemo.svelte';
import FlybackDemo from './FlybackDemo.svelte';
import SwitchPickerDemo from './SwitchPickerDemo.svelte';
// Components: resistors and LEDs (#0690)
import ResistorBandsDemo from './ResistorBandsDemo.svelte';
import OhmsLawDemo from './OhmsLawDemo.svelte';
import LedBasicsDemo from './LedBasicsDemo.svelte';
import LedResistorDemo from './LedResistorDemo.svelte';
// Raspberry Pi: setup, WiFi and Bookworm (#0719)
import PiImagerDemo from './PiImagerDemo.svelte';
import PiWifiBandDemo from './PiWifiBandDemo.svelte';
import PiBookwormDemo from './PiBookwormDemo.svelte';
// Raspberry Pi: power and SD cards (#0720)
import PiUndervoltageDemo from './PiUndervoltageDemo.svelte';
import ThrottledDecoder from './ThrottledDecoder.svelte';
import SdWriteDemo from './SdWriteDemo.svelte';
// Raspberry Pi: Volumio and RGB panels (#0721)
import VolumioDemo from './VolumioDemo.svelte';
import RgbMatrixDemo from './RgbMatrixDemo.svelte';
// Raspberry Pi batch 2: Zero audio, RGB panel basics and power (#0726)
import ZeroAudioDemo from './ZeroAudioDemo.svelte';
import RgbLayoutDemo from './RgbLayoutDemo.svelte';
import RgbPowerDemo from './RgbPowerDemo.svelte';

afterEach(cleanup);

describe('guide demos', () => {
  for (const t of TOPICS) {
    it(`${t.slug} demo mounts`, async () => {
      const { container } = render((await TOPIC_DEMOS[t.slug]()).default);
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

  for (const [name, Demo, setting] of [
    ['voltage', VoltageDemo, 'DC volts, 20'],
    ['current', CurrentDemo, 'DC current, 20m'],
    ['continuity', ContinuityDemo, 'continuity'],
  ] as const) {
    it(`multimeter ${name}: shows how to set the dial, and the button sets it`, async () => {
      const r = render(Demo);
      expect(r.getByText(`Turn the dial to ${setting}`, { selector: 'strong' })).toBeTruthy();
      expect(r.container.querySelectorAll('.spot.dial.hint')).toHaveLength(1);
      await fireEvent.click(r.getByRole('button', { name: `Turn the dial to ${setting}` }));
      expect(r.getByText(`The dial is on ${setting}`)).toBeTruthy();
      expect(r.container.querySelectorAll('.spot.dial.hint')).toHaveLength(0);
    });
  }

  it('multimeter continuity: points at the symbol until the dial is on it', async () => {
    const r = render(ContinuityDemo);
    const spot = r.getByRole('button', { name: 'Continuity and diode (beep)' });
    expect(spot.classList.contains('hint')).toBe(true);
    expect(r.getByText('Set the dial')).toBeTruthy();
    expect(r.getByRole('img', { name: /Continuity symbol/ })).toBeTruthy();
    await fireEvent.click(r.getByRole('button', { name: 'Turn the dial to continuity' }));
    expect(spot.classList.contains('hint')).toBe(false);
    expect(spot.getAttribute('aria-pressed')).toBe('true');
    expect(r.getByText('BEEP')).toBeTruthy();
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
  it('solder wire: 63/37 melts at 361 (°F by default, 183 in °C); plumbing solder is flagged', async () => {
    resetTempUnitForTest();
    const r = render(SolderWireDemo);
    expect(r.container.textContent).toContain('361 °F');
    expect(r.container.textContent).toContain('630–660 °F');
    setTempUnit('C');
    await tick();
    expect(r.container.textContent).toContain('183 °C');
    resetTempUnitForTest();
    await tick();
    await fireEvent.click(r.getByRole('radio', { name: 'Plumbing solder' }));
    expect(r.container.textContent).toContain('Never use it on electronics');
  });

  it('iron temperature: 640 (°F) / 340 (°C) makes a good leaded joint, too cool for lead-free', async () => {
    resetTempUnitForTest();
    const r = render(IronTempDemo);
    expect(r.container.textContent).toContain('Iron set to 480 °F');
    await fireEvent.input(r.getByRole('slider'), { target: { value: '640' } });
    expect(r.container.textContent).toContain('Iron set to 640 °F');
    expect(r.getByRole('heading', { name: 'Good joint' })).toBeTruthy();
    await fireEvent.click(r.getByRole('radio', { name: 'Lead-free SAC305' }));
    expect(r.getByRole('heading', { name: 'It works, slowly' })).toBeTruthy();
    await fireEvent.click(r.getByRole('radio', { name: 'Leaded 63/37' }));
    setTempUnit('C');
    await tick();
    await fireEvent.input(r.getByRole('slider'), { target: { value: '340' } });
    expect(r.container.textContent).toContain('Iron set to 340 °C');
    expect(r.getByRole('heading', { name: 'Good joint' })).toBeTruthy();
    await fireEvent.click(r.getByRole('radio', { name: 'Lead-free SAC305' }));
    expect(r.getByRole('heading', { name: 'It works, slowly' })).toBeTruthy();
    resetTempUnitForTest();
  });

  it('iron temperature: every setting in the shown sweet spot is a good joint', async () => {
    for (const unit of ['F', 'C'] as const) {
      for (const solder of ['Leaded 63/37', 'Lead-free SAC305']) {
        resetTempUnitForTest();
        setTempUnit(unit);
        const r = render(IronTempDemo);
        await tick();
        await fireEvent.click(r.getByRole('radio', { name: solder }));
        const m = /^sweet spot (\d+)–(\d+)$/.exec(r.container.querySelector('.scale > span:nth-child(2)')!.textContent ?? '');
        expect(m, `${solder} ${unit}`).not.toBeNull();
        const lo = Number(m![1]);
        const hi = Number(m![2]);
        const step = unit === 'F' ? 10 : 5;
        const slider = r.getByRole('slider');
        for (let v = lo; v <= hi; v += step) {
          await fireEvent.input(slider, { target: { value: String(v) } });
          expect(r.queryByRole('heading', { name: 'Good joint' }), `${solder} at ${v} °${unit}`).not.toBeNull();
        }
        for (const v of [lo - step, hi + step]) {
          await fireEvent.input(slider, { target: { value: String(v) } });
          expect(r.queryByRole('heading', { name: 'Good joint' }), `${solder} at ${v} °${unit}`).toBeNull();
        }
        cleanup();
      }
    }
    resetTempUnitForTest();
  });

  it('iron temperature: switching units re-judges the shown setting', async () => {
    resetTempUnitForTest();
    const r = render(IronTempDemo);
    await fireEvent.click(r.getByRole('radio', { name: 'Lead-free SAC305' }));
    setTempUnit('C');
    await tick();
    await fireEvent.input(r.getByRole('slider'), { target: { value: '350' } });
    expect(r.getByRole('heading', { name: 'Good joint' })).toBeTruthy();
    setTempUnit('F');
    await tick();
    expect(r.container.textContent).toContain('Iron set to 660 °F');
    expect(r.getByRole('heading', { name: 'It works, slowly' })).toBeTruthy();
    resetTempUnitForTest();
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

  // ESP32-C3: code on the board (#0670)
  it('boot buttons: hold BOOT, tap RST, let go, upload, RST runs it', async () => {
    const r = render(BootButtonsDemo);
    expect(r.getByText('Deep sleep')).toBeTruthy();
    const boot = r.getByRole('button', { name: 'BOOT button' });
    const rst = r.getByRole('button', { name: 'RST button' });
    await fireEvent.click(boot);
    await fireEvent.click(rst);
    expect(r.getByText('Held in reset')).toBeTruthy();
    await fireEvent.click(rst);
    await fireEvent.click(boot);
    expect(r.getByText('Download mode')).toBeTruthy();
    expect(r.getByText('/dev/cu.usbmodem101')).toBeTruthy();
    await fireEvent.click(r.getByText('Upload'));
    expect(r.container.textContent).toContain('Press RST to run the new code');
    await fireEvent.click(rst);
    await fireEvent.click(rst);
    expect(r.getByText('Running your program')).toBeTruthy();
  });

  it('upload doctor: no port and an unknown cable ends at swapping the cable', async () => {
    const r = render(UploadDoctor);
    await fireEvent.click(r.getByRole('button', { name: 'No' }));
    await fireEvent.click(r.getByRole('button', { name: 'No' }));
    await tick();
    const h = r.getByRole('heading', { name: 'Swap the cable' });
    expect(document.activeElement).toBe(h);
    await fireEvent.click(r.getByRole('button', { name: 'Start over' }));
    expect(r.getByRole('heading', { name: /Does a new port show up/ })).toBeTruthy();
  });

  it('serial output: blank monitor until the USB flags are added', async () => {
    const r = render(SerialOutputDemo);
    const run = r.getByText('Upload, then watch the monitor');
    await fireEvent.click(run);
    const screens = r.container.querySelectorAll('.screen');
    expect(screens[0].textContent).toBe('(nothing)');
    expect(screens[1].textContent).toContain('Hello from setup()');
    await fireEvent.click(r.getByRole('checkbox', { name: 'Add the USB build flags' }));
    await fireEvent.click(run);
    expect(screens[0].textContent).toContain('loop 1');
    expect(screens[0].textContent).not.toContain('Hello from setup()');
    await fireEvent.click(r.getByRole('checkbox', { name: 'Wait for the monitor in setup()' }));
    await fireEvent.click(run);
    expect(screens[0].textContent).toContain('Hello from setup()');
  });

  it('platformio.ini builder: esp32dev is flagged as the wrong chip', async () => {
    const r = render(IniBuilderDemo);
    expect(r.getAllByText('Looks good. This builds and uploads to a SuperMini.').length).toBeGreaterThan(0);
    await fireEvent.change(r.getByRole('combobox', { name: 'board' }), { target: { value: 'esp32dev' } });
    expect(r.getByText('Won’t work')).toBeTruthy();
    expect(r.getByText('board = esp32dev')).toBeTruthy();
    await fireEvent.click(r.getByText('framework = arduino'));
    expect(r.container.textContent).toContain('arduino gives you setup(), loop()');
  });

  it('OTA: WiFi fails on a new board, works after a cable upload', async () => {
    const r = render(OtaDemo);
    await fireEvent.click(r.getByText('Upload over WiFi'));
    expect(r.container.textContent).toContain('the first upload must go by cable');
    await fireEvent.click(r.getByText('Upload by cable'));
    await fireEvent.click(r.getByText('Upload over WiFi'));
    expect(r.getByText('app1 · running')).toBeTruthy();
    expect(r.getByText('v2')).toBeTruthy();
  });

  // ESP32-C3: wireless and sleep (#0672)
  it('wifi: a 5 GHz-only network is not found; the antenna fix connects', async () => {
    const r = render(WifiConnectDemo);
    await fireEvent.click(r.getByRole('radio', { name: /5 GHz only/ }));
    expect(r.getByText('WL_NO_SSID_AVAIL', { selector: '.status code' })).toBeTruthy();
    await fireEvent.click(r.getByRole('radio', { name: /Home WiFi/ }));
    await fireEvent.click(r.getByRole('checkbox', { name: /antenna problem/ }));
    expect(r.getByRole('heading', { name: 'Never connects, even next to the router' })).toBeTruthy();
    await fireEvent.click(r.getByRole('checkbox', { name: /setTxPower/ }));
    expect(r.getByRole('heading', { name: 'Connected' })).toBeTruthy();
  });

  it('ble vs wifi: internet picks WiFi, audio is something the C3 cannot do', async () => {
    const r = render(BleWifiPicker);
    await fireEvent.click(r.getByRole('checkbox', { name: /internet/ }));
    expect(r.getByRole('heading', { name: 'WiFi' })).toBeTruthy();
    await fireEvent.click(r.getByRole('checkbox', { name: /audio/ }));
    expect(r.getByRole('heading', { name: 'The C3 can’t do this one' })).toBeTruthy();
  });

  it('ble basics: scan, connect and write 01 turns the board LED on', async () => {
    vi.useFakeTimers();
    try {
      const r = render(BleBasicsDemo);
      await fireEvent.click(r.getByRole('button', { name: 'Scan' }));
      await vi.advanceTimersByTimeAsync(800);
      await fireEvent.click(r.getByRole('button', { name: 'Connect' }));
      await fireEvent.click(r.getByRole('button', { name: 'Write 01' }));
      expect(r.getByRole('img', { name: 'SuperMini, LED on' })).toBeTruthy();
      await fireEvent.click(r.getByRole('button', { name: 'Subscribe' }));
      await fireEvent.click(r.getByRole('button', { name: 'Press BOOT on the board' }));
      expect(r.getByText(/Notify LED → 0x00/)).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  it('brownout: a thin cable resets on WiFi bursts; the capacitor steadies it', async () => {
    const r = render(BrownoutDemo);
    expect(r.getByRole('heading', { name: 'Brownout: the board resets' })).toBeTruthy();
    expect(r.getByText('Brownout detector was triggered')).toBeTruthy();
    await fireEvent.click(r.getByRole('checkbox', { name: /470 µF/ }));
    expect(r.queryByRole('heading', { name: 'Brownout: the board resets' })).toBeNull();
  });

  it('deep sleep: turning off WiFi and the board parts lengthens battery life', async () => {
    const r = render(DeepSleepDemo);
    const before = r.container.querySelector('.big')?.textContent;
    await fireEvent.click(r.getByRole('checkbox', { name: /power LED/ }));
    await fireEvent.click(r.getByRole('checkbox', { name: /WiFi/ }));
    const after = r.container.querySelector('.big')?.textContent;
    expect(before).toMatch(/days/);
    expect(after).toMatch(/days|years/);
    expect(after).not.toBe(before);
  });

  // ESP32-C3: pins and power (#0671)
  it('boot pins: holding a button on GPIO 9 through RESET lands in download mode', async () => {
    const r = render(C3BootPinsDemo);
    await fireEvent.click(r.getByRole('checkbox'));
    await fireEvent.click(r.getByText('Press RESET'));
    expect(r.getByRole('heading', { name: 'Download mode: your program does not start' })).toBeTruthy();
    await fireEvent.click(r.getByRole('button', { name: 'GPIO 4' }));
    await fireEvent.click(r.getByText('Press RESET'));
    expect(r.getByRole('heading', { name: 'Your program runs' })).toBeTruthy();
  });

  it('pin chooser: analog sensor marks GPIO 5 as one to avoid', async () => {
    const r = render(C3PinChooserDemo);
    await fireEvent.click(r.getByRole('radio', { name: 'Analog sensor' }));
    await fireEvent.click(r.getByRole('button', { name: 'GPIO 5: avoid' }));
    expect(r.getByText('GPIO 5: Avoid')).toBeTruthy();
    expect(r.container.textContent).toContain('Best pins for this: 0, 1, 3, 4');
  });

  it('analog input: 4 V on the pin warns about damage', async () => {
    const r = render(C3AnalogDemo);
    await fireEvent.input(r.getByRole('slider'), { target: { value: '4' } });
    expect(r.getByText('More than 3.3 V on a GPIO can damage the chip.')).toBeTruthy();
  });

  it('pwm: 50% duty looks about 73% bright, and 1 MHz leaves 6 bits', async () => {
    const r = render(C3PwmDemo);
    expect(r.container.textContent).toContain('It looks about 73% bright');
    await fireEvent.click(r.getByRole('radio', { name: '1 MHz' }));
    expect(r.container.textContent).toContain('up to 6-bit duty');
  });

  it('power: a LiPo on the 3.3 pin is a damage risk', async () => {
    const r = render(C3PowerDemo);
    await fireEvent.click(r.getByRole('radio', { name: /One LiPo cell/ }));
    await fireEvent.click(r.getByRole('radio', { name: '3.3 pin' }));
    expect(r.getByText('Damage risk')).toBeTruthy();
  });

  // Components: capacitors (#0691)
  it('capacitor types: 472 reads as 4.7 nF; a tantalum flips the polarity rule', async () => {
    const r = render(CapCodeDemo);
    await fireEvent.input(r.getByRole('textbox', { name: 'Marking on the capacitor' }), { target: { value: '472' } });
    expect(r.container.querySelector('.big')?.textContent).toBe('4.7 nF');
    expect(r.getByRole('img', { name: 'Ceramic capacitor marked 472' })).toBeTruthy();
    // A code is never printed on a can, even at 1 µF; a value with units at 1 µF is.
    await fireEvent.input(r.getByRole('textbox', { name: 'Marking on the capacitor' }), { target: { value: '105K' } });
    expect(r.getByRole('img', { name: 'Ceramic capacitor marked 105K' })).toBeTruthy();
    await fireEvent.input(r.getByRole('textbox', { name: 'Marking on the capacitor' }), { target: { value: '10µF 16V' } });
    expect(r.getByRole('img', { name: 'Electrolytic capacitor marked 10µF 16V' })).toBeTruthy();
    // Electrolytic starts with its stripe on the + rail: reversed.
    expect(r.getByText('Reversed')).toBeTruthy();
    await fireEvent.click(r.getByRole('button', { name: 'Flip the capacitor' }));
    expect(r.getByText('Polarity OK')).toBeTruthy();
    await fireEvent.click(r.getByRole('radio', { name: 'Tantalum' }));
    expect(r.getByText('Reversed')).toBeTruthy();
  });

  it('rc charging: 10 kΩ × 100 µF is τ = 1 s, and at 1τ it is at 63%', async () => {
    const r = render(RcChargeDemo);
    expect(r.container.textContent).toContain('τ = R × C = 1 s');
    await fireEvent.click(r.getByRole('button', { name: 'Charge (switch to 3.3 V)' }));
    await fireEvent.input(r.getByRole('slider', { name: /Time since the switch/ }), { target: { value: '1' } });
    expect(r.container.querySelector('.big')?.textContent).toBe('2.09 V');
    expect(r.container.textContent).toContain('63% of the way');
    await fireEvent.input(r.getByRole('slider', { name: /Resistor R/ }), { target: { value: '0' } });
    expect(r.container.textContent).toContain('τ = R × C = 100 ms');
  });

  it('decoupling: no capacitor dips below 3.0 V; a close one holds; a far one does not', async () => {
    const r = render(DecouplingDemo);
    expect(r.getByRole('heading', { name: 'Dips below 3.0 V' })).toBeTruthy();
    await fireEvent.click(r.getByRole('checkbox', { name: /decoupling capacitor/ }));
    expect(r.getByRole('heading', { name: 'Steady' })).toBeTruthy();
    await fireEvent.input(r.getByRole('slider', { name: /Distance/ }), { target: { value: '30' } });
    expect(r.getByRole('heading', { name: 'Holding, but close' })).toBeTruthy();
    expect(r.container.textContent).toContain('Move it closer to the power pin');
    await fireEvent.input(r.getByRole('slider', { name: /Distance/ }), { target: { value: '40' } });
    expect(r.getByRole('heading', { name: 'Dips below 3.0 V' })).toBeTruthy();
  });

  // Components: transistors and MOSFETs (#0692)
  it('bjt switch: 10 kΩ base resistor leaves it not saturated; pin low turns it off', async () => {
    const r = render(BjtSwitchDemo);
    expect(r.getByRole('heading', { name: 'Saturated: fully on' })).toBeTruthy();
    const [rb] = r.getAllByRole('slider');
    await fireEvent.input(rb, { target: { value: '12' } });
    expect(r.getByRole('heading', { name: 'Not saturated' })).toBeTruthy();
    expect(r.getByRole('img', { name: /10 kΩ base resistor/ })).toBeTruthy();
    await fireEvent.click(r.getByRole('button', { name: /GPIO 4 is HIGH/ }));
    expect(r.getByRole('heading', { name: 'Off' })).toBeTruthy();
    await fireEvent.click(r.getByRole('radio', { name: '2N3904' }));
    expect(r.getByRole('img', { name: /2N3904 in TO-92.*E, B, C/ })).toBeTruthy();
  });

  it('mosfet switch: IRFZ44N barely turns on at 3.3 V, fully at 10 V; no pull-down floats', async () => {
    const r = render(MosfetSwitchDemo);
    expect(r.getByRole('heading', { name: 'Fully on' })).toBeTruthy();
    await fireEvent.click(r.getByRole('radio', { name: /IRFZ44N/ }));
    expect(r.getByRole('heading', { name: 'Barely on' })).toBeTruthy();
    await fireEvent.click(r.getByRole('radio', { name: '10 V (gate driver)' }));
    expect(r.getByRole('heading', { name: 'Fully on' })).toBeTruthy();
    await fireEvent.click(r.getByRole('radio', { name: /Board booting/ }));
    expect(r.getByRole('heading', { name: 'Off' })).toBeTruthy();
    await fireEvent.click(r.getByRole('checkbox', { name: /pull-down/ }));
    expect(r.getByRole('heading', { name: 'Floating: it could be half on' })).toBeTruthy();
  });

  it('mosfet switch shows junction temperature in °F by default', async () => {
    resetTempUnitForTest();
    const r = render(MosfetSwitchDemo);
    expect(r.container.textContent).toMatch(/Temperature\d+ °F/);
    expect(r.container.textContent).not.toContain('°C');
    setTempUnit('C');
    await tick();
    expect(r.container.textContent).toMatch(/Temperature\d+ °C/);
    resetTempUnitForTest();
  });

  it('flyback: no diode breaks the transistor down; band to + clamps; reversed shorts when on', async () => {
    const r = render(FlybackDemo);
    await fireEvent.click(r.getByRole('button', { name: 'Switch off' }));
    expect(r.getByRole('heading', { name: 'Spike: the transistor breaks down' })).toBeTruthy();
    await fireEvent.click(r.getByRole('radio', { name: 'Diode, band (cathode) to +' }));
    expect(r.getByRole('heading', { name: 'Clamped at 5.7 V' })).toBeTruthy();
    expect(r.getByRole('img', { name: /band to the positive supply/ })).toBeTruthy();
    await fireEvent.click(r.getByRole('radio', { name: 'Diode reversed' }));
    await fireEvent.click(r.getByRole('button', { name: 'Switch on' }));
    expect(r.getByRole('heading', { name: 'Short circuit!' })).toBeTruthy();
  });

  it('bjt vs mosfet: 50 mA is either, 500 mA wants a MOSFET', async () => {
    const r = render(SwitchPickerDemo);
    expect(r.getByRole('heading', { name: 'Either works' })).toBeTruthy();
    await fireEvent.input(r.getByRole('slider'), { target: { value: '4' } });
    expect(r.getByRole('heading', { name: 'Use a logic-level MOSFET' })).toBeTruthy();
    expect(r.getByRole('region', { name: 'NPN transistor: not suitable' })).toBeTruthy();
  });

  // Components: resistors and LEDs (#0690)
  it('resistor bands: tapping a band changes the value, and typing a value sets the bands', async () => {
    const r = render(ResistorBandsDemo);
    expect(r.container.textContent).toContain('220 Ω ± 5%');
    await fireEvent.click(r.getByRole('button', { name: /Band 3, multiplier: brown/ }));
    expect(r.container.textContent).toContain('2.2 kΩ ± 5%');
    await fireEvent.input(r.getByRole('textbox', { name: /type a value/i }), { target: { value: '4k7' } });
    expect(r.getByRole('button', { name: /Band 1, digit 1: yellow/ })).toBeTruthy();
    expect(r.getByRole('button', { name: /Band 2, digit 2: violet/ })).toBeTruthy();
    expect(r.container.textContent).toContain('4.7 kΩ ± 5%');
    await fireEvent.click(r.getByRole('radio', { name: '5 bands' }));
    expect(r.getAllByRole('button', { name: /^Band \d/ })).toHaveLength(5);
    expect(r.container.textContent).toContain('4.7 kΩ ± 1%');
  });

  it('ohms law: 12 V across 10 Ω is far past 1/4 W', async () => {
    const r = render(OhmsLawDemo);
    expect(r.container.textContent).toContain('under 1/4 W');
    await fireEvent.input(r.getByRole('slider', { name: /Voltage/ }), { target: { value: '12' } });
    await fireEvent.input(r.getByRole('slider', { name: /R1/ }), { target: { value: '0' } });
    expect(r.container.textContent).toContain('R1 is turning 14.4 W into heat');
    await fireEvent.click(r.getByRole('radio', { name: 'Series' }));
    expect(r.getByRole('slider', { name: /R2/ })).toBeTruthy();
    expect(r.getByRole('rowheader', { name: 'Total' })).toBeTruthy();
  });

  it('led basics: backwards stays dark, no resistor is too much current', async () => {
    const r = render(LedBasicsDemo);
    expect(r.getByRole('heading', { name: 'Lit: 5.91 mA' })).toBeTruthy();
    await fireEvent.click(r.getByRole('button', { name: 'Flip the LED' }));
    expect(r.getByRole('heading', { name: 'Dark: the LED is backwards' })).toBeTruthy();
    await fireEvent.click(r.getByRole('button', { name: 'Flip the LED' }));
    await fireEvent.click(r.getByRole('checkbox', { name: /resistor/ }));
    expect(r.getByRole('heading', { name: 'No resistor: too much current' })).toBeTruthy();
  });

  it('led resistor: red at 10 mA on a pin needs 150 Ω; 30 mA warns about the pin', async () => {
    const r = render(LedResistorDemo);
    expect(r.container.textContent).toContain('= 130 Ω');
    expect(r.container.querySelector('dd strong')?.textContent).toBe('150 Ω');
    await fireEvent.input(r.getByRole('slider', { name: /Target current/ }), { target: { value: '30' } });
    expect(r.container.textContent).toContain('from an ESP32 pin');
    await fireEvent.click(r.getByRole('radio', { name: /Blue/ }));
    await fireEvent.click(r.getByRole('radio', { name: 'Custom' }));
    await fireEvent.input(r.getByRole('spinbutton', { name: /Supply voltage/ }), { target: { value: '3' } });
    expect(r.getByRole('heading', { name: 'Can’t light it' })).toBeTruthy();
  });

  // Raspberry Pi: setup, WiFi and Bookworm (#0719)
  it('pi imager: turning off WiFi never joins; turning off SSH refuses connections', async () => {
    const r = render(PiImagerDemo);
    expect(r.getByRole('heading', { name: 'Ready: log in over SSH' })).toBeTruthy();
    await fireEvent.click(r.getByRole('checkbox', { name: /Wireless LAN/ }));
    expect(r.getByRole('heading', { name: 'Never joins the network' })).toBeTruthy();
    await fireEvent.click(r.getByRole('checkbox', { name: /Wireless LAN/ }));
    await fireEvent.click(r.getByRole('checkbox', { name: /Enable SSH/ }));
    expect(r.getByRole('heading', { name: 'On the network, but it refuses connections' })).toBeTruthy();
    expect(r.container.textContent).toContain('Connection refused');
  });

  it('pi wifi: a Zero 2 W cannot see a 5 GHz-only network; a Pi 4 can', async () => {
    const r = render(PiWifiBandDemo);
    await fireEvent.click(r.getByRole('radio', { name: /5 GHz only/ }));
    await fireEvent.click(r.getByRole('radio', { name: /Zero 2 W/ }));
    expect(r.getByRole('heading', { name: 'Never appears' })).toBeTruthy();
    await fireEvent.click(r.getByRole('radio', { name: /Pi 4/ }));
    expect(r.container.textContent).toContain('freq: 5180');
  });

  it('pi bookworm: pip shows the externally-managed error and the venv fix', async () => {
    const r = render(PiBookwormDemo);
    await fireEvent.click(r.getByRole('radio', { name: /pip install/ }));
    expect(r.container.textContent).toContain('externally-managed-environment');
    expect(r.container.textContent).toContain('python3 -m venv');
    await fireEvent.click(r.getByRole('radio', { name: /config\.txt/ }));
    expect(r.container.textContent).toContain('/boot/firmware/config.txt');
  });

  // Raspberry Pi: power and SD cards (#0720)
  it('pi undervoltage: a thin cable drops a busy Pi 4 under; the official supply fixes it', async () => {
    const r = render(PiUndervoltageDemo);
    expect(r.getByRole('heading', { name: 'Close to the line' })).toBeTruthy();
    await fireEvent.click(r.getByRole('radio', { name: /Thin long cable/ }));
    expect(r.getByRole('heading', { name: 'Under-voltage: the Pi slows down' })).toBeTruthy();
    expect(r.container.textContent).toContain('throttled=0x50005');
    await fireEvent.click(r.getByRole('radio', { name: 'Official Raspberry Pi supply' }));
    expect(r.getByRole('heading', { name: 'Plenty of margin' })).toBeTruthy();
    expect((r.getByRole('radio', { name: /Thin long cable/ }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('throttled decoder: 0x50005, bad input, and the 0x0 preset', async () => {
    const r = render(ThrottledDecoder);
    expect(r.getByRole('heading', { name: 'Power problem now' })).toBeTruthy();
    expect(r.container.textContent).toContain('Throttling has happened since boot');
    await fireEvent.input(r.getByRole('textbox', { name: 'get_throttled value' }), { target: { value: 'zebra' } });
    expect(r.getByRole('alert').textContent).toContain('isn’t a hex number');
    await fireEvent.click(r.getByRole('button', { name: '0x0' }));
    expect(r.getByRole('heading', { name: 'All clear' })).toBeTruthy();
  });

  it('sd card: a cut during the flash rewrite corrupts; the overlay protects', async () => {
    const r = render(SdWriteDemo);
    const slider = r.getByRole('slider', { name: /When the power is cut/ });
    await fireEvent.input(slider, { target: { value: '20' } });
    expect(r.getByRole('heading', { name: 'Change lost, card fine' })).toBeTruthy();
    await fireEvent.input(slider, { target: { value: '65' } });
    expect(r.getByRole('heading', { name: 'Saved: the journal finishes it at boot' })).toBeTruthy();
    await fireEvent.input(slider, { target: { value: '90' } });
    expect(r.getByRole('heading', { name: 'Corrupted: other files damaged' })).toBeTruthy();
    await fireEvent.click(r.getByRole('checkbox', { name: /Overlay file system/ }));
    expect(r.getByRole('heading', { name: 'Card untouched: the save was in memory' })).toBeTruthy();
  });

  // Raspberry Pi: Volumio and RGB panels (#0721)
  it('volumio: AirPlay is free, Bluetooth needs Premium, a Zero 2W has no jack', async () => {
    const r = render(VolumioDemo);
    await fireEvent.click(r.getByRole('radio', { name: /iPhone or Mac/ }));
    expect(r.container.textContent).toContain('Free in Volumio OS');
    await fireEvent.click(r.getByRole('radio', { name: /Bluetooth/ }));
    expect(r.container.textContent).toContain('Needs Volumio Premium');
    await fireEvent.click(r.getByRole('radio', { name: 'Headphone jack' }));
    expect(r.getByRole('heading', { name: 'No headphone jack on a Zero 2W' })).toBeTruthy();
    await fireEvent.click(r.getByRole('radio', { name: 'Pi 4' }));
    expect(r.queryByRole('heading', { name: 'No headphone jack on a Zero 2W' })).toBeNull();
    expect(r.getByRole('link', { name: /2.4 GHz/ }).getAttribute('href')).toBe('/guides/pi-wifi-2-4ghz');
  });

  it('rgb panel: sound, mapping and slowdown fixes bring the panel up, steady', async () => {
    const r = render(RgbMatrixDemo);
    const h = () => r.getByRole('heading', { level: 3 }).textContent ?? '';
    expect(h()).toMatch(/won’t start/);
    await fireEvent.click(r.getByRole('checkbox', { name: /Onboard sound off/ }));
    expect(h()).toMatch(/mapping/);
    await fireEvent.click(r.getByRole('radio', { name: 'adafruit-hat' }));
    expect(h()).toMatch(/Shimmer/);
    await fireEvent.click(within(r.getByRole('radiogroup', { name: /slowdown/ })).getByRole('radio', { name: '2' }));
    expect(h()).toMatch(/Uneven/);
    await fireEvent.click(r.getByRole('checkbox', { name: /Core 3/ }));
    expect(h()).toBe('Steady');
    expect(r.container.textContent).toContain('178 Hz');
    expect(r.container.querySelectorAll('animate, animateTransform, animateMotion').length).toBe(0);
    expect(r.container.querySelectorAll('.cell').length).toBe(512);
  });

  // Raspberry Pi batch 2: Zero audio, RGB panel basics and power (#0726)
  it('zero audio: a DAC HAT is an overlay line; Bluetooth is poor for delay; a USB DAC links the ports guide', async () => {
    const r = render(ZeroAudioDemo);
    const h = () => r.getByRole('heading', { level: 3 }).textContent ?? '';
    expect(h()).toBe('DAC HAT: the cleanest wired sound');
    expect(r.container.textContent).toContain('dtoverlay=hifiberry-dac');
    expect(r.container.textContent).toContain('Example only');
    await fireEvent.click(r.getByRole('radio', { name: 'Bluetooth' }));
    expect(h()).toBe('Bluetooth: no wires, some delay');
    await fireEvent.click(r.getByRole('radio', { name: 'Low delay' }));
    expect(r.container.textContent).toContain('A poor pick when low delay matters most; try a DAC HAT, a USB DAC or HDMI.');
    await fireEvent.click(r.getByRole('radio', { name: 'USB DAC' }));
    expect(r.getByRole('link', { name: /micro-USB port/ }).getAttribute('href')).toBe('/guides/pi-zero-usb-ports');
    expect(r.getByRole('link', { name: /Volumio/ }).getAttribute('href')).toBe('/guides/pi-volumio-streaming');
  });

  it('rgb layout: a chain of six flickers; three parallel chains of two do not; a single-chain adapter refuses', async () => {
    const r = render(RgbLayoutDemo);
    const h = () => r.getByRole('heading', { level: 3 }).textContent ?? '';
    expect(h()).toBe('Fine to the eye: 178 Hz');
    expect(r.container.textContent).toContain('64×32 pixels');
    const slider = r.getByRole('slider', { name: /Panels in each chain/ });
    await fireEvent.input(slider, { target: { value: '6' } });
    expect(h()).toBe('Flicker likely: 81 Hz');
    expect(r.container.textContent).toContain('384×32');
    expect(r.container.textContent).toContain('--led-chain=6');
    await fireEvent.input(slider, { target: { value: '2' } });
    await fireEvent.click(r.getByRole('radio', { name: 'Three-output adapter board' }));
    await fireEvent.click(within(r.getByRole('radiogroup', { name: /Parallel chains/ })).getByRole('radio', { name: '3' }));
    expect(h()).toBe('Fine to the eye: 144 Hz');
    expect(r.container.textContent).toContain('128×96');
    expect(r.container.querySelectorAll('.panel').length).toBe(6);
    await fireEvent.click(r.getByRole('radio', { name: 'Single-chain HAT or bonnet' }));
    expect(h()).toBe('One chain only on this adapter');
    expect(r.container.querySelectorAll('animate, animateTransform, animateMotion').length).toBe(0);
  });

  it('rgb power: a picture fits 4 A, full white overloads it, a 20 A supply has room', async () => {
    const r = render(RgbPowerDemo);
    const h = () => r.getByRole('heading', { level: 3 }).textContent ?? '';
    expect(h()).toBe('OK for this content; full white would overload the supply');
    expect(r.container.textContent).toContain('2.8 A');
    expect(r.container.textContent).toContain('8.0 A');
    await fireEvent.click(r.getByRole('radio', { name: 'Full white' }));
    expect(h()).toBe('Over the supply’s rating');
    await fireEvent.click(r.getByRole('radio', { name: '20 A' }));
    expect(h()).toBe('Plenty of supply');
    expect(r.container.textContent).toContain('Never power panels from the Pi’s 5 V pin');
    expect(r.getByRole('link', { name: /Power budgets for LED strips/ }).getAttribute('href')).toBe('/guides/led-power-budget');
  });
});
