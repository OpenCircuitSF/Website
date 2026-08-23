// Pure, framework-free helpers backing WorkshopDetail.svelte (#0054,
// PRD §7.3) -- the location block's line-by-line construction, the
// external-signup-vs-subscribe-CTA decision, and the slug list handed to
// SubscribeForm's `preselect` prop. Kept out of the .svelte file per
// CLAUDE.md's "SPA logic goes in plain TypeScript modules ... Svelte
// components stay thin" -- #0094 (restated by lib/workshops.ts's own header
// comment) means nothing in a .svelte file is covered by a test, so every
// decision the detail view makes about location/signup/preselect routes
// through here rather than a comparison written directly in the component.
//
// Date formatting, the location-name fallback, and the canceled-badge
// decision already live in lib/workshops.ts (#0053) and are reused by the
// view directly rather than duplicated here.

import type { PublicWorkshop } from './types';

/**
 * Explicit cover-image render dimensions (#0054 acceptance criterion:
 * "Cover image lazy-loaded with explicit dimensions to avoid layout
 * shift"). A fixed 1200x630 box -- the same aspect ratio as the site's OG
 * default image (PRD §4.5) -- is a rendering choice, not a validated upload
 * constraint: the admin workshop editor (#0052) never asks an organizer to
 * upload a specific size, so an image with a different natural aspect ratio
 * is letterboxed by object-fit in the component's own CSS, not resized
 * here.
 */
export const COVER_IMAGE_WIDTH = 1200;
export const COVER_IMAGE_HEIGHT = 630;

/**
 * The location lines to render, in order, omitting any field that's unset:
 * name, then address, then `location_note`. `location_note` (#0054's own
 * acceptance criterion, "location block honoring location_note") is
 * whatever doesn't fit the name/address -- parking instructions, "enter
 * through the loading dock", a buzzer code -- and is always its own line,
 * never folded into the address string.
 */
export function workshopLocationLines(
  w: Pick<PublicWorkshop, 'location_name' | 'location_address' | 'location_note'>,
): string[] {
  const lines: string[] = [];
  const name = w.location_name?.trim();
  const address = w.location_address?.trim();
  const note = w.location_note?.trim();
  if (name) lines.push(name);
  if (address) lines.push(address);
  if (note) lines.push(note);
  return lines;
}

/**
 * Whether the location block has anything at all to show. A workshop whose
 * date is set but whose venue isn't finalized yet gets an honest
 * "Location TBA" (mirroring lib/workshops.ts's workshopLocationLabel for the
 * card) rather than an empty block with nothing in it.
 */
export function hasAnyLocationInfo(
  w: Pick<PublicWorkshop, 'location_name' | 'location_address' | 'location_note'>,
): boolean {
  return workshopLocationLines(w).length > 0;
}

/**
 * The slugs to hand SubscribeForm's `preselect` prop (#0029's own doc
 * comment: "used by workshop pages in #0054") -- every interest tag on this
 * workshop, so a visitor reading about a soldering workshop gets "Soldering
 * & Assembly" already ticked rather than having to find and check it
 * themselves (this issue's Notes: "the conversion path that makes this page
 * worth building").
 */
export function workshopPreselectSlugs(w: Pick<PublicWorkshop, 'interests'>): string[] {
  return w.interests.map((i) => i.slug);
}

/**
 * Whether to surface the workshop's external signup_url as the page's
 * primary action (#0054's own acceptance criterion). An organizer who set a
 * signup_url (Eventbrite, a Google Form, a ticketing page) wants registration
 * traffic routed there; the inline SubscribeForm still renders alongside it
 * for staying on the mailing list, which is a separate action from
 * registering for this specific session.
 */
export function hasExternalSignup(w: Pick<PublicWorkshop, 'signup_url'>): boolean {
  return !!w.signup_url && w.signup_url.trim() !== '';
}
