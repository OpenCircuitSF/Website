// Pure validation and shaping for the Outreach Locations sub-view (#0548).
// No DOM and no fetch: the Svelte component is markup and wiring. The code
// rule (2-16 characters, lowercase letters, digits, single interior dashes)
// is the same one internal/outreach.ValidCode and the table's CHECK enforce;
// the Go test file carries the same case list by hand.

import type { OutreachLocation } from './types';

export const CODE_MIN = 2;
export const CODE_MAX = 16;
export const CODE_PATTERN = /^[a-z0-9]+(-[a-z0-9]+)*$/;
export const NAME_MAX = 100;
export const ADDRESS_MAX = 300;
export const NOTES_MAX = 2000;

/** null when the code is valid, else a message for the field. */
export function validateCode(code: string): string | null {
  if (code === '') return 'Code is required.';
  if (code.length < CODE_MIN) return `Code must be at least ${CODE_MIN} characters.`;
  if (code.length > CODE_MAX) return `Code must be at most ${CODE_MAX} characters.`;
  if (!CODE_PATTERN.test(code)) {
    return 'Use lowercase letters, digits and single dashes; no leading or trailing dash.';
  }
  return null;
}

/** Applied as the admin types in the code field. Does not truncate: the
 * length is reported by validateCode instead. */
export function normalizeCodeInput(raw: string): string {
  return raw.toLowerCase().replace(/[\s_]+/g, '-');
}

/** A short code suggested from a location name, or '' when none fits. */
export function suggestCode(name: string): string {
  let s = name
    .normalize('NFKD')
    .replace(/\p{M}/gu, '')
    .toLowerCase()
    .replace(/&/g, ' and ')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
  if (s.length > CODE_MAX) {
    const cut = s.lastIndexOf('-', CODE_MAX);
    s = cut >= CODE_MIN ? s.slice(0, cut) : s.slice(0, CODE_MAX);
    s = s.replace(/-+$/, '');
  }
  return s.length < CODE_MIN ? '' : s;
}

/** base if free, else base-2, base-3, ... with base shortened so the result
 * stays within CODE_MAX. */
export function uniqueCode(base: string, taken: ReadonlySet<string>): string {
  if (base === '' || !taken.has(base)) return base;
  for (let n = 2; ; n++) {
    const suffix = `-${n}`;
    const stem = base.slice(0, CODE_MAX - suffix.length).replace(/-+$/, '');
    const candidate = stem + suffix;
    if (!taken.has(candidate)) return candidate;
  }
}

export interface LocationForm {
  name: string;
  code: string;
  address: string;
  notes: string;
}

export type LocationFormErrors = Partial<Record<'name' | 'code' | 'address' | 'notes', string>>;

export function validateLocationForm(form: LocationForm): LocationFormErrors {
  const errors: LocationFormErrors = {};
  const name = form.name.trim();
  if (name === '') errors.name = 'Name is required.';
  else if ([...name].length > NAME_MAX) errors.name = `Name must be at most ${NAME_MAX} characters.`;
  const codeError = validateCode(form.code);
  if (codeError) errors.code = codeError;
  if ([...form.address.trim()].length > ADDRESS_MAX) errors.address = `Address must be at most ${ADDRESS_MAX} characters.`;
  if ([...form.notes.trim()].length > NOTES_MAX) errors.notes = `Notes must be at most ${NOTES_MAX} characters.`;
  return errors;
}

export function formIsValid(errors: LocationFormErrors): boolean {
  return Object.keys(errors).length === 0;
}

export type LocationPatchBody = Partial<LocationForm>;

/** Only the changed fields (trimmed, except code); null when nothing changed.
 * The code is never included while it is locked and unchanged. */
export function locationPatch(original: OutreachLocation, form: LocationForm): LocationPatchBody | null {
  const patch: LocationPatchBody = {};
  const name = form.name.trim();
  const address = form.address.trim();
  const notes = form.notes.trim();
  if (name !== original.name) patch.name = name;
  if (address !== original.address) patch.address = address;
  if (notes !== original.notes) patch.notes = notes;
  if (form.code !== original.code) patch.code = form.code;
  return Object.keys(patch).length > 0 ? patch : null;
}

export function locationStatusLabel(loc: OutreachLocation): 'Active' | 'Inactive' {
  return loc.active ? 'Active' : 'Inactive';
}

export function canDelete(loc: OutreachLocation): boolean {
  return !loc.code_locked;
}

/** A new location (null) or an unlocked one may change its code. */
export function canEditCode(loc: OutreachLocation | null): boolean {
  return loc === null || !loc.code_locked;
}
