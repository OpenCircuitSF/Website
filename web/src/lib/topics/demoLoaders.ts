// #0717: one lazy loader per topic guide's demo, keyed by slug. Kept out of
// topics.ts so a guide page can fetch its demo in parallel with TopicGuide.
// routeViews.ts imports this module lazily too, so neither the demos nor this
// map is in the entry chunk.
import type { Component } from 'svelte';

export type DemoModule = { default: Component };

export const TOPIC_DEMOS: Readonly<Record<string, () => Promise<DemoModule>>> = {
  'one-wire-many-leds': () => import('../guide/ChainDemo.svelte'),
  'delay-vs-millis': () => import('./DelayDemo.svelte'),
  'baud-rate': () => import('./BaudDemo.svelte'),
  'charge-only-cables': () => import('./CableDemo.svelte'),
  'led-power-budget': () => import('./PowerDemo.svelte'),
  'esp32-c3-supermini-pinout': () => import('./PinoutExplorer.svelte'),
  'multimeter-probes': () => import('./ProbesDemo.svelte'),
  'multimeter-continuity': () => import('./ContinuityDemo.svelte'),
  'multimeter-voltage': () => import('./VoltageDemo.svelte'),
  'multimeter-current': () => import('./CurrentDemo.svelte'),
  'soldering-solder-wire': () => import('./SolderWireDemo.svelte'),
  'soldering-iron-temperature': () => import('./IronTempDemo.svelte'),
  'soldering-tinning': () => import('./TinningDemo.svelte'),
  'soldering-good-joint': () => import('./JointQuiz.svelte'),
  'soldering-tip-care': () => import('./TipCareDemo.svelte'),
  'soldering-header-pins': () => import('./HeaderPinsDemo.svelte'),
  'esp32-c3-boot-reset-buttons': () => import('./BootButtonsDemo.svelte'),
  'esp32-c3-wont-upload': () => import('./UploadDoctor.svelte'),
  'esp32-c3-serial-output': () => import('./SerialOutputDemo.svelte'),
  'platformio-first-project': () => import('./IniBuilderDemo.svelte'),
  'esp32-ota-updates': () => import('./OtaDemo.svelte'),
  'esp32-c3-wifi': () => import('./WifiConnectDemo.svelte'),
  'ble-vs-wifi': () => import('./BleWifiPicker.svelte'),
  'ble-basics': () => import('./BleBasicsDemo.svelte'),
  'wifi-brownout-resets': () => import('./BrownoutDemo.svelte'),
  'esp32-c3-deep-sleep': () => import('./DeepSleepDemo.svelte'),
  'esp32-c3-boot-pins': () => import('./C3BootPinsDemo.svelte'),
  'esp32-c3-reserved-pins': () => import('./C3PinChooserDemo.svelte'),
  'esp32-c3-analog-input': () => import('./C3AnalogDemo.svelte'),
  'esp32-c3-pwm': () => import('./C3PwmDemo.svelte'),
  'esp32-c3-power': () => import('./C3PowerDemo.svelte'),
  'capacitor-types': () => import('./CapCodeDemo.svelte'),
  'capacitor-rc-charging': () => import('./RcChargeDemo.svelte'),
  'decoupling-capacitors': () => import('./DecouplingDemo.svelte'),
  'bjt-transistor-switch': () => import('./BjtSwitchDemo.svelte'),
  'mosfet-switch': () => import('./MosfetSwitchDemo.svelte'),
  'flyback-diodes': () => import('./FlybackDemo.svelte'),
  'bjt-vs-mosfet': () => import('./SwitchPickerDemo.svelte'),
  'resistor-color-codes': () => import('./ResistorBandsDemo.svelte'),
  'ohms-law-resistors': () => import('./OhmsLawDemo.svelte'),
  'led-basics': () => import('./LedBasicsDemo.svelte'),
  'led-resistor-calculator': () => import('./LedResistorDemo.svelte'),
  // Raspberry Pi: setup, WiFi and Bookworm (#0719)
  'pi-imager-setup': () => import('./PiImagerDemo.svelte'),
  'pi-wifi-2-4ghz': () => import('./PiWifiBandDemo.svelte'),
  'pi-bookworm-changes': () => import('./PiBookwormDemo.svelte'),
  // Raspberry Pi: power and SD cards (#0720)
  'pi-undervoltage': () => import('./PiUndervoltageDemo.svelte'),
  'pi-sd-card-corruption': () => import('./SdWriteDemo.svelte'),
  // Raspberry Pi: Volumio and RGB panels (#0721)
  'pi-volumio-streaming': () => import('./VolumioDemo.svelte'),
  'pi-rgb-matrix-flicker': () => import('./RgbMatrixDemo.svelte'),
  // Raspberry Pi batch 2: Zero audio, RGB panel basics and power (#0726)
  'pi-zero-audio-out': () => import('./ZeroAudioDemo.svelte'),
  'pi-rgb-matrix-basics': () => import('./RgbLayoutDemo.svelte'),
  'pi-rgb-matrix-power': () => import('./RgbPowerDemo.svelte'),
};
