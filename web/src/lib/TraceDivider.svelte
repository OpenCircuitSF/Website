<script lang="ts">
  // Terminal motif (PRD §4.4): an inline SVG PCB-trace horizontal rule —
  // purely decorative, so the whole thing is aria-hidden (equivalent to
  // <hr> without semantics a screen reader needs). Not present in
  // placeholder/index.html (which used a plain border-top instead); this is
  // a new asset built for the same terminal/PCB visual language as the logo
  // (PRD §4.5) and the rest of the motif set.
  //
  // stroke="currentColor" so the trace color is set by the wrapping element's
  // CSS `color`, matching --accent-dim (the token PRD §4.2 reserves for
  // "rules, dividers" — this is exactly that use).
</script>

<div class="trace-divider" aria-hidden="true">
  <svg viewBox="0 0 400 16" preserveAspectRatio="none" focusable="false">
    <path
      d="M4 8 H150 L162 8 L174 2 L186 2 L198 8 L214 8 L226 14 L238 14 L250 8 H396"
      fill="none"
      stroke="currentColor"
      stroke-width="1.5"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
    <circle cx="4" cy="8" r="2.5" fill="currentColor" />
    <circle cx="396" cy="8" r="2.5" fill="currentColor" />
    <circle cx="174" cy="2" r="1.75" fill="currentColor" />
    <circle cx="226" cy="14" r="1.75" fill="currentColor" />
  </svg>
</div>

<style>
  .trace-divider {
    width: 100%;
    height: 16px;
    color: var(--accent-dim);
  }

  .trace-divider svg {
    display: block;
    width: 100%;
    height: 100%;
  }
</style>
