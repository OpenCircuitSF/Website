// Pure helpers behind the admin venues screen and the workshop forms' Venue
// selectors (#0617). Disabling a venue means "no new listings here", so a listing's
// current venue stays selectable even when disabled.

import type { Venue } from './types';

/** Venues ordered by sort_order then name, matching the server's ListAll. */
export function sortedVenues(list: Venue[]): Venue[] {
  return [...list].sort((a, b) => a.sort_order - b.sort_order || a.name.localeCompare(b.name));
}

/** Validate the add-a-venue form. */
export function validateNewVenue(name: string): { name: string } | { error: string } {
  const trimmed = name.trim();
  if (trimmed === '') {
    return { error: 'Name is required.' };
  }
  return { name: trimmed };
}

/** The sort_order that puts a new venue last: the highest plus 10. */
export function nextVenueSortOrder(list: Venue[]): number {
  return list.reduce((max, v) => Math.max(max, v.sort_order), 0) + 10;
}

/** Enabled venues plus `currentId` (which may be disabled), in display order. */
export function assignableVenues(list: Venue[], currentId: number | null): Venue[] {
  return sortedVenues(list).filter((v) => v.enabled || v.id === currentId);
}

/** The default venue's id, or null when the list has none. */
export function defaultVenueId(list: Venue[]): number | null {
  return list.find((v) => v.is_default)?.id ?? null;
}

/** A select option's text: the address tells two venues of one name apart. */
export function venueOptionLabel(venue: Venue): string {
  const base = venue.address ? `${venue.name}, ${venue.address}` : venue.name;
  return venue.enabled ? base : `${base} (disabled)`;
}

/** Why a venue cannot be deleted, or null when it can. */
export function venueDeleteBlock(venue: Venue): string | null {
  if (venue.is_default) return 'The default venue cannot be deleted. Choose another default first.';
  if (venue.workshop_count > 0) return 'Listings use this venue. Disable it instead.';
  return null;
}
