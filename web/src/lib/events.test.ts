// Unit tests for the generic SSE client: the subscribe/parse/cleanup wiring.
// There is no real EventSource or DOM here — the subscribe path is driven
// through a hand-rolled fake EventSource so we can assert frame parsing,
// defensive handling of bad frames, the callback firing, and that cleanup
// closes the connection.

import { describe, it, expect, vi } from 'vitest';
import { subscribeEvent, EVENTS_URL, type EventSourceLike } from './events';

interface TestPayload {
  id: number;
  label: string;
}

/** A minimal fake EventSource that records listeners and lets tests emit frames. */
class FakeEventSource implements EventSourceLike {
  url: string;
  closed = false;
  private listeners = new Map<string, ((event: { data: string }) => void)[]>();

  constructor(url: string) {
    this.url = url;
  }

  addEventListener(type: string, listener: (event: { data: string }) => void): void {
    const arr = this.listeners.get(type) ?? [];
    arr.push(listener);
    this.listeners.set(type, arr);
  }

  /** Test helper: deliver a frame to every listener registered for `type`. */
  emit(type: string, data: string): void {
    for (const l of this.listeners.get(type) ?? []) l({ data });
  }

  close(): void {
    this.closed = true;
  }
}

const TEST_EVENT = 'test.event';

describe('subscribeEvent', () => {
  it('opens the /api/events stream', () => {
    let captured: FakeEventSource | null = null;
    subscribeEvent<TestPayload>(TEST_EVENT, () => {}, (url) => (captured = new FakeEventSource(url)));
    expect(captured).not.toBeNull();
    expect(captured!.url).toBe(EVENTS_URL);
  });

  it('parses a frame matching the given event name and fires the callback with the payload', () => {
    let es!: FakeEventSource;
    const onEvent = vi.fn();
    subscribeEvent<TestPayload>(TEST_EVENT, onEvent, (url) => (es = new FakeEventSource(url)));

    es.emit(TEST_EVENT, JSON.stringify({ id: 1, label: 'live1' }));

    expect(onEvent).toHaveBeenCalledTimes(1);
    const arg = onEvent.mock.calls[0][0] as TestPayload;
    expect(arg.id).toBe(1);
    expect(arg.label).toBe('live1');
  });

  it('ignores a malformed frame without crashing or firing the callback', () => {
    let es!: FakeEventSource;
    const onEvent = vi.fn();
    subscribeEvent<TestPayload>(TEST_EVENT, onEvent, (url) => (es = new FakeEventSource(url)));

    expect(() => es.emit(TEST_EVENT, 'not json{')).not.toThrow();
    expect(onEvent).not.toHaveBeenCalled();

    // A subsequent good frame still works — one bad frame doesn't kill the stream.
    es.emit(TEST_EVENT, JSON.stringify({ id: 2, label: 'ok' }));
    expect(onEvent).toHaveBeenCalledTimes(1);
  });

  it('ignores frames of a different event name', () => {
    let es!: FakeEventSource;
    const onEvent = vi.fn();
    subscribeEvent<TestPayload>(TEST_EVENT, onEvent, (url) => (es = new FakeEventSource(url)));

    es.emit('other.event', JSON.stringify({ id: 3, label: 'nope' }));
    expect(onEvent).not.toHaveBeenCalled();
  });

  it('cleanup closes the EventSource', () => {
    let es!: FakeEventSource;
    const cleanup = subscribeEvent<TestPayload>(TEST_EVENT, () => {}, (url) => (es = new FakeEventSource(url)));
    expect(es.closed).toBe(false);
    cleanup();
    expect(es.closed).toBe(true);
  });

  it('does not register an open listener when onOpen is omitted', () => {
    let es!: FakeEventSource;
    subscribeEvent<TestPayload>(TEST_EVENT, () => {}, (url) => (es = new FakeEventSource(url)));
    // Must not throw even though nothing is listening for 'open'.
    expect(() => es.emit('open', '')).not.toThrow();
  });

  it('calls onOpen when the EventSource reports open', () => {
    let es!: FakeEventSource;
    const onOpen = vi.fn();
    subscribeEvent<TestPayload>(
      TEST_EVENT,
      () => {},
      (url) => (es = new FakeEventSource(url)),
      onOpen,
    );

    expect(onOpen).not.toHaveBeenCalled();
    es.emit('open', '');
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it('calls onOpen again on a reconnect (a second open frame)', () => {
    let es!: FakeEventSource;
    const onOpen = vi.fn();
    subscribeEvent<TestPayload>(
      TEST_EVENT,
      () => {},
      (url) => (es = new FakeEventSource(url)),
      onOpen,
    );

    es.emit('open', '');
    es.emit('open', ''); // the browser's automatic reconnect fires 'open' again
    expect(onOpen).toHaveBeenCalledTimes(2);
  });
});
