// @vitest-environment jsdom
//
// #0128: mounts Pending.svelte (the pending-subscriber admin screen) with
// @testing-library/svelte under jsdom, following
// Dashboard.behavior.test.ts's precedent — '../../lib/api' is mocked so
// this test controls the payload directly rather than hitting a network.
// This repo does not install @testing-library/jest-dom, so assertions use
// plain `toBeTruthy()`/native DOM properties (`.disabled`, `.textContent`),
// matching every other behavior test here rather than jest-dom's
// `toHaveTextContent`/`toBeDisabled` matchers, which are not registered.
//
// jsdom is not a browser (CLAUDE.md §1): this test establishes that,
// mounted under jsdom, the component renders the expected rows/badges from
// a given payload and calls listPendingSubscribers/resendConfirmation with
// the arguments its own click handlers claim to pass — it does NOT
// establish real-browser layout, focus order, or screen-reader behavior.
// The admin console cannot be driven in a real browser in this sandbox (no
// WebAuthn ceremony), so this jsdom mount plus static reading of
// Admin.svelte's wiring is the honest ceiling for this issue's frontend
// verification.
import { render, cleanup, waitFor, screen, fireEvent } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { PendingListResponse, ResendConfirmationResponse } from '../../lib/types';

const listPendingSubscribers = vi.fn<(oldestFirst?: boolean) => Promise<PendingListResponse>>();
const resendConfirmation = vi.fn<(id: number) => Promise<ResendConfirmationResponse>>();
vi.mock('../../lib/api', () => ({
  listPendingSubscribers: (...args: unknown[]) => listPendingSubscribers(...(args as [boolean?])),
  resendConfirmation: (...args: unknown[]) => resendConfirmation(...(args as [number])),
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

const { default: Pending } = await import('./Pending.svelte');
const { ApiError } = (await import('../../lib/api')) as unknown as {
  ApiError: new (status: number, message: string) => Error;
};

afterEach(() => {
  cleanup();
  listPendingSubscribers.mockReset();
  resendConfirmation.mockReset();
});

function payload(): PendingListResponse {
  return {
    pending: [
      {
        id: 1,
        email: 'live@example.com',
        confirm_sent_at: '2026-08-24T10:00:00Z',
        confirm_expires_at: '2026-08-31T10:00:00Z',
        age_seconds: 3600,
        expired: false,
        utm_source: 'newsletter',
        queue_state: 'queued',
      },
      {
        id: 2,
        email: 'stale@example.com',
        confirm_sent_at: '2026-08-01T10:00:00Z',
        confirm_expires_at: '2026-08-08T10:00:00Z',
        age_seconds: 86400 * 20,
        expired: true,
        queue_state: 'abandoned',
      },
    ],
  };
}

describe('Pending — loading and empty states', () => {
  it('shows a loading indicator, then empty-state copy for zero pending rows', async () => {
    listPendingSubscribers.mockResolvedValue({ pending: [] });
    render(Pending);

    expect(screen.getByText(/loading pending signups/i)).toBeTruthy();
    await waitFor(() => {
      expect(screen.getByText(/no pending signups/i)).toBeTruthy();
    });
    expect(listPendingSubscribers).toHaveBeenCalledWith(true);
  });

  it('shows the error copy and a retry button on a failed load', async () => {
    listPendingSubscribers.mockRejectedValue(new ApiError(500, 'internal server error'));
    render(Pending);

    await waitFor(() => {
      expect(screen.getByText('internal server error')).toBeTruthy();
    });
    expect(screen.getByText('Retry')).toBeTruthy();
  });
});

describe('Pending — populated list', () => {
  it('renders each row with its email, expired badge, and queue-state badge', async () => {
    listPendingSubscribers.mockResolvedValue(payload());
    render(Pending);

    await waitFor(() => {
      expect(screen.getByText('live@example.com')).toBeTruthy();
    });
    expect(screen.getByText('stale@example.com')).toBeTruthy();

    // The expired row carries an "Expired" badge; the live row does not.
    expect(screen.getByText('Expired')).toBeTruthy();

    // Queue-state labels: "Queued" for the live row, "Abandoned" for the stale one.
    expect(screen.getByText('Queued')).toBeTruthy();
    expect(screen.getByText('Abandoned')).toBeTruthy();

    // Signup source: UTM for the first row, "Direct" fallback for the second.
    expect(screen.getByText('newsletter')).toBeTruthy();
    expect(screen.getByText('Direct')).toBeTruthy();
  });

  it('re-fetches with the flipped sort direction when the age header is clicked', async () => {
    listPendingSubscribers.mockResolvedValue(payload());
    render(Pending);

    await waitFor(() => {
      expect(screen.getByText('live@example.com')).toBeTruthy();
    });
    expect(listPendingSubscribers).toHaveBeenCalledWith(true);

    const sortToggle = screen.getByText(/age \(oldest first\)/i);
    await fireEvent.click(sortToggle);

    await waitFor(() => {
      expect(listPendingSubscribers).toHaveBeenCalledWith(false);
    });
  });
});

describe('Pending — resend', () => {
  it('resends, shows a per-row notice, and reloads the list — without disabling other rows', async () => {
    listPendingSubscribers.mockResolvedValue(payload());
    resendConfirmation.mockResolvedValue({
      id: 1,
      confirm_sent_at: '2026-08-24T12:00:00Z',
      confirm_expires_at: '2026-08-31T12:00:00Z',
    });
    render(Pending);

    await waitFor(() => {
      expect(screen.getByText('live@example.com')).toBeTruthy();
    });

    const resendButtons = screen.getAllByText('Resend') as HTMLButtonElement[];
    expect(resendButtons).toHaveLength(2);

    await fireEvent.click(resendButtons[0]);

    expect(resendConfirmation).toHaveBeenCalledWith(1);
    // The OTHER row's button must never have been disabled by this click.
    expect(resendButtons[1].disabled).toBe(false);

    await waitFor(() => {
      expect(screen.getByText(/confirmation resent/i)).toBeTruthy();
    });
    // load() runs once on mount and once more after a successful resend.
    expect(listPendingSubscribers).toHaveBeenCalledTimes(2);
  });

  it('shows the server error message verbatim on a cooldown refusal, not invented copy', async () => {
    listPendingSubscribers.mockResolvedValue(payload());
    resendConfirmation.mockRejectedValue(
      new ApiError(429, 'a confirmation was sent to this address recently — try again later'),
    );
    render(Pending);

    await waitFor(() => {
      expect(screen.getByText('live@example.com')).toBeTruthy();
    });

    const resendButtons = screen.getAllByText('Resend');
    await fireEvent.click(resendButtons[0]);

    await waitFor(() => {
      expect(screen.getByText(/try again later/i)).toBeTruthy();
    });
  });
});
