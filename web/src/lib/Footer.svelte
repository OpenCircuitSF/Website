<script lang="ts">
  // Site-wide footer (#0017): rendered under every view, public or
  // authenticated, matching the original Footer.svelte's placement in
  // App.svelte. Carries the legal/social links the header intentionally
  // does NOT: a Discord invite, the contact address, a privacy policy link,
  // copyright, and -- per the acceptance criteria ("staff sign-in is
  // reachable but unobtrusive, a footer link not primary nav") -- the one
  // path to /login that isn't in the header.
  //
  // The privacy policy page (#0070) now lives at /privacy -- PRD §11 requires
  // one, since the site collects email, interests, signup IP, and UTM
  // source. Before #0070 landed, this same link pointed at a route the
  // router didn't recognize yet and resolved to the terminal-styled 404
  // (#0022) -- a more honest failure mode than omitting the link PRD §11
  // requires, but a 404 nonetheless.
  import { APP_NAME } from './branding';
  import { DISCORD_URL, LUMA_URL } from './links';

  const CONTACT_EMAIL = 'contact@opencircuitsf.com';
  const year = new Date().getFullYear();
</script>

<footer class="site-footer">
  <div class="footer-inner">
    <nav class="footer-links" aria-label="Footer">
      <a href={DISCORD_URL} target="_blank" rel="noopener noreferrer">Discord</a>
      <span class="sep" aria-hidden="true">·</span>
      <a href={LUMA_URL} target="_blank" rel="noopener noreferrer">Calendar</a>
      <span class="sep" aria-hidden="true">·</span>
      <a href="/archive">Archive</a>
      <span class="sep" aria-hidden="true">·</span>
      <a href="mailto:{CONTACT_EMAIL}">{CONTACT_EMAIL}</a>
      <span class="sep" aria-hidden="true">·</span>
      <a href="/privacy">Privacy policy</a>
      <span class="sep" aria-hidden="true">·</span>
      <a href="/login">Staff sign-in</a>
    </nav>
    <p class="copyright">&copy; {year} {APP_NAME}</p>
  </div>
</footer>

<style>
  .site-footer {
    border-top: var(--border-w) solid var(--border);
    background: var(--bg-panel);
    margin-top: var(--space-6);
  }

  .footer-inner {
    max-width: 1040px;
    margin: 0 auto;
    padding: var(--space-4);
    text-align: center;
  }

  .footer-links {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: center;
    gap: var(--space-2);
    font-size: var(--fs-sm);
  }

  .footer-links a {
    color: var(--text-faint);
    text-decoration: none;
    padding: var(--space-1) 0;
  }
  .footer-links a:hover {
    color: var(--text-muted);
    text-decoration: underline;
  }

  .sep {
    color: var(--border-strong);
  }

  .copyright {
    margin: var(--space-2) 0 0;
    font-size: var(--fs-sm);
    color: var(--text-faint);
  }

  @media (max-width: 480px) {
    /* Match the header's >=40px tap target rule for footer links too. */
    .footer-links a {
      min-height: 40px;
      display: inline-flex;
      align-items: center;
    }
  }
</style>
