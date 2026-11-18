// The visitor's temperature unit for the guides: one site-wide choice,
// remembered in this browser. Every storage access is guarded, because
// private windows and blocked storage throw; failing safe means °F.
import { DEFAULT_TEMP_UNIT, TEMP_UNIT_KEY, parseTempUnit, type TempUnit } from './temperature';

function readStored(): TempUnit {
  try {
    return parseTempUnit(localStorage.getItem(TEMP_UNIT_KEY));
  } catch {
    return DEFAULT_TEMP_UNIT;
  }
}

export const tempUnit = $state<{ value: TempUnit }>({ value: readStored() });

export function setTempUnit(u: TempUnit): void {
  tempUnit.value = u;
  try {
    localStorage.setItem(TEMP_UNIT_KEY, u);
  } catch {
    // Ignore: the choice just won't survive a reload.
  }
}

/** Test hook: back to the default without touching storage. */
export function resetTempUnitForTest(): void {
  tempUnit.value = DEFAULT_TEMP_UNIT;
}
