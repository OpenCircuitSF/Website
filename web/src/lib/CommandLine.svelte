<script lang="ts">
  // Terminal motif (PRD §4.4): "$ command" with a CSS-animated block cursor.
  // Ported from placeholder/index.html's .cmdline / .cursor.
  interface Props {
    command: string;
  }

  let { command }: Props = $props();
</script>

<p class="cmdline">{command}<span class="cursor" aria-hidden="true"></span></p>

<style>
  .cmdline {
    margin: 0;
    font-family: var(--font-mono);
    font-size: clamp(13px, 2.4vw, 15px);
    color: var(--text);
    overflow-wrap: anywhere;
  }

  .cmdline::before {
    content: '$ ';
    color: var(--accent);
  }

  .cursor {
    display: inline-block;
    width: 0.58em;
    height: 1.05em;
    margin-left: 2px;
    background: var(--accent);
    vertical-align: text-bottom;
    animation: blink 1.1s steps(1) infinite;
  }

  @keyframes blink {
    50% {
      opacity: 0;
    }
  }

  /* prefers-reduced-motion is not optional — a blinking cursor is exactly
   * the kind of motion that setting exists for. */
  @media (prefers-reduced-motion: reduce) {
    .cursor {
      animation: none;
    }
  }
</style>
