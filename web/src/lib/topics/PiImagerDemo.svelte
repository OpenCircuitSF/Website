<script lang="ts">
  // Raspberry Pi Imager guide (#0719): switch OS customisation settings off
  // and see what the first boot looks like from your laptop. The outcomes
  // are in piSetup.ts.
  import {
    DEFAULT_CHOICE,
    DEMO_USER,
    IMAGER_SETTINGS,
    firstBoot,
    hostFor,
    isValidHostname,
    type SettingId,
    type SshAuth,
  } from './piSetup';

  let on = $state<Record<SettingId, boolean>>({ ...DEFAULT_CHOICE.on });
  let sshAuth = $state<SshAuth>(DEFAULT_CHOICE.sshAuth);
  let hostname = $state(DEFAULT_CHOICE.hostname);

  const choice = $derived({ on: { ...on }, sshAuth, hostname });
  const boot = $derived(firstBoot(choice));
  const host = $derived(hostFor(choice));
  const hostOk = $derived(isValidHostname(hostname));
  const svgLabel = $derived(
    `${boot.title}. The Pi ${boot.joins ? 'is on the network' : 'is not connected to the router'}, SSH from the laptop ${boot.canLogIn ? 'works' : 'fails'}, and it answers to ${host}.`,
  );
  const AUTHS: { id: SshAuth; label: string }[] = [
    { id: 'password', label: 'Password' },
    { id: 'key', label: 'Public key' },
  ];
</script>

<div class="imager">
  <fieldset>
    <legend>OS customisation</legend>
    {#each IMAGER_SETTINGS as s (s.id)}
      <div class="setting">
        <label>
          <input type="checkbox" bind:checked={on[s.id]} />
          <span><strong>{s.label}</strong> <span class="detail">{s.detail}</span></span>
        </label>
        {#if s.id === 'hostname' && on.hostname}
          <label class="sub">
            Hostname <input type="text" bind:value={hostname} spellcheck="false" autocomplete="off" />
          </label>
          {#if !hostOk}
            <p class="note">Letters, digits and hyphens only, not at the start or end. Falls back to raspberrypi.</p>
          {/if}
        {/if}
        {#if s.id === 'ssh' && on.ssh}
          <div class="nets" role="radiogroup" aria-label="SSH login">
            {#each AUTHS as a (a.id)}
              <button type="button" role="radio" aria-checked={sshAuth === a.id} class:on={sshAuth === a.id} onclick={() => (sshAuth = a.id)}>
                <strong>{a.label}</strong>
              </button>
            {/each}
          </div>
        {/if}
      </div>
    {/each}
  </fieldset>

  <svg viewBox="0 0 360 160" role="img" aria-label={svgLabel}>
    <!-- laptop -->
    <rect x="14" y="58" width="64" height="40" rx="3" class="box" />
    <rect x="6" y="98" width="80" height="6" rx="2" class="box" />
    <text x="46" y="124" class="lbl">laptop</text>
    <!-- router -->
    <rect x="150" y="74" width="60" height="24" rx="3" class="box" />
    <line x1="165" y1="74" x2="160" y2="58" class="wire" />
    <line x1="195" y1="74" x2="200" y2="58" class="wire" />
    <text x="180" y="124" class="lbl">router</text>
    <line x1="86" y1="86" x2="150" y2="86" class="link" />
    <!-- Pi -->
    <rect x="270" y="70" width="70" height="34" rx="4" class="pi" />
    <text x="305" y="92" class="pitxt">Pi</text>
    <text x="305" y="124" class="lbl">{host}</text>
    {#if boot.joins}
      <line x1="210" y1="86" x2="270" y2="86" class="link" />
    {:else}
      <line x1="210" y1="86" x2="270" y2="86" class="link broken" />
      <text x="240" y="80" class="x">✕</text>
    {/if}
    <!-- SSH arrow -->
    <path d="M46 56 Q180 6 300 66" class={boot.canLogIn ? 'ssh' : 'ssh broken'} />
    <text x="180" y="26" class="lbl">SSH{boot.canLogIn ? '' : ' ✕'}</text>
    {#if !on.user}
      <rect x="300" y="12" width="44" height="30" rx="2" class="box" />
      <text x="322" y="33" class="q">?</text>
    {/if}
  </svg>

  <div class="layout">
    <div class="monitor" role="group" aria-label="Laptop terminal">
      {#each boot.terminal as line, i (i)}
        <div>{line}</div>
      {/each}
    </div>
    <div class="result {boot.tone}" aria-live="polite">
      <h3>{boot.title}</h3>
      {#if boot.problems.length}
        <ul>
          {#each boot.problems as p, i (i)}
            <li>{p}</li>
          {/each}
        </ul>
      {:else}
        <p>Every setting is in place.</p>
      {/if}
    </div>
  </div>

  <h4>Once it’s up</h4>
  {#if sshAuth === 'key' && on.ssh}
    <p>Make a key on your laptop first. Imager can fill in your public key from <code>~/.ssh/id_ed25519.pub</code>.</p>
    <pre><code>ssh-keygen -t ed25519
ssh {DEMO_USER}@{host}</code></pre>
  {:else}
    <pre><code>ssh {DEMO_USER}@{host}</code></pre>
  {/if}
</div>

<style>
  fieldset {
    border: var(--border-w) solid var(--border-strong);
    border-radius: 6px;
    padding: var(--space-3);
    margin: 0 0 var(--space-4);
    display: grid;
    gap: var(--space-2);
  }
  legend {
    font-weight: 600;
    padding: 0 var(--space-1);
  }
  .setting label {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: var(--space-2);
    margin: 0;
    font-size: var(--fs-md);
    font-weight: 400;
    color: var(--text);
  }
  .detail,
  .note {
    font-size: var(--fs-sm);
    color: var(--text-faint);
  }
  .sub {
    margin: var(--space-1) 0 0 var(--space-5) !important;
  }
  .sub input {
    font: inherit;
    font-family: var(--font-mono);
    padding: 2px var(--space-2);
    border: var(--border-w) solid var(--border-strong);
    border-radius: 4px;
    background: var(--bg-panel);
    color: var(--text);
  }
  .note {
    margin: var(--space-1) 0 0 var(--space-5);
  }
  .nets {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
    gap: var(--space-2);
    margin: var(--space-1) 0 0 var(--space-5);
  }
  .nets button {
    text-align: left;
    font: inherit;
    font-size: var(--fs-base);
    padding: var(--space-2) var(--space-3);
    border-radius: 6px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  .nets button.on {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-text);
  }
  svg {
    display: block;
    width: 100%;
    max-width: 480px;
    margin: 0 auto var(--space-4);
  }
  .box {
    fill: var(--bg-panel);
    stroke: var(--text);
    stroke-width: 1.5;
  }
  .pi {
    fill: var(--bg-panel);
    stroke: var(--accent);
    stroke-width: 2;
  }
  .pitxt,
  .q {
    fill: var(--text);
    font: 700 14px var(--font-mono);
    text-anchor: middle;
  }
  .lbl {
    fill: var(--text-faint);
    font: 11px var(--font-mono);
    text-anchor: middle;
  }
  .wire {
    stroke: var(--text);
    stroke-width: 1.5;
  }
  .link {
    stroke: var(--accent);
    stroke-width: 3;
  }
  .ssh {
    fill: none;
    stroke: var(--accent);
    stroke-width: 2;
  }
  .broken {
    stroke: var(--danger);
    stroke-dasharray: 6 4;
  }
  .x {
    fill: var(--danger);
    font: 700 16px var(--font-mono);
    text-anchor: middle;
  }
  .layout {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: var(--space-4);
    align-items: start;
  }
  .monitor {
    padding: var(--space-3);
    min-height: 6em;
    background: var(--guide-pcb);
    color: #d8e0d8;
    border-radius: 6px;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    overflow-wrap: anywhere;
  }
  .result {
    padding: var(--space-4);
    border: 2px solid var(--accent);
    border-radius: 6px;
    background: var(--bg-panel);
  }
  .result.meh {
    border-color: var(--warning);
  }
  .result.bad {
    border-color: var(--danger);
  }
  h3 {
    margin: 0 0 var(--space-2);
  }
  .result ul,
  .result p {
    margin: 0;
    font-size: var(--fs-md);
  }
  h4 {
    margin: var(--space-4) 0 var(--space-1);
  }
  pre {
    margin: var(--space-2) 0 0;
    padding: var(--space-3);
    overflow-x: auto;
    background: var(--bg-panel);
    border: var(--border-w) solid var(--border);
    border-radius: 6px;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
  }
</style>
