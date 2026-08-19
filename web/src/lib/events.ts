// Generic SSE client for the app's `/api/events` stream. The server pushes
// `event: <name>\ndata: <JSON>\n\n` frames scoped to the authenticated user;
// this module opens that stream and dispatches parsed frames of a given event
// name to a callback. It is deliberately payload-agnostic — the source
// skeleton's original version of this file was hardwired to a `link.created`
// frame carrying a Link (deleted in #0003 along with Dashboard.svelte, the
// SPA's only user of it at the time). This project's #0048 (live campaign
// send progress) is the next consumer, over its own event name and payload
// shape, so the parsing/cleanup plumbing is kept generic rather than
// re-specialized prematurely.

/** The endpoint the SPA subscribes to for live server-pushed events. */
export const EVENTS_URL = '/api/events';

/**
 * The minimal EventSource surface this module uses. Declaring it lets tests pass
 * a hand-rolled fake (the real DOM EventSource satisfies it) without depending on
 * a jsdom environment.
 */
export interface EventSourceLike {
  addEventListener(type: string, listener: (event: { data: string }) => void): void;
  close(): void;
}

/** A factory for an EventSource, injectable so tests can supply a fake. */
export type EventSourceFactory = (url: string) => EventSourceLike;

const defaultFactory: EventSourceFactory = (url) =>
  new EventSource(url) as unknown as EventSourceLike;

/**
 * Open the SSE stream and invoke `onEvent` with the parsed JSON payload of
 * every frame named `eventName`. Returns a cleanup function that closes the
 * connection; call it on the consuming view's unmount so the stream is torn
 * down.
 *
 * Reconnection is intentionally NOT custom: the browser's built-in EventSource
 * reconnects automatically after a drop, so this stays simple.
 *
 * A malformed frame (non-JSON `data`) is swallowed defensively: it is ignored so
 * one bad event cannot crash the consuming view or kill the live stream.
 *
 * @param eventName the SSE `event:` name to listen for, e.g. "campaign.progress".
 * @param onEvent   called with each parsed JSON payload of that event name.
 * @param factory   optional EventSource factory (defaults to the global), for tests.
 */
export function subscribeEvent<T>(
  eventName: string,
  onEvent: (payload: T) => void,
  factory: EventSourceFactory = defaultFactory,
): () => void {
  const es = factory(EVENTS_URL);

  es.addEventListener(eventName, (event) => {
    let payload: T;
    try {
      payload = JSON.parse(event.data) as T;
    } catch {
      // A bad/partial frame must not crash the consumer or end the stream.
      return;
    }
    onEvent(payload);
  });

  return () => es.close();
}
