// Guard (#0713): guide content never hard-codes a °C or °F temperature.
// Every reader-facing temperature goes through lib/temperature.ts (a
// {temp:…} token or formatTemp), so it follows the visitor's unit switch.
// Comments are not reader-facing and °C/W is a rate, not a temperature.
import { describe, expect, it } from 'vitest';

const BS = String.fromCharCode(92);
const DEG = `(?:°|&deg;|${BS}${BS}u00[bB]0)`;
const LITERAL = new RegExp(`${DEG}\\s*[CF](?!/W)`, 'g');

function findTempLiterals(src: string): string[] {
  const hits: string[] = [];
  for (const line of src.split('\n')) {
    const t = line.trim();
    if (t.startsWith('//') || t.startsWith('/*') || t.startsWith('*') || t.startsWith('<!--')) continue;
    for (const m of line.matchAll(LITERAL)) hits.push(`${m[0]} in: ${t}`);
  }
  return hits;
}

const sources = Object.fromEntries(
  Object.entries(
    import.meta.glob(['./*.ts', './*.svelte', '../TempUnitToggle.svelte', '../../views/TopicGuide.svelte', '../../content/guides/guides.json'], {
      query: '?raw',
      import: 'default',
      eager: true,
    }) as Record<string, string>,
  ).filter(([k]) => !k.endsWith('.test.ts')),
);

describe('guide temperature literals', () => {
  it('flags a hard-coded unit literal in guide content', () => {
    expect(findTempLiterals("'melts at 183 °C'")).toHaveLength(1);
    expect(findTempLiterals('375&deg;F')).toHaveLength(1);
    expect(findTempLiterals(`at 183 ${BS}u00b0C`)).toHaveLength(1);
    expect(findTempLiterals('100 ° F')).toHaveLength(1);
  });

  it('ignores comments and °C/W rates', () => {
    expect(findTempLiterals('  // 183 °C')).toEqual([]);
    expect(findTempLiterals('  /** at 25 °C */')).toEqual([]);
    expect(findTempLiterals('   * 25 °C')).toEqual([]);
    expect(findTempLiterals('<!-- 183 °C -->')).toEqual([]);
    expect(findTempLiterals("'62 °C/W'")).toEqual([]);
  });

  it('no guide source hard-codes °C or °F', () => {
    const keys = Object.keys(sources);
    expect(keys.length).toBeGreaterThanOrEqual(60);
    expect(keys).toContain('./IronTempDemo.svelte');
    expect(keys).toContain('./topics.ts');
    expect(keys).toContain('../../content/guides/guides.json');
    for (const [k, src] of Object.entries(sources)) expect(findTempLiterals(src), k).toEqual([]);
  });
});
