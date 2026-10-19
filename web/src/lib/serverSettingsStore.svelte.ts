// One shared load of the server settings (#0593 / #0584): Admin.svelte mounts
// ServerSettings twice (Server settings and Outreach), and both read the same
// state from one fetch instead of each calling the API.
import { getServerSettings, ApiError } from './api';
import type { ServerSettings } from './types';

export class ServerSettingsStore {
  settings = $state<ServerSettings | null>(null);
  loading = $state(true);
  loadError = $state<string | null>(null);
  unavailable = $state(false);
  private started: Promise<void> | null = null;

  /** Starts the load once; later callers get the same promise. */
  ensure(): Promise<void> {
    if (!this.started) this.started = this.load();
    return this.started;
  }

  /** Reloads (Retry). */
  reload(): Promise<void> {
    this.started = this.load();
    return this.started;
  }

  private async load(): Promise<void> {
    this.loading = true;
    this.loadError = null;
    this.unavailable = false;
    try {
      this.settings = await getServerSettings();
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) this.unavailable = true;
      else this.loadError = 'Could not load server settings. Please try again.';
    } finally {
      this.loading = false;
    }
  }
}
