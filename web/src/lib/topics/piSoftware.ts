// Raspberry Pi batch 2 (#0725): WiFi dropouts, run on boot, SD card backup.
// Plain functions; the demos only draw what these return. Facts follow
// raspberrypi.com/documentation, systemd.service(5) and the NetworkManager
// settings reference; the items from memory are listed in #0725's Assumptions.

// ---------------------------------------------------------------------------
// 1. WiFi dropouts: a yes/no tree (the shape of codeOnBoard.ts' troubleshooter)

export type WifiSpot = 'supply' | 'pi' | 'air' | 'router';

export const SPOT_LABELS: Record<WifiSpot, string> = {
  supply: 'Power supply',
  pi: 'Pi’s WiFi chip',
  air: 'Signal',
  router: 'Router',
};

export interface WifiStep {
  id: string;
  question: string;
  detail: string;
  spot: WifiSpot;
  yes: string;
  no: string;
}
export interface WifiLink {
  href: string;
  label: string;
}
export interface WifiFix {
  id: string;
  title: string;
  body: string;
  spot: WifiSpot;
  commands: string[];
  links: WifiLink[];
}

export const WIFI_STEPS: Record<string, WifiStep> = {
  start: {
    id: 'start',
    question: 'Does vcgencmd get_throttled print anything other than throttled=0x0?',
    detail:
      'Run it over SSH while the Pi is reachable. An under-voltage flag (0x50005, 0x50000 and friends) means the supply sags when the Pi gets busy, and WiFi is one of the first things to go. Not sure what the value means? Decode it in the under-voltage guide.',
    spot: 'supply',
    yes: 'fix-power',
    no: 'powersave',
  },
  powersave: {
    id: 'powersave',
    question: 'Does iw wlan0 get power_save say Power save: on?',
    detail:
      'WiFi power saving lets the WiFi chip doze between packets. A Pi that only listens, for SSH or a web page, can stop answering for seconds at a time or drop off.',
    spot: 'pi',
    yes: 'fix-powersave',
    no: 'address',
  },
  address: {
    id: 'address',
    question: 'When it drops, is the Pi still on the router’s client list, maybe at a new address?',
    detail:
      'Look in the router’s list of connected devices. If the Pi is there under a different IP address than the one you were using, it never left: its address changed.',
    spot: 'router',
    yes: 'fix-address',
    no: 'signal',
  },
  signal: {
    id: 'signal',
    question: 'Does iw dev wlan0 link show a signal weaker than −70 dBm?',
    detail:
      'Run it on the Pi and read the signal line. Closer to zero is stronger: −50 dBm is good, −70 dBm is weak, −80 dBm is barely there. The Zero 2W’s antenna is tiny, and a metal case blocks most of the signal.',
    spot: 'air',
    yes: 'fix-signal',
    no: 'steering',
  },
  steering: {
    id: 'steering',
    question: 'Does the router use one network name for both 2.4 and 5 GHz?',
    detail:
      'Called band steering or smart connect. The router decides which band each device uses and can move it between them; every move is a short drop.',
    spot: 'router',
    yes: 'fix-steering',
    no: 'fix-log',
  },
};

export const WIFI_FIXES: Record<string, WifiFix> = {
  'fix-power': {
    id: 'fix-power',
    title: 'Fix the power first',
    body: 'WiFi drops out when the supply can’t hold the voltage up. Use the official supply or a short, thick cable and check get_throttled again. If the value only shows temperature flags, cool the Pi, then start over and answer No here.',
    spot: 'supply',
    commands: ['vcgencmd get_throttled'],
    links: [{ href: '/guides/pi-undervoltage', label: 'Why a Pi slows down: under-voltage' }],
  },
  'fix-powersave': {
    id: 'fix-powersave',
    title: 'Turn off WiFi power saving',
    body: 'Find the connection’s name with nmcli connection show (Raspberry Pi Imager names it preconfigured). Setting 802-11-wireless.powersave to 2 turns power saving off for that connection, and it stays off after a reboot. Check afterwards: iw should say Power save: off.',
    spot: 'pi',
    commands: [
      'nmcli connection show',
      'sudo nmcli connection modify preconfigured 802-11-wireless.powersave 2',
      'sudo reboot',
      'iw wlan0 get power_save',
    ],
    links: [],
  },
  'fix-address': {
    id: 'fix-address',
    title: 'Use its name, or reserve its address',
    body: 'The router gave the Pi a new address. Connect by name instead, mypi.local (your hostname plus .local), or reserve an address for the Pi in the router’s DHCP settings so it always gets the same one.',
    spot: 'router',
    commands: ['ping mypi.local', 'hostname -I'],
    links: [{ href: '/guides/pi-find-on-network', label: 'Where’s my Pi? Finding it on the network' }],
  },
  'fix-signal': {
    id: 'fix-signal',
    title: 'Get a stronger signal',
    body: 'Move the Pi or the router, take the Pi out of a metal case, or add an access point nearer the Pi. 2.4 GHz reaches further through walls than 5 GHz.',
    spot: 'air',
    commands: ['iw dev wlan0 link'],
    links: [],
  },
  'fix-steering': {
    id: 'fix-steering',
    title: 'Give the Pi its own 2.4 GHz network',
    body: 'Many routers can give the 2.4 GHz band its own network name. Connect the Pi to that one, so the router can’t move it between bands.',
    spot: 'router',
    commands: ['iw dev wlan0 link'],
    links: [{ href: '/guides/pi-wifi-2-4ghz', label: '2.4 GHz vs 5 GHz' }],
  },
  'fix-log': {
    id: 'fix-log',
    title: 'Catch it in the log',
    body: 'None of the usual causes. Leave the log open when it drops: NetworkManager says when it lost the connection and why, and brcmf lines come from the WiFi chip’s driver.',
    spot: 'pi',
    commands: ['journalctl -u NetworkManager -b', 'sudo dmesg | grep -i brcmf'],
    links: [],
  },
};

export const WIFI_NOTE = 'Never joins the WiFi at all? That’s a different problem: see the 2.4 GHz guide.';

export function isWifiFix(id: string): boolean {
  return Object.prototype.hasOwnProperty.call(WIFI_FIXES, id);
}

export function wifiNext(id: string, answer: 'yes' | 'no'): string {
  const step = WIFI_STEPS[id];
  if (!step) throw new Error(`wifi doctor: no step ${id}`);
  return answer === 'yes' ? step.yes : step.no;
}

export function wifiSpot(id: string): WifiSpot {
  if (isWifiFix(id)) return WIFI_FIXES[id].spot;
  const step = WIFI_STEPS[id];
  if (!step) throw new Error(`wifi doctor: no step ${id}`);
  return step.spot;
}

// ---------------------------------------------------------------------------
// 2. Run on boot: a systemd unit built from toggles

export type UnitToggle = 'fullPath' | 'workdir' | 'user' | 'network' | 'restart' | 'restartSec' | 'install';
export type UnitOptions = Record<UnitToggle, boolean>;

export const ALL_ON: UnitOptions = {
  fullPath: true,
  workdir: true,
  user: true,
  network: true,
  restart: true,
  restartSec: true,
  install: true,
};

export const UNIT_TOGGLES: { id: UnitToggle; label: string; what: string }[] = [
  { id: 'fullPath', label: 'Full paths in ExecStart', what: 'Starts the right program no matter which folder it starts in.' },
  { id: 'workdir', label: 'WorkingDirectory=', what: 'The folder the program starts in.' },
  { id: 'user', label: 'User=', what: 'Runs as your user, not root.' },
  { id: 'network', label: 'Wait for the network (After= and Wants=)', what: 'Starts after the network is up.' },
  { id: 'restart', label: 'Restart=on-failure', what: 'Starts it again when it crashes.' },
  { id: 'restartSec', label: 'RestartSec=5', what: 'Waits 5 s before restarting.' },
  { id: 'install', label: '[Install] WantedBy=multi-user.target', what: 'Lets systemctl enable start it at boot.' },
];

export const SERVICE = 'plantlog';

export interface UnitLine {
  text: string;
  toggle: UnitToggle | null;
  on: boolean;
}

function line(text: string, toggle: UnitToggle | null, o: UnitOptions): UnitLine {
  if (toggle === null) return { text, toggle, on: true };
  const on = o[toggle];
  return { text: on ? text : `# ${text}`, toggle, on };
}

export function unitLines(o: UnitOptions): UnitLine[] {
  const blank: UnitLine = { text: '', toggle: null, on: true };
  return [
    line('[Unit]', null, o),
    line('Description=Plant logger', null, o),
    line('After=network-online.target', 'network', o),
    line('Wants=network-online.target', 'network', o),
    { ...blank },
    line('[Service]', null, o),
    {
      text: o.fullPath ? 'ExecStart=/usr/bin/python3 /home/maker/plants/log.py' : 'ExecStart=python3 log.py',
      toggle: 'fullPath',
      on: true,
    },
    line('WorkingDirectory=/home/maker/plants', 'workdir', o),
    line('User=maker', 'user', o),
    line('Restart=on-failure', 'restart', o),
    line('RestartSec=5', 'restartSec', o),
    { ...blank },
    line('[Install]', 'install', o),
    line('WantedBy=multi-user.target', 'install', o),
  ];
}

export function unitText(o: UnitOptions): string {
  return unitLines(o)
    .map((l) => l.text)
    .join('\n');
}

export type Scenario = 'boot' | 'crash';
export const SCENARIOS: { id: Scenario; label: string }[] = [
  { id: 'boot', label: 'The Pi boots' },
  { id: 'crash', label: 'The program crashes' },
];
export type ServiceId =
  | 'running'
  | 'root'
  | 'noInstall'
  | 'badPath'
  | 'netRetry'
  | 'netGaveUp'
  | 'netDead'
  | 'crashOk'
  | 'crashFast'
  | 'crashDead';
export type EventTone = 'good' | 'meh' | 'bad' | 'plain';
export interface TimelineEvent {
  at: number;
  label: string;
  tone: EventTone;
}
export interface ServiceOutcome {
  id: ServiceId;
  title: string;
  body: string;
  tone: 'good' | 'meh' | 'bad';
  status: string;
  log: string[];
  events: TimelineEvent[];
}

const ev = (at: number, label: string, tone: EventTone = 'plain'): TimelineEvent => ({ at, label, tone });

const RUNNING_EVENTS = (): TimelineEvent[] => [
  ev(0, 'Power on'),
  ev(30, 'WiFi up'),
  ev(35, 'plantlog starts', 'good'),
  ev(100, 'running', 'good'),
];

function badPath(o: UnitOptions): ServiceOutcome {
  const events = [ev(0, 'Power on'), ev(35, 'plantlog starts'), ev(37, 'can’t find log.py', 'bad')];
  // Without RestartSec the retries come 100 ms apart, so systemd's start limit
  // (5 starts in 10 s by default) stops them within a few seconds (#0725 review).
  const retrying = o.restart && o.restartSec;
  if (o.restart) events.push(ev(60, retrying ? 'tries again' : 'start limit: gives up', 'bad'));
  return {
    id: 'badPath',
    title: 'Starts, then can’t find its file',
    body:
      'With no WorkingDirectory the program starts in /, so python3 looks for /log.py. Use full paths: they work wherever it starts.' +
      (retrying
        ? ' Restart=on-failure just repeats the failure every few seconds.'
        : o.restart
          ? ' Without RestartSec it retries at once, hits systemd’s start limit and gives up.'
          : ' It fails once and stays dead.'),
    tone: 'bad',
    status: retrying ? 'Active: activating (auto-restart) (Result: exit-code)' : 'Active: failed (Result: exit-code)',
    log: [
      "python3: can't open file '/log.py': [Errno 2] No such file or directory",
      'plantlog.service: Main process exited, code=exited, status=2',
    ],
    events,
  };
}

export function serviceOutcome(o: UnitOptions, s: Scenario): ServiceOutcome {
  const brokenPath = !o.fullPath && !o.workdir;
  if (s === 'crash') {
    if (brokenPath) return badPath(o);
    if (!o.restart)
      return {
        id: 'crashDead',
        title: 'Crashed and stays dead',
        body: 'Restart= defaults to no: when the program dies, systemd leaves it dead until someone notices.',
        tone: 'bad',
        status: 'Active: failed (Result: exit-code)',
        log: ['Traceback (most recent call last):', 'plantlog.service: Main process exited, code=exited, status=1/FAILURE'],
        events: [ev(0, 'running', 'good'), ev(40, 'crash', 'bad'), ev(100, 'still dead', 'bad')],
      };
    if (!o.restartSec)
      return {
        id: 'crashFast',
        title: 'Restarted at once',
        body: 'Without RestartSec, systemd restarts it after a tenth of a second. If it crashes straight away again, systemd soon gives up and marks it failed. A few seconds’ wait gives a flaky network or sensor time to recover.',
        tone: 'meh',
        status: 'Active: active (running)',
        log: [
          'plantlog.service: Main process exited, code=exited, status=1/FAILURE',
          'plantlog.service: Scheduled restart job, restart counter is at 1.',
        ],
        events: [ev(0, 'running', 'good'), ev(40, 'crash', 'bad'), ev(41, 'restart', 'meh'), ev(100, 'running', 'good')],
      };
    return {
      id: 'crashOk',
      title: 'Crashed, back 5 s later',
      body: 'systemd saw it exit with an error, waited 5 s and started it again. journalctl shows the crash, so you can still fix the bug.',
      tone: 'good',
      status: 'Active: active (running)',
      log: [
        'plantlog.service: Main process exited, code=exited, status=1/FAILURE',
        'plantlog.service: Scheduled restart job, restart counter is at 1.',
      ],
      events: [ev(0, 'running', 'good'), ev(40, 'crash', 'bad'), ev(50, 'restarted 5 s later', 'good'), ev(100, 'running', 'good')],
    };
  }

  if (!o.install)
    return {
      id: 'noInstall',
      title: 'Doesn’t start at boot',
      body: 'Without an [Install] section, systemctl enable has nothing to hook into. It says so, and at boot nothing starts the service. It still runs if you start it by hand.',
      tone: 'bad',
      status: 'Active: inactive (dead)',
      log: ['$ sudo systemctl enable plantlog', 'The unit files have no installation config (WantedBy=, RequiredBy=, …)'],
      events: [ev(0, 'Power on'), ev(30, 'WiFi up'), ev(100, 'plantlog never starts', 'bad')],
    };
  if (brokenPath) return badPath(o);
  if (!o.network && o.restart && !o.restartSec)
    // Retries 100 ms apart fail as fast as they start: five in a few seconds
    // trips systemd's default start limit (5 in 10 s) long before the WiFi is
    // up, so the restart never gets to save it (#0725 review).
    return {
      id: 'netGaveUp',
      title: 'Fails at boot, retries at once, then gives up',
      body: 'It started before the WiFi was up. Without RestartSec, systemd restarted it a tenth of a second later, again and again, until it hit the start limit and stopped trying, all before the WiFi came up. Wait for network-online.target, and give RestartSec a few seconds.',
      tone: 'bad',
      status: 'Active: failed (Result: start-limit-hit)',
      log: [
        'Temporary failure in name resolution',
        'plantlog.service: Start request repeated too quickly.',
        "plantlog.service: Failed with result 'start-limit-hit'.",
      ],
      events: [
        ev(0, 'Power on'),
        ev(10, 'plantlog starts'),
        ev(14, 'fails, restarts, fails…', 'bad'),
        ev(20, 'start limit: gives up', 'bad'),
        ev(30, 'WiFi up'),
      ],
    };
  if (!o.network && o.restart)
    return {
      id: 'netRetry',
      title: 'Fails once at boot, then the restart fixes it',
      body: 'It started before the WiFi was up and couldn’t reach the server. Restart= saved it this time; waiting for network-online.target fixes the cause.',
      tone: 'meh',
      status: 'Active: active (running)',
      log: [
        'plantlog.service: Main process exited, code=exited, status=1/FAILURE',
        'plantlog.service: Scheduled restart job',
        'Started plantlog.service - Plant logger.',
      ],
      events: [
        ev(0, 'Power on'),
        ev(10, 'plantlog starts'),
        ev(14, 'no network yet', 'bad'),
        ev(30, 'WiFi up'),
        ev(40, 'restarted: running', 'good'),
      ],
    };
  if (!o.network)
    return {
      id: 'netDead',
      title: 'Fails at boot and stays dead',
      body: 'It started before the WiFi was up, failed, and nothing started it again.',
      tone: 'bad',
      status: 'Active: failed (Result: exit-code)',
      log: ['Temporary failure in name resolution', "plantlog.service: Failed with result 'exit-code'."],
      events: [ev(0, 'Power on'), ev(10, 'plantlog starts'), ev(14, 'no network yet: dead', 'bad'), ev(30, 'WiFi up')],
    };
  if (!o.user)
    return {
      id: 'root',
      title: 'Running, but as root',
      body: 'It works, but as root: any file it writes belongs to root, and a bug in it can change anything on the card. Add User= with your own user name.',
      tone: 'meh',
      status: 'Active: active (running)',
      log: ['Started plantlog.service - Plant logger.'],
      events: RUNNING_EVENTS(),
    };
  return {
    id: 'running',
    title: 'Running',
    body: 'It waits for the network, starts as your user, and comes back if it crashes.',
    tone: 'good',
    status: 'Active: active (running)',
    log: ['Started plantlog.service - Plant logger.'],
    events: RUNNING_EVENTS(),
  };
}

// ---------------------------------------------------------------------------
// 3. Back up the SD card: build dd and guard the wrong disk

export type HostOs = 'mac' | 'linux';
export const HOSTS: { id: HostOs; label: string }[] = [
  { id: 'mac', label: 'Mac' },
  { id: 'linux', label: 'Linux' },
];
export type DiskKind = 'internal' | 'usb' | 'sdcard';
export interface Disk {
  id: string;
  dev: string;
  rawDev: string;
  size: string;
  kind: DiskKind;
  label: string;
}

export const DISKS: Record<HostOs, Disk[]> = {
  mac: [
    { id: 'disk0', dev: '/dev/disk0', rawDev: '/dev/rdisk0', size: '500.3 GB', kind: 'internal', label: 'internal: this Mac’s own disk' },
    { id: 'disk4', dev: '/dev/disk4', rawDev: '/dev/rdisk4', size: '31.9 GB', kind: 'sdcard', label: 'external: the Pi’s SD card, in a card reader' },
    { id: 'disk6', dev: '/dev/disk6', rawDev: '/dev/rdisk6', size: '1.0 TB', kind: 'usb', label: 'external: your USB backup drive' },
  ],
  linux: [
    { id: 'nvme0n1', dev: '/dev/nvme0n1', rawDev: '/dev/nvme0n1', size: '476.9G', kind: 'internal', label: 'this laptop’s own disk' },
    { id: 'sda', dev: '/dev/sda', rawDev: '/dev/sda', size: '931.5G', kind: 'usb', label: 'your USB backup drive' },
    { id: 'sdb', dev: '/dev/sdb', rawDev: '/dev/sdb', size: '29.7G', kind: 'sdcard', label: 'the Pi’s SD card, in a USB card reader' },
  ],
};

export const IMAGE = 'pi.img';
export type EndId = string;

export type DdVerdict = 'backup' | 'restore' | 'same' | 'eraseOwnDisk' | 'eraseOtherDrive' | 'diskOverCard' | 'notTheCard';
export interface DdPlan {
  verdict: DdVerdict;
  tone: 'good' | 'meh' | 'bad';
  title: string;
  body: string;
  writesDisk: boolean;
  command: string | null;
  warning: { strong: string; rest: string } | null;
  before: string[];
  after: string[];
}

function diskOf(os: HostOs, id: EndId): Disk | undefined {
  return DISKS[os].find((d) => d.id === id);
}

function cardOf(os: HostOs): Disk {
  const c = DISKS[os].find((d) => d.kind === 'sdcard');
  if (!c) throw new Error(`sd backup: no card for ${os}`);
  return c;
}

export function endLabel(os: HostOs, id: EndId): string {
  if (id === 'file') return `${IMAGE} (a file on this computer)`;
  const d = diskOf(os, id);
  if (!d) throw new Error(`sd backup: no disk ${id}`);
  return `${d.dev} (${d.size}, ${d.label})`;
}

export function ddCommand(os: HostOs, src: EndId, dst: EndId): string {
  const end = (id: EndId) => {
    if (id === 'file') return IMAGE;
    const d = diskOf(os, id);
    if (!d) throw new Error(`sd backup: no disk ${id}`);
    return os === 'mac' ? d.rawDev : d.dev;
  };
  return os === 'mac'
    ? `sudo dd if=${end(src)} of=${end(dst)} bs=4m status=progress`
    : `sudo dd if=${end(src)} of=${end(dst)} bs=4M status=progress conv=fsync`;
}

function refused(verdict: DdVerdict, tone: 'meh' | 'bad', title: string, body: string, writesDisk: boolean): DdPlan {
  return { verdict, tone, title, body, writesDisk, command: null, warning: null, before: [], after: [] };
}

export function ddPlan(os: HostOs, src: EndId, dst: EndId): DdPlan {
  const card = cardOf(os);
  const s = diskOf(os, src);
  const d = diskOf(os, dst);
  if (src === dst)
    return refused('same', 'meh', 'Source and destination are the same', 'Pick the SD card as one end and pi.img as the other.', !!d);
  if (d && d.kind === 'internal')
    return refused(
      'eraseOwnDisk',
      'bad',
      'Stop: that’s your computer’s own disk',
      `dd doesn’t add a file to a disk, it overwrites the whole disk from the first byte. This would erase ${d.dev}, the disk your computer runs from.`,
      true,
    );
  if (d && d.kind === 'usb')
    return refused(
      'eraseOtherDrive',
      'bad',
      'Stop: that would erase your backup drive',
      `dd writes the raw disk, not a file on it: everything on ${d.dev} would be gone. To keep the image on that drive, pick pi.img and run the command from a folder on the drive.`,
      true,
    );
  if (d && d.kind === 'sdcard' && s)
    return refused(
      'diskOverCard',
      'bad',
      'Stop: that copies a whole disk over the card',
      `Only an image file goes onto the card. This would overwrite the card with ${s.dev}.`,
      true,
    );
  if (dst === 'file' && s && s.kind !== 'sdcard')
    return refused(
      'notTheCard',
      'meh',
      'That’s not the SD card',
      `This copies ${s.dev} (${s.size}) into a file, not the Pi’s card. Find the card by its size: ${card.size}.`,
      false,
    );
  const before = os === 'mac' ? ['diskutil list', `diskutil unmountDisk ${card.dev}`] : ['lsblk', `sudo umount ${card.dev}1 ${card.dev}2`];
  if (s && s.kind === 'sdcard' && dst === 'file')
    return {
      verdict: 'backup',
      tone: 'good',
      title: 'Backup: card → pi.img',
      body: 'dd reads every byte of the card into pi.img: the same size as the card, empty space included. Compress it afterwards.',
      writesDisk: false,
      command: ddCommand(os, src, dst),
      warning: {
        strong: 'Check the disk number twice.',
        rest: 'Reading the card is safe. Swap if= and of= and the same command erases it.',
      },
      before,
      after: os === 'mac' ? ['gzip pi.img', `diskutil eject ${card.dev}`] : ['gzip pi.img'],
    };
  if (src === 'file' && d && d.kind === 'sdcard')
    return {
      verdict: 'restore',
      tone: 'meh',
      title: 'Restore: pi.img → card (erases the card)',
      body: 'This works, but Raspberry Pi Imager’s Use custom does the same and lists only removable drives. Imager also reads pi.img.gz without unzipping.',
      writesDisk: true,
      command: ddCommand(os, src, dst),
      warning: {
        strong: `This erases everything on ${card.dev} (${card.size}).`,
        rest: 'Run diskutil list (lsblk on Linux) again right before, and check the size is the card’s.',
      },
      before,
      after: os === 'mac' ? [`diskutil eject ${card.dev}`] : ['sync'],
    };
  throw new Error(`sd backup: no plan for ${src} → ${dst}`);
}

export function isRefused(p: DdPlan): boolean {
  return p.command === null;
}

export const COPIER_NOTE =
  'On the Pi’s desktop: Accessories → SD Card Copier copies the running card to a second card in a USB reader.';
