<script lang="ts">
  // Which Raspberry Pi (#0723): tick what the project needs and see which
  // board fits. The rule and the reasons are in piStart.ts.
  import { NEEDS, PI_MODELS, SPEC_ROWS, recommendBoard, type NeedId, type PiModelId } from './piStart';

  let needs = $state<NeedId[]>([]);
  const pick = $derived(recommendBoard(needs));
  const out = $derived(new Set(pick.ruledOut.map((r) => r.board)));
  const svgLabel = $derived(
    `Pick: ${pick.label}.${pick.ruledOut.length ? ` Ruled out: ${pick.ruledOut.map((r) => r.label).join(', ')}.` : ''}`,
  );

  // Outlines roughly to scale: Zero 2W 65×30 mm, Pi 4 and Pi 5 85×56 mm.
  const BOXES: Record<PiModelId, { x: number; y: number; w: number; h: number }> = {
    zero2w: { x: 14, y: 52, w: 104, h: 48 },
    pi4: { x: 136, y: 30, w: 136, h: 90 },
    pi5: { x: 284, y: 30, w: 136, h: 90 },
  };

  function toggle(id: NeedId, on: boolean) {
    needs = on ? [...needs, id] : needs.filter((n) => n !== id);
  }
</script>

<!-- #0727: illustration slot -->
<div class="picker">
  <fieldset>
    <legend>What does the project need?</legend>
    {#each NEEDS as n (n.id)}
      <label>
        <input type="checkbox" checked={needs.includes(n.id)} onchange={(e) => toggle(n.id, e.currentTarget.checked)} />
        {n.label}
      </label>
    {/each}
  </fieldset>

  <svg viewBox="0 0 434 150" role="img" aria-label={svgLabel}>
    {#each PI_MODELS as m (m.id)}
      {@const b = BOXES[m.id]}
      <rect x={b.x} y={b.y} width={b.w} height={b.h} rx="4" class="board" class:pick={pick.board === m.id} class:out={out.has(m.id)} />
      <text x={b.x + b.w / 2} y="140" class="lbl" class:bad={out.has(m.id)}>
        {m.label}{pick.board === m.id ? ' ✓ pick' : out.has(m.id) ? ' ✕' : ''}
      </text>
    {/each}
  </svg>

  <div class="result" aria-live="polite">
    <h3>{pick.label}</h3>
    <ul>
      {#each pick.reasons as r, i (i)}<li>{r}</li>{/each}
    </ul>
    {#if pick.compromises.length}
      <h4>What you give up</h4>
      <ul>
        {#each pick.compromises as c, i (i)}<li>{c}</li>{/each}
      </ul>
    {/if}
    {#if pick.ruledOut.length}
      <h4>Ruled out</h4>
      <ul>
        {#each pick.ruledOut as r (r.board)}<li><strong>{r.label}</strong>: {r.why}</li>{/each}
      </ul>
    {/if}
  </div>

  <div class="scroll">
    <table>
      <caption>Specs</caption>
      <thead>
        <tr>
          <td></td>
          {#each PI_MODELS as m (m.id)}
            <th scope="col" class:picked={pick.board === m.id}>{m.label}{pick.board === m.id ? ' ✓' : ''}</th>
          {/each}
        </tr>
      </thead>
      <tbody>
        {#each SPEC_ROWS as row (row.key)}
          <tr>
            <th scope="row">{row.label}</th>
            {#each PI_MODELS as m (m.id)}
              <td class:picked={pick.board === m.id}>{m.specs[row.key]}</td>
            {/each}
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  <p class="note">Idle power is a rough figure; it depends on what is plugged in.</p>

  <h4>Related guides</h4>
  <ul class="related">
    <li><a href="/guides/pi-wifi-2-4ghz">Why a Zero 2W never sees 5 GHz WiFi</a></li>
    <li><a href="/guides/pi-rgb-matrix-flicker">RGB LED panels on a Pi 4</a></li>
    <li><a href="/guides/pi-volumio-streaming">A streaming speaker with Volumio</a></li>
    <li><a href="/guides/pi-undervoltage">Power supplies and undervoltage</a></li>
    <li><a href="/guides/pi-zero-usb-ports">The Zero 2W’s USB ports</a></li>
    <li><a href="/guides/pi-zero-audio-out">Sound out of a Zero 2W</a></li>
  </ul>
</div>

<style>
  fieldset {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
    gap: var(--space-1) var(--space-3);
    margin: 0 0 var(--space-4);
    padding: var(--space-3);
    border: var(--border-w) solid var(--border-strong);
    border-radius: 6px;
  }
  legend {
    font-weight: 600;
    padding: 0 var(--space-1);
  }
  label {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    cursor: pointer;
  }
  svg {
    display: block;
    width: 100%;
    max-width: 480px;
    margin: 0 auto var(--space-4);
  }
  .board {
    fill: var(--bg-panel);
    stroke: var(--text-faint);
    stroke-width: 1.5;
  }
  .board.pick {
    stroke: var(--accent);
    stroke-width: 4;
  }
  .board.out {
    stroke: var(--danger);
    stroke-dasharray: 6 4;
  }
  .lbl {
    fill: var(--text);
    font: 11px var(--font-mono);
    text-anchor: middle;
  }
  .lbl.bad {
    fill: var(--danger);
  }
  .result {
    padding: var(--space-4);
    border: 2px solid var(--accent);
    border-radius: 6px;
    background: var(--bg-panel);
  }
  h3 {
    margin: 0 0 var(--space-1);
  }
  h4 {
    margin: var(--space-3) 0 var(--space-1);
  }
  .result ul {
    margin: 0;
    padding-left: var(--space-5);
  }
  .scroll {
    overflow-x: auto;
    margin-top: var(--space-4);
  }
  table {
    border-collapse: collapse;
    font-size: var(--fs-sm);
    width: 100%;
  }
  caption {
    text-align: left;
    font-weight: 600;
    margin-bottom: var(--space-1);
  }
  th,
  td {
    text-align: left;
    padding: 4px 8px;
    border-bottom: var(--border-w) solid var(--border);
  }
  .picked {
    background: var(--accent-subtle);
  }
  .note {
    font-size: var(--fs-sm);
    color: var(--text-faint);
  }
  .related {
    margin: 0;
    padding-left: var(--space-5);
  }
</style>
