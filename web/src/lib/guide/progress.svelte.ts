// Which guide steps a student has ticked off. Kept in this browser only
// (localStorage) -- the site has no student accounts, and a lost checklist
// costs nothing. Every storage access is guarded: private windows and
// blocked storage throw, and the page must still work without it.

export class Progress {
  done = $state<Record<string, boolean>>({});
  #key: string;

  constructor(slug: string) {
    this.#key = `guide-progress:${slug}`;
    try {
      const raw = localStorage.getItem(this.#key);
      if (raw) this.done = JSON.parse(raw) as Record<string, boolean>;
    } catch {
      // No storage: start empty.
    }
  }

  toggle(id: string) {
    this.done = { ...this.done, [id]: !this.done[id] };
    this.#save();
  }

  count(ids: readonly string[]): number {
    return ids.filter((id) => this.done[id]).length;
  }

  clear() {
    this.done = {};
    this.#save();
  }

  #save() {
    try {
      localStorage.setItem(this.#key, JSON.stringify(this.done));
    } catch {
      // Ignore: progress just won't survive a reload.
    }
  }
}
