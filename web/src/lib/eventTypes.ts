// Pure helpers behind the admin event types screen and the workshop forms'
// Type selectors (#0613). Disabling a type means "no new listings of this type",
// so a listing's current type stays selectable even when disabled.

import { isValidInterestSlug } from './admin';
import type { EventType } from './types';

/** Types ordered by sort_order then name, matching the server's ListAll. */
export function sortedEventTypes(list: EventType[]): EventType[] {
  return [...list].sort((a, b) => a.sort_order - b.sort_order || a.name.localeCompare(b.name));
}

/** Validate the add-a-type form; the server still enforces slug uniqueness. */
export function validateNewEventType(
  slug: string,
  name: string,
): { slug: string; name: string } | { error: string } {
  const trimmedSlug = slug.trim();
  const trimmedName = name.trim();
  if (!isValidInterestSlug(trimmedSlug)) {
    return { error: 'Slug must be lowercase letters, numbers, and single hyphens (e.g. "hack-night").' };
  }
  if (trimmedName === '') {
    return { error: 'Name is required.' };
  }
  return { slug: trimmedSlug, name: trimmedName };
}

/** The sort_order that puts a new type last: the highest plus 10. */
export function nextEventTypeSortOrder(list: EventType[]): number {
  return list.reduce((max, t) => Math.max(max, t.sort_order), 0) + 10;
}

/** Enabled types plus `currentId` (which may be disabled), in display order. */
export function assignableEventTypes(list: EventType[], currentId: number | null): EventType[] {
  return sortedEventTypes(list).filter((t) => t.enabled || t.id === currentId);
}

/** The default type's id, or null when the list has none. */
export function defaultEventTypeId(list: EventType[]): number | null {
  return list.find((t) => t.is_default)?.id ?? null;
}

/** A select option's text, marking a disabled type. */
export function eventTypeOptionLabel(type: EventType): string {
  return type.enabled ? type.name : `${type.name} (disabled)`;
}

/** Why a type cannot be deleted, or null when it can. */
export function eventTypeDeleteBlock(type: EventType): string | null {
  if (type.is_default) return 'The default type cannot be deleted. Choose another default first.';
  if (type.workshop_count > 0) return 'Listings use this type. Disable it instead.';
  return null;
}
