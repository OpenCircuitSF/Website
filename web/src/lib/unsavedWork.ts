// unsavedWork — a tiny registry of in-progress admin edits (#0715).
//
// The version check (versionCheck.ts) asks hasUnsavedWork() before reloading
// a tab onto a new build; a form that registers here gets a "Reload" notice
// instead of losing its typing. isEditingField is the safety net for forms
// that do not register: a focused text field also holds the reload back.

const unsaved = new Set<string>();

export function setUnsaved(key: string, value: boolean): void {
  if (value) unsaved.add(key);
  else unsaved.delete(key);
}

export function hasUnsavedWork(): boolean {
  return unsaved.size > 0;
}

export function resetUnsavedForTests(): void {
  unsaved.clear();
}

const NON_TEXT_INPUT_TYPES = new Set([
  'button', 'submit', 'reset', 'checkbox', 'radio', 'file', 'hidden', 'range', 'color', 'image',
]);

export interface FieldLike {
  tagName: string;
  type?: string;
  isContentEditable?: boolean;
}

export function isEditingField(el: FieldLike | null | undefined): boolean {
  if (!el) return false;
  if (el.isContentEditable) return true;
  const tag = el.tagName.toUpperCase();
  if (tag === 'TEXTAREA') return true;
  if (tag === 'INPUT') return !NON_TEXT_INPUT_TYPES.has((el.type ?? 'text').toLowerCase());
  return false;
}
