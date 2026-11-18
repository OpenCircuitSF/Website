import { afterEach, describe, expect, it } from 'vitest';
import { hasUnsavedWork, isEditingField, resetUnsavedForTests, setUnsaved } from './unsavedWork';

describe('unsavedWork', () => {
  afterEach(() => resetUnsavedForTests());

  it('setUnsaved marks and clears per key', () => {
    setUnsaved('a', true);
    expect(hasUnsavedWork()).toBe(true);
    setUnsaved('a', false);
    expect(hasUnsavedWork()).toBe(false);
  });

  it('hasUnsavedWork stays true while any key is unsaved', () => {
    setUnsaved('a', true);
    setUnsaved('b', true);
    setUnsaved('a', false);
    expect(hasUnsavedWork()).toBe(true);
    setUnsaved('b', false);
    expect(hasUnsavedWork()).toBe(false);
  });

  it('isEditingField is true for text inputs, textareas and contenteditable', () => {
    expect(isEditingField({ tagName: 'INPUT', type: 'text' })).toBe(true);
    expect(isEditingField({ tagName: 'INPUT', type: 'email' })).toBe(true);
    expect(isEditingField({ tagName: 'INPUT' })).toBe(true);
    expect(isEditingField({ tagName: 'TEXTAREA' })).toBe(true);
    expect(isEditingField({ tagName: 'DIV', isContentEditable: true })).toBe(true);
  });

  it('isEditingField is false for buttons, checkboxes, null and non-fields', () => {
    expect(isEditingField({ tagName: 'INPUT', type: 'checkbox' })).toBe(false);
    expect(isEditingField({ tagName: 'INPUT', type: 'submit' })).toBe(false);
    expect(isEditingField({ tagName: 'BUTTON' })).toBe(false);
    expect(isEditingField({ tagName: 'BODY' })).toBe(false);
    expect(isEditingField(null)).toBe(false);
  });
});
