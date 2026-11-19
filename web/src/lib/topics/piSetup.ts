// Raspberry Pi guides (#0719): setting up a headless Pi with Raspberry Pi
// Imager, why a Pi never joins a 5 GHz network, and what moved in Raspberry
// Pi OS Bookworm. The demos are thin; every outcome they show is computed
// here so it can be unit-tested without a DOM.
//
// The facts follow Raspberry Pi's own documentation
// (raspberrypi.com/documentation: "Getting started" / Imager,
// "Configuration" / networking, "Raspberry Pi OS" / Python). Items written
// from memory are listed under #0719's Assumptions.
//
// Illustrations (#0722):
//   pi-imager-setup     — Imager's OS customisation screen
//   pi-wifi-2-4ghz      — a router with 2.4 and 5 GHz rings and a Zero 2 W
//   pi-bookworm-changes — the SD card's two partitions, /boot/firmware and /

// ---------------------------------------------------------------------------
// 1. Imager (pi-imager-setup)

export type SettingId = 'hostname' | 'user' | 'wifi' | 'locale' | 'ssh';
export interface ImagerSetting {
  id: SettingId;
  label: string;
  detail: string;
}

export const IMAGER_SETTINGS: ImagerSetting[] = [
  { id: 'hostname', label: 'Hostname', detail: 'the name it answers to, like mypi.local' },
  { id: 'user', label: 'Username and password', detail: 'there is no default pi user any more' },
  { id: 'wifi', label: 'Wireless LAN', detail: 'network name, password and WiFi country' },
  { id: 'locale', label: 'Locale settings', detail: 'time zone and keyboard layout' },
  { id: 'ssh', label: 'Enable SSH', detail: 'so you can log in over the network' },
];

export type SshAuth = 'password' | 'key';
export interface ImagerChoice {
  on: Record<SettingId, boolean>;
  sshAuth: SshAuth;
  hostname: string;
}

export const DEFAULT_CHOICE: ImagerChoice = {
  on: { hostname: true, user: true, wifi: true, locale: true, ssh: true },
  sshAuth: 'password',
  hostname: 'mypi',
};

export const DEMO_USER = 'maker';

/** 1–63 characters of a–z, A–Z, 0–9 and '-', not starting or ending with '-'. */
export function isValidHostname(name: string): boolean {
  return /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/.test(name);
}

export function hostFor(c: ImagerChoice): string {
  return c.on.hostname && isValidHostname(c.hostname) ? `${c.hostname}.local` : 'raspberrypi.local';
}

export type BootTone = 'good' | 'meh' | 'bad';
export interface FirstBoot {
  tone: BootTone;
  /** On the network at all. */
  joins: boolean;
  canLogIn: boolean;
  /** Headline, used as the demo's <h3>. */
  title: string;
  /** One line per setting that is off, in IMAGER_SETTINGS order. */
  problems: string[];
  /** What the laptop shows; commands are prefixed '$ '. */
  terminal: string[];
}

const PROBLEM: Record<SettingId, string> = {
  hostname:
    'No hostname: it answers to raspberrypi.local. Fine for one Pi, but two on the same network clash.',
  user: 'No user: it boots to a set-up prompt on its HDMI screen asking for a new user, and nobody is looking at that screen.',
  wifi: 'No WiFi details: it never joins your network. A Pi 4 with an Ethernet cable plugged in would; a Zero 2 W has no Ethernet port.',
  locale: 'No locale: the time zone and keyboard layout stay at the image’s defaults, which are British, not yours.',
  ssh: 'No SSH: it joins the network, but nothing is listening for you to log in.',
};

export function firstBoot(c: ImagerChoice): FirstBoot {
  const host = hostFor(c);
  const shortHost = host.replace(/\.local$/, '');
  const problems = IMAGER_SETTINGS.filter((s) => !c.on[s.id]).map((s) => PROBLEM[s.id]);
  const ping = [`$ ping ${host}`, `64 bytes from ${host}: icmp_seq=0 ttl=64 time=4.1 ms`];
  let out: FirstBoot;
  if (!c.on.wifi) {
    out = {
      tone: 'bad',
      joins: false,
      canLogIn: false,
      title: 'Never joins the network',
      problems,
      terminal: [`$ ping ${host}`, `ping: cannot resolve ${host}: Unknown host`],
    };
  } else if (!c.on.ssh) {
    out = {
      tone: 'bad',
      joins: true,
      canLogIn: false,
      title: 'On the network, but it refuses connections',
      problems,
      terminal: [...ping, `$ ssh ${DEMO_USER}@${host}`, `ssh: connect to host ${host} port 22: Connection refused`],
    };
  } else if (!c.on.user) {
    out = {
      tone: 'bad',
      joins: true,
      canLogIn: false,
      title: 'On the network, but there is no one to log in as',
      problems,
      terminal: [...ping, '(no account to log in with)'],
    };
  } else {
    out = {
      tone: 'good',
      joins: true,
      canLogIn: true,
      title: 'Ready: log in over SSH',
      problems,
      terminal: [
        `$ ssh ${DEMO_USER}@${host}`,
        c.sshAuth === 'password' ? `${DEMO_USER}@${host}'s password:` : `${DEMO_USER}@${shortHost}:~ $`,
      ],
    };
  }
  if (out.tone === 'good' && (!c.on.hostname || !c.on.locale)) out.tone = 'meh';
  return out;
}

// ---------------------------------------------------------------------------
// 2. WiFi bands (pi-wifi-2-4ghz)

export type Band = '2.4' | '5';
export type BoardId = 'zero2w' | 'zerow' | 'pi3b' | 'pi3bplus' | 'pi4' | 'pi5';
export interface PiBoard {
  id: BoardId;
  label: string;
  bands: Band[];
}

export const PI_BOARDS: PiBoard[] = [
  { id: 'zero2w', label: 'Zero 2 W', bands: ['2.4'] },
  { id: 'zerow', label: 'Zero W', bands: ['2.4'] },
  { id: 'pi3b', label: 'Pi 3 B', bands: ['2.4'] },
  { id: 'pi3bplus', label: 'Pi 3 B+', bands: ['2.4', '5'] },
  { id: 'pi4', label: 'Pi 4', bands: ['2.4', '5'] },
  { id: 'pi5', label: 'Pi 5', bands: ['2.4', '5'] },
];

function board(id: BoardId): PiBoard {
  const b = PI_BOARDS.find((x) => x.id === id);
  if (!b) throw new Error(`piSetup: unknown board ${id}`);
  return b;
}

export function hears(id: BoardId, band: Band): boolean {
  return board(id).bands.includes(band);
}

export type RouterId = 'one-name' | 'split' | 'only-24' | 'only-5' | 'hotspot';
export interface RouterSetup {
  id: RouterId;
  label: string;
  detail: string;
  networks: { ssid: string; bands: Band[] }[];
}

export const ROUTERS: RouterSetup[] = [
  { id: 'one-name', label: 'One name for both bands', detail: 'most home routers', networks: [{ ssid: 'Home', bands: ['2.4', '5'] }] },
  {
    id: 'split',
    label: 'Separate 2.4 and 5 GHz names',
    detail: 'Home and Home-5G',
    networks: [
      { ssid: 'Home', bands: ['2.4'] },
      { ssid: 'Home-5G', bands: ['5'] },
    ],
  },
  { id: 'only-24', label: '2.4 GHz only', detail: 'older or simple routers', networks: [{ ssid: 'Home', bands: ['2.4'] }] },
  { id: 'only-5', label: '5 GHz only', detail: 'the 2.4 GHz band switched off', networks: [{ ssid: 'Home', bands: ['5'] }] },
  { id: 'hotspot', label: 'Phone hotspot, 2.4 GHz', detail: 'just for first setup', networks: [{ ssid: 'Phone', bands: ['2.4'] }] },
];

export function routerById(id: RouterId): RouterSetup {
  const r = ROUTERS.find((x) => x.id === id);
  if (!r) throw new Error(`piSetup: unknown router ${id}`);
  return r;
}

/** Channel 6 on 2.4 GHz, channel 36 on 5 GHz. */
export const FREQ_MHZ: Record<Band, number> = { '2.4': 2437, '5': 5180 };

export interface JoinOutcome {
  joins: boolean;
  band: Band | null;
  tone: 'good' | 'meh' | 'bad';
  title: string;
  reason: string;
  /** What iwgetid and iw print once joined; empty when not joined. */
  check: string[];
}

function bandName(b: Band): string {
  return `${b} GHz`;
}

export function joinOutcome(boardId: BoardId, routerId: RouterId, typed?: string): JoinOutcome {
  const b = board(boardId);
  const r = routerById(routerId);
  const net = r.networks.find((n) => n.ssid === typed) ?? r.networks[0];
  const usable = net.bands.filter((x) => b.bands.includes(x));
  if (usable.length === 0) {
    const what = b.bands.length === 1 ? `only has ${bandName(b.bands[0])} WiFi` : 'can’t hear that band';
    return {
      joins: false,
      band: null,
      tone: 'bad',
      title: 'Never appears',
      reason: `The ${b.label} ${what}. It can’t see ${net.ssid} (${net.bands.map(bandName).join(' and ')}) at all, so there is no error, just no Pi.`,
      check: [],
    };
  }
  const band: Band = usable.includes('5') ? '5' : '2.4';
  const check = [
    '$ iwgetid -r',
    net.ssid,
    '$ iw dev wlan0 link',
    'Connected to 3c:84:6a:12:34:56 (on wlan0)',
    `\tSSID: ${net.ssid}`,
    `\tfreq: ${FREQ_MHZ[band]}`,
  ];
  let tone: JoinOutcome['tone'] = 'good';
  let title = `Joins ${net.ssid} on ${bandName(band)}`;
  let reason = `The ${b.label} hears ${b.bands.map(bandName).join(' and ')}, and ${net.ssid} broadcasts on ${bandName(band)}.`;
  if (r.id === 'one-name' && net.bands.length > 1 && usable.length > 1) {
    tone = 'meh';
    title = 'Joins, usually on 5 GHz';
    reason = `The ${b.label} hears both bands and usually picks 5 GHz. Band steering, where the router pushes devices between bands, can cause odd drops; if it does, give the 2.4 GHz band its own name.`;
  } else if (r.id === 'one-name') {
    reason = `The ${b.label} only hears 2.4 GHz, so it simply joins ${net.ssid} on 2.4 GHz and ignores the 5 GHz half.`;
  } else if (r.id === 'hotspot') {
    reason = `A 2.4 GHz hotspot is handy for first setup: every Pi can hear it. Add the home network later with nmcli.`;
  }
  return { joins: true, band, tone, title, reason, check };
}

// ---------------------------------------------------------------------------
// 3. Bookworm (pi-bookworm-changes)

export type StepId = 'boot-files' | 'wifi' | 'static-ip' | 'pip' | 'camera' | 'desktop';
export interface OldStep {
  id: StepId;
  /** Button text. */
  label: string;
  /** The old tutorial's lines. */
  oldCmd: string[];
  /** What you see on Bookworm. */
  now: string[];
  /** What to type instead. */
  fix: string[];
  why: string;
  /** SVG label: the old thing, crossed out. */
  oldBox: string;
  /** SVG label: what replaced it. */
  newBox: string;
}

export const BOOKWORM_STEPS: OldStep[] = [
  {
    id: 'boot-files',
    label: 'Edit config.txt',
    oldCmd: ['sudo nano /boot/config.txt'],
    now: ['(a short note saying the file has moved to /boot/firmware/config.txt)', 'Your edits here do nothing.'],
    fix: ['sudo nano /boot/firmware/config.txt', 'sudo nano /boot/firmware/cmdline.txt   # still one single line'],
    why: 'The boot partition is now mounted at /boot/firmware. config.txt and cmdline.txt live there; the old path is only a pointer.',
    oldBox: '/boot/config.txt',
    newBox: '/boot/firmware/config.txt',
  },
  {
    id: 'wifi',
    label: 'Add WiFi in wpa_supplicant.conf',
    oldCmd: ['sudo nano /etc/wpa_supplicant/wpa_supplicant.conf', '# or drop wpa_supplicant.conf on the boot partition'],
    now: ['(no error: the file is ignored)', 'The Pi never joins the network.'],
    fix: ['nmcli dev wifi list', 'sudo nmcli dev wifi connect "Home" password "your-password"', 'sudo nmtui   # or use the menu'],
    why: 'NetworkManager runs WiFi on Bookworm, so wpa_supplicant.conf is no longer read. Use nmcli, or set the WiFi in Imager before the first boot.',
    oldBox: 'wpa_supplicant.conf',
    newBox: 'NetworkManager',
  },
  {
    id: 'static-ip',
    label: 'Static IP in dhcpcd.conf',
    oldCmd: [
      'sudo nano /etc/dhcpcd.conf',
      '  interface wlan0',
      '  static ip_address=192.168.1.50/24',
      'sudo systemctl restart dhcpcd',
    ],
    now: ['$ sudo systemctl restart dhcpcd', 'Failed to restart dhcpcd.service: Unit dhcpcd.service not found.'],
    fix: [
      'nmcli con show   # find the connection name',
      'sudo nmcli con mod "preconfigured" ipv4.method manual ipv4.addresses 192.168.1.50/24 ipv4.gateway 192.168.1.1 ipv4.dns 192.168.1.1',
      'sudo nmcli con up "preconfigured"',
    ],
    why: 'dhcpcd is gone; NetworkManager hands out addresses too. Set a fixed address on the connection itself. Imager’s WiFi connection is usually called preconfigured, but check with nmcli con show.',
    oldBox: 'dhcpcd',
    newBox: 'NetworkManager',
  },
  {
    id: 'pip',
    label: 'pip install a library',
    oldCmd: ['pip install requests'],
    now: ['$ pip install requests', 'error: externally-managed-environment', '', '× This environment is externally managed'],
    fix: [
      'python3 -m venv --system-site-packages ~/env',
      'source ~/env/bin/activate',
      'pip install requests',
      '# or, without a venv:',
      'sudo apt install python3-requests',
    ],
    why: 'Since Debian 12, the system Python belongs to apt, so pip refuses to install into it. A virtual environment is yours to change; --system-site-packages keeps apt’s libraries, such as gpiozero, visible inside it.',
    oldBox: 'system Python',
    newBox: 'a venv',
  },
  {
    id: 'camera',
    label: 'Take a photo with raspistill',
    oldCmd: ['raspistill -o test.jpg'],
    now: ['$ raspistill -o test.jpg', 'bash: raspistill: command not found'],
    fix: ['rpicam-still -o test.jpg'],
    why: 'The old camera stack is gone. Its replacement is rpicam-still; posts from 2022 and 2023 call it libcamera-still.',
    oldBox: 'raspistill',
    newBox: 'rpicam-still',
  },
  {
    id: 'desktop',
    label: 'Stop the screen blanking',
    oldCmd: ['xset s off'],
    now: ['(no error, and the screen still blanks)', 'Current Bookworm desktops run Wayland, not X11.'],
    fix: ['sudo raspi-config', '# Display Options → Screen Blanking', '# or switch back to X11 under Advanced Options'],
    why: 'Tools that talk to X11, like xset and xdotool, may do nothing under Wayland. Look for the setting in raspi-config first.',
    oldBox: 'X11',
    newBox: 'Wayland',
  },
];

export function stepById(id: StepId): OldStep {
  const s = BOOKWORM_STEPS.find((x) => x.id === id);
  if (!s) throw new Error(`piSetup: unknown Bookworm step ${id}`);
  return s;
}
