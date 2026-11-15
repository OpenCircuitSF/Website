// A requestAnimationFrame clock for the LED simulator: `elapsed` is the
// on-page equivalent of millis() - bootMs. Create one inside a component's
// script; it starts on mount and stops when the component is destroyed.
import { onMount } from 'svelte';

export class Clock {
  elapsed = $state(0);
  // Under prefers-reduced-motion the clock starts paused, so the strips sit
  // still until the user presses play.
  playing = $state(!prefersReducedMotion());
  #origin = 0;
  #pausedAt = 0;

  constructor() {
    onMount(() => {
      this.#origin = performance.now();
      let raf = 0;
      const tick = (now: number) => {
        if (this.playing) this.elapsed = now - this.#origin;
        raf = requestAnimationFrame(tick);
      };
      raf = requestAnimationFrame(tick);
      return () => cancelAnimationFrame(raf);
    });
  }

  /** Power-cycle: millis() starts again from zero, as after an upload. */
  reset() {
    this.#origin = performance.now();
    this.elapsed = 0;
  }

  toggle() {
    if (this.playing) {
      this.#pausedAt = this.elapsed;
      this.playing = false;
    } else {
      this.#origin = performance.now() - this.#pausedAt;
      this.playing = true;
    }
  }

  /** Jump to a moment (the "how it works" scrubber). Pauses the clock. */
  seek(ms: number) {
    this.playing = false;
    this.#pausedAt = ms;
    this.elapsed = ms;
  }
}

function prefersReducedMotion(): boolean {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false;
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}
