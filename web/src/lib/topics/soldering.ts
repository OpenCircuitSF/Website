// The logic behind the soldering topic guides (#0667): solder alloys, the
// iron-temperature window, tinning, joint types and tip care. Plain data and
// pure functions so the demos stay thin and the rules are unit-tested.
//
// Illustrations are drawn SVGs (assets/guides/soldering) rendered to PNG by
// scripts/render-soldering-art.sh. Every image goes through art(), so swapping
// a drawing for a photo later is a one-line change here.
import { formatTemp, type TempUnit } from '../temperature';

export interface Art {
  src: string;
  srcset: string;
  width: number;
  height: number;
  alt: string;
}

export const ART_BASE = '/img/guides/soldering';

export function art(name: string, width: number, height: number, alt: string): Art {
  return {
    src: `${ART_BASE}/${name}.png`,
    srcset: `${ART_BASE}/${name}.png 1x, ${ART_BASE}/${name}@2x.png 2x`,
    width,
    height,
    alt,
  };
}

const joint = (name: string, alt: string) => art(name, 400, 250, alt);
const strip = (name: string, alt: string) => art(name, 400, 200, alt);

export const SPOOL_ART = art(
  'solder-spool',
  400,
  250,
  'A spool of solder wire, with a magnified cut end showing the flux core in the middle of the wire',
);

// ---------------------------------------------------------------------------
// Solder wire

export type AlloyId = 'sn63' | 'sn60' | 'sac305' | 'plumbing';

export interface Alloy {
  id: AlloyId;
  name: string;
  /** What is in it. */
  makeup: string;
  /** Solid below the first number, fully liquid above the second. */
  meltC: [number, number] | null;
  /** The iron setting to start at, °C. */
  ironC: [number, number] | null;
  finish: string;
  /** Safe for electronics at all. */
  forElectronics: boolean;
  verdict: string;
}

export const ALLOYS: Alloy[] = [
  {
    id: 'sn63',
    name: '63/37 leaded',
    makeup: '63% tin, 37% lead (Sn63Pb37)',
    meltC: [183, 183],
    ironC: [330, 350],
    finish: 'Bright and shiny.',
    forElectronics: true,
    verdict:
      'The easiest to learn with. It is eutectic: it goes straight from solid to liquid at {temp:183}, with no mushy in-between stage, so joints set cleanly.',
  },
  {
    id: 'sn60',
    name: '60/40 leaded',
    makeup: '60% tin, 40% lead (Sn60Pb40)',
    meltC: [183, 190],
    ironC: [330, 350],
    finish: 'Shiny.',
    forElectronics: true,
    verdict:
      'Nearly as easy. Between {temp:183} and {temp:190} it is a paste, so hold the joint still a moment longer while it sets.',
  },
  {
    id: 'sac305',
    name: 'SAC305 lead-free',
    makeup: '96.5% tin, 3% silver, 0.5% copper',
    meltC: [217, 220],
    ironC: [350, 380],
    finish: 'Duller and slightly grainy, even when the joint is good.',
    forElectronics: true,
    verdict:
      'No lead, so it is what most new products use. It melts hotter and wets a little more slowly, so it needs more heat and a clean tip.',
  },
  {
    id: 'plumbing',
    name: 'Plumbing solder',
    makeup: 'Thick wire, often with an acid flux core, made for copper pipes',
    meltC: null,
    ironC: null,
    finish: 'Not relevant: it should never touch a circuit board.',
    forElectronics: false,
    verdict:
      'Never use it on electronics. The acid flux keeps eating copper after you finish, and the joint corrodes and fails weeks later.',
  },
];

export function alloyById(id: AlloyId): Alloy {
  const a = ALLOYS.find((x) => x.id === id);
  if (!a) throw new Error(`soldering: unknown alloy ${id}`);
  return a;
}

export const DIAMETERS_MM = [0.5, 0.8, 1.0, 1.6] as const;

export interface DiameterAdvice {
  fit: 'good' | 'ok' | 'poor';
  text: string;
}

/** How well a solder wire diameter suits small through-hole electronics. */
export function diameterAdvice(mm: number): DiameterAdvice {
  if (mm < 0.6) return { fit: 'ok', text: 'Very fine. Great for tiny surface-mount parts, but slow to feed on bigger joints.' };
  if (mm <= 0.8) return { fit: 'good', text: 'Just right for header pins, LED strips and most kits.' };
  if (mm <= 1.0) return { fit: 'ok', text: 'Usable, but easy to dump too much solder on a small pad.' };
  return { fit: 'poor', text: 'Too thick for small pads. It floods the joint and bridges neighbours.' };
}

// ---------------------------------------------------------------------------
// Iron temperature

export type TipShape = 'fine' | 'chisel';

/**
 * How far below the dial a tip actually runs once it touches a joint. A
 * chisel tip has more metal and more contact area, so it delivers heat
 * faster and drops less. The dial ranges in ALLOYS assume a chisel tip.
 */
export const TIP_DROP_C: Record<TipShape, number> = { fine: 45, chisel: 15 };

export type JointKind = 'no-melt' | 'cold' | 'slow' | 'good' | 'hot' | 'scorch';

export interface JointOutcome {
  kind: JointKind;
  /** Seconds to get the joint to flow; null when it never will. */
  seconds: number | null;
  title: string;
  body: string;
  art: Art;
}

/** The longest a joint can take before we just say "too long". */
export const MAX_SECONDS = 20;

export const JOINT_ART = {
  good: joint('joint-good', 'A good joint: a shiny, smooth cone of solder that wets the whole pad and climbs the pin'),
  bare: joint('joint-bare', 'A pin through a pad with no solder on it, and a lump of solder that has not melted'),
  cold: joint('joint-cold', 'A cold joint: a dull, grainy lump that sits on the pad with a crack around the pin'),
  insufficient: joint('joint-insufficient', 'Not enough solder: a thin ring on the pin, with bare copper still showing on the pad'),
  blob: joint('joint-blob', 'Too much solder: a round ball that hides the pin and the edge of the pad'),
  disturbed: joint('joint-disturbed', 'A disturbed joint: the pin is tilted and the solder is wrinkled and cracked'),
  overheated: joint('joint-overheated', 'An overheated joint: burnt brown flux, a scorched board and a pad lifting off it'),
  bridge: joint('joint-bridge', 'A solder bridge: one blob of solder joining two neighbouring pins'),
} satisfies Record<string, Art>;

/** What happens to a through-hole joint at a dial setting, alloy and tip. */
export function jointAt(dialC: number, alloyId: 'sn63' | 'sac305', tip: TipShape, unit: TempUnit = 'F'): JointOutcome {
  const alloy = alloyById(alloyId);
  const [melt0, melt] = alloy.meltC!;
  const [lo, hi] = alloy.ironC!;
  const tipC = dialC - TIP_DROP_C[tip];
  const goodLo = lo - TIP_DROP_C.chisel;
  const goodHi = hi - TIP_DROP_C.chisel;

  if (tipC < melt + 10) {
    return {
      kind: 'no-melt',
      seconds: null,
      title: 'The solder won’t melt',
      body: `At the joint the tip only reaches about ${formatTemp(tipC, unit, 10)}. ${alloy.name} needs well over ${formatTemp(melt0, unit)}, so the solder just sits there.`,
      art: JOINT_ART.bare,
    };
  }
  const seconds = Math.min(MAX_SECONDS, Math.round((5 * (goodLo - melt)) / (tipC - melt)) / 2);
  if (tipC < goodLo - 40) {
    return {
      kind: 'cold',
      seconds,
      title: 'Cold joint',
      body: 'The solder half-melts and lumps up instead of flowing. It looks dull and grainy, and it may not connect at all.',
      art: JOINT_ART.cold,
    };
  }
  if (tipC < goodLo) {
    return {
      kind: 'slow',
      seconds,
      title: 'It works, slowly',
      body:
        tip === 'fine'
          ? 'The thin tip can’t push heat into the joint fast enough. While you wait, the flux cooks away. Use a bigger tip before you turn the dial up.'
          : 'It flows eventually, but the long wait burns off the flux and heats the whole area. A little hotter helps.',
      art: JOINT_ART.good,
    };
  }
  if (tipC <= goodHi) {
    return {
      kind: 'good',
      seconds,
      title: 'Good joint',
      body: 'The solder flows in two or three seconds and wets both the pad and the pin. Smooth, shiny, done.',
      art: JOINT_ART.good,
    };
  }
  if (tipC <= goodHi + 60) {
    return {
      kind: 'hot',
      seconds: 1,
      title: 'Too hot',
      body: 'It melts fast, but the flux burns off before it can clean the metal. The tip blackens quickly and joints come out dull and crusty.',
      // Same drawing as a cold joint (dull, lumpy), but the alt text must not call it one.
      art: { ...JOINT_ART.cold, alt: 'A dull, crusty lump of solder: the flux burnt off before the solder could wet the pad' },
    };
  }
  return {
    kind: 'scorch',
    seconds: 1,
    title: 'Way too hot',
    body: 'Flux smokes and turns brown, the board scorches, and the copper pad can peel off the board. A lifted pad is hard to fix.',
    art: JOINT_ART.overheated,
  };
}

// ---------------------------------------------------------------------------
// Tinning

export const TINNING_ART = {
  wireBare: strip('wire-bare', 'Stripped stranded wire, its copper strands splayed and fraying'),
  wireTinned: strip('wire-tinned', 'Stripped stranded wire, tinned: the strands are fused into one neat silver rod'),
  padBare: strip('pad-bare', 'A bare copper pad on a circuit board'),
  padTinned: strip('pad-tinned', 'A copper pad with a thin, shiny layer of solder on it'),
  joinedGood: strip('joined-good', 'A tinned wire lying flat on a tinned pad, joined by a smooth, shiny fillet'),
  joinedBlob: strip('joined-blob', 'An untinned wire: a ball of solder on top of frayed strands, with solder wicked up under the insulation'),
} satisfies Record<string, Art>;

export interface TinningOutcome {
  kind: 'clean' | 'slow' | 'weak';
  /** Rough seconds of heat to make the joint. */
  seconds: number;
  title: string;
  body: string;
  art: Art;
}

export function tinningResult(wireTinned: boolean, padTinned: boolean): TinningOutcome {
  if (wireTinned && padTinned) {
    return {
      kind: 'clean',
      seconds: 1,
      title: 'Clean joint in about a second',
      body: 'Both sides already carry solder. Touch the iron and the two layers melt into one. No new solder needed.',
      art: TINNING_ART.joinedGood,
    };
  }
  if (wireTinned || padTinned) {
    return {
      kind: 'slow',
      seconds: 3,
      title: 'It joins, but takes longer',
      body: `The bare ${wireTinned ? 'pad' : 'wire'} has to heat up and take solder while you hold the wire, the iron and the solder all at once.`,
      art: TINNING_ART.joinedGood,
    };
  }
  return {
    kind: 'weak',
    seconds: 5,
    title: 'A weak blob',
    body: 'Loose strands splay out and soak solder up under the insulation, leaving it stiff where it will later snap. The rest sits in a ball on top instead of flowing into the joint.',
    art: TINNING_ART.joinedBlob,
  };
}

// ---------------------------------------------------------------------------
// Joint types ("spot the bad joint")

export type JointTypeId = 'good' | 'cold' | 'insufficient' | 'bridge' | 'blob' | 'disturbed' | 'overheated';

export interface JointType {
  id: JointTypeId;
  name: string;
  good: boolean;
  /** How to recognize it. */
  looks: string;
  /** Why it happens and how to fix it. */
  fix: string;
  art: Art;
}

export const JOINT_TYPES: JointType[] = [
  {
    id: 'good',
    name: 'Good joint',
    good: true,
    looks: 'Smooth and shiny, with concave sides like a little volcano. The solder covers the whole pad and climbs the pin.',
    fix: 'Nothing to fix. (With lead-free solder a good joint is a bit duller, but the shape is the same.)',
    art: JOINT_ART.good,
  },
  {
    id: 'cold',
    name: 'Cold joint',
    good: false,
    looks: 'Dull, grainy and lumpy. It sits on the pad like a pebble instead of flowing into it.',
    fix: 'Not enough heat reached the pad and pin. Add a touch of fresh solder (for its flux) and reheat until it flows.',
    art: JOINT_ART.cold,
  },
  {
    id: 'insufficient',
    name: 'Not enough solder',
    good: false,
    looks: 'A thin ring on the pin, with bare copper still showing around it.',
    fix: 'Reheat the pad and pin and feed in a little more solder until the pad is covered.',
    art: JOINT_ART.insufficient,
  },
  {
    id: 'bridge',
    name: 'Solder bridge',
    good: false,
    looks: 'One blob joining two pins that should be separate. It is a short circuit.',
    fix: 'Drag a clean iron tip between the pins, or soak up the extra with solder wick.',
    art: JOINT_ART.bridge,
  },
  {
    id: 'blob',
    name: 'Too much solder',
    good: false,
    looks: 'A round ball that hides the pin and the pad. You can’t tell whether it wetted anything underneath.',
    fix: 'Remove the extra with solder wick or a solder sucker, then reheat so it settles into a cone.',
    art: JOINT_ART.blob,
  },
  {
    id: 'disturbed',
    name: 'Disturbed joint',
    good: false,
    looks: 'Wrinkled, frosty or cracked, often with the pin at a slight angle.',
    fix: 'Something moved while the solder was cooling. Reheat it and hold everything still for a few seconds.',
    art: JOINT_ART.disturbed,
  },
  {
    id: 'overheated',
    name: 'Overheated',
    good: false,
    looks: 'Burnt brown flux, a darkened board, and maybe a pad lifting away from it.',
    fix: 'Too much heat for too long. Lower the iron, use a bigger tip, and keep each joint to 2–3 seconds.',
    art: JOINT_ART.overheated,
  },
];

/** The order the quiz shows the joints in: mixed, with the good one second. */
export const QUIZ_ORDER: JointTypeId[] = ['cold', 'good', 'bridge', 'blob', 'insufficient', 'overheated', 'disturbed'];

export function jointTypeById(id: JointTypeId): JointType {
  const j = JOINT_TYPES.find((x) => x.id === id);
  if (!j) throw new Error(`soldering: unknown joint ${id}`);
  return j;
}

// ---------------------------------------------------------------------------
// Tip care

export interface TipState {
  /** 0 clean metal … 3 black. Oxide at 2 and above stops solder wetting. */
  oxide: 0 | 1 | 2 | 3;
  /** Carrying a fresh coat of solder. */
  tinned: boolean;
  /** Covered in old solder and burnt flux. */
  dirty: boolean;
}

export const NEW_TIP: TipState = { oxide: 0, tinned: true, dirty: false };

export type TipAction = 'solder' | 'tin' | 'brass' | 'sponge' | 'idle' | 'store-bare' | 'store-tinned' | 'tinner';

export const TIP_ACTIONS: { id: TipAction; label: string }[] = [
  { id: 'solder', label: 'Make a joint' },
  { id: 'brass', label: 'Wipe on brass wool' },
  { id: 'sponge', label: 'Wipe on a wet sponge' },
  { id: 'tin', label: 'Melt fresh solder on the tip' },
  { id: 'idle', label: 'Leave it hot, bare, for 10 minutes' },
  { id: 'store-bare', label: 'Switch off with a bare tip' },
  { id: 'store-tinned', label: 'Add a blob of solder, then switch off' },
  { id: 'tinner', label: 'Use tip tinner paste' },
];

export type TipLook = 'shiny' | 'dull' | 'brown' | 'black';

export function tipLook(s: TipState): TipLook {
  if (s.oxide >= 3) return 'black';
  if (s.oxide === 2) return 'brown';
  return s.tinned && s.oxide === 0 ? 'shiny' : 'dull';
}

/** Solder sticks to the tip (and so heat gets into the joint). */
export function tipWets(s: TipState): boolean {
  return s.oxide < 2;
}

const bump = (o: number, by: number) => Math.min(3, o + by) as TipState['oxide'];

export function tipStep(s: TipState, action: TipAction): { state: TipState; message: string } {
  switch (action) {
    case 'solder':
      if (!tipWets(s))
        return { state: s, message: 'The solder beads up and rolls off. An oxidized tip won’t wet, so almost no heat reaches the joint.' };
      return {
        state: { ...s, dirty: true, tinned: true },
        message: s.tinned
          ? 'The tip’s solder coat touches the joint and heat flows straight in. Quick and clean.'
          : 'It works, but slowly: a dry tip only touches the joint at a point. Tin the tip first.',
      };
    case 'tin':
      if (!tipWets(s))
        return { state: s, message: 'The solder balls up and won’t stick. The oxide layer is in the way. Try tip tinner.' };
      return { state: { ...s, tinned: true, oxide: 0 }, message: 'A fresh coat of solder: the tip is shiny again and ready.' };
    case 'brass':
      return {
        state: { ...s, dirty: false, oxide: s.oxide === 1 ? 0 : s.oxide },
        message: 'Brass wool scrubs off old solder and burnt flux without cooling the tip, and leaves a thin coat behind.',
      };
    case 'sponge':
      return {
        state: { ...s, dirty: false },
        message: 'Clean, but each wipe chills the tip and the steam can crack its plating over time. Damp, not soaking.',
      };
    case 'idle':
      if (s.tinned)
        return { state: { ...s, tinned: false, oxide: bump(s.oxide, 1) }, message: 'The solder coat slowly burnt away and the bare tip has started to oxidize.' };
      return { state: { ...s, oxide: bump(s.oxide, 1) }, message: 'A hot, bare tip oxidizes fast. It is getting darker.' };
    case 'store-bare':
      return {
        state: { ...s, tinned: false, oxide: bump(s.oxide, 2) },
        message: 'As it cooled, the bare metal oxidized. Do this a few times and the tip turns black.',
      };
    case 'store-tinned':
      if (!tipWets(s)) return { state: s, message: 'The solder won’t stick to this tip, so it has nothing to protect it.' };
      return {
        state: { ...s, tinned: true, dirty: false },
        message: 'The blob seals the tip from the air. Wipe it off next time and it is shiny underneath.',
      };
    case 'tinner':
      return { state: { oxide: 0, tinned: true, dirty: false }, message: 'Tip tinner chemically strips the oxide and re-tins it. A last resort, not a habit.' };
  }
}

// ---------------------------------------------------------------------------
// Header pins on the ESP32-C3 SuperMini

/** Pins per side of the SuperMini. */
export const HEADER_PINS = 8;

export interface HeaderStep {
  title: string;
  body: string;
  /** Is the SuperMini sitting on the pins yet. */
  boardOn: boolean;
  /** Which pins (0-based, along the near row) are soldered after this step. */
  soldered: number[];
  /** The pin the iron is on, if any. */
  iron: number | null;
}

export const HEADER_STEPS: HeaderStep[] = [
  {
    title: 'Use a breadboard as a jig',
    body: 'Push the long ends of both header strips into a breadboard, the same distance apart as the board’s two rows. The breadboard holds them straight and parallel.',
    boardOn: false,
    soldered: [],
    iron: null,
  },
  {
    title: 'Drop the board on',
    body: 'Lay the SuperMini over the short ends so every pin pokes up through a hole. The board should sit flat on the black plastic.',
    boardOn: true,
    soldered: [],
    iron: null,
  },
  {
    title: 'Tack one corner',
    body: 'Touch the tip to the pad and the pin together, then feed a little solder in where they meet. Just one pin for now.',
    boardOn: true,
    soldered: [0],
    iron: 0,
  },
  {
    title: 'Tack the opposite corner',
    body: 'Now solder the pin at the far corner, diagonally across on the other row. With two opposite corners held, the board can’t tilt.',
    boardOn: true,
    soldered: [0, HEADER_PINS - 1],
    iron: HEADER_PINS - 1,
  },
  {
    title: 'Check it’s flat',
    body: 'Look from the side. If the board is tilted, reheat one tack and gently press the board flat with a tool, never your finger.',
    boardOn: true,
    soldered: [0, HEADER_PINS - 1],
    iron: null,
  },
  {
    title: 'Solder the rest',
    body: 'Work along each row, 2–3 seconds a pin. Then look for bridges between neighbours. Park the iron in its stand between joints.',
    boardOn: true,
    soldered: Array.from({ length: HEADER_PINS }, (_, i) => i),
    iron: null,
  },
];
