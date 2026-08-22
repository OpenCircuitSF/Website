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
 * reconnects automatically after a drop, so this stays simple. Because the
 * server always publishes a full recomputed snapshot rather than a delta
 * (#0048's CampaignProgress, worker.go's own doc comment), a reconnect can
 * never double-count — whatever the next frame says simply replaces whatever
 * the consumer was showing.
 *
 * A malformed frame (non-JSON `data`) is swallowed defensively: it is ignored so
 * one bad event cannot crash the consuming view or kill the live stream.
 *
 * `onOpen`, when given, fires on every `open` the underlying EventSource
 * reports — the INITIAL connection and every automatic reconnect alike (the
 * DOM EventSource does not distinguish the two). #0048's CampaignEditor uses
 * this to re-fetch the campaign's status from the server on each (re)connect:
 * the database, not this stream, is the source of truth (CLAUDE.md), and a
 * campaign can finish sending — or be cancelled, or fail — entirely during a
 * gap with no further batch to publish a closing frame, so a client that
 * relies solely on the next event could wait forever for one that never
 * comes. Re-checking status on every open bounds that gap to "however long
 * the stream was down," rather than "possibly forever."
 *
 * @param eventName the SSE `event:` name to listen for, e.g. "campaign.progress".
 * @param onEvent   called with each parsed JSON payload of that event name.
 * @param factory   optional EventSource factory (defaults to the global), for tests.
 * @param onOpen    optional callback fired on the initial connection and every reconnect.
 */
export function subscribeEvent<T>(
  eventName: string,
  onEvent: (payload: T) => void,
  factory: EventSourceFactory = defaultFactory,
  onOpen?: () => void,
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

  if (onOpen) {
    es.addEventListener('open', () => onOpen());
  }

  return () => es.close();
}
