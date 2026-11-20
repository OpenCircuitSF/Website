package seo

// OnInvalidate registers fn to run at the end of every Invalidate, after the
// site's own caches are cleared, in registration order. A nil fn is
// ignored. cmd/opencircuit registers the public API's list cache here
// (#0730), so any publish or edit that clears the site clears it too.
func (s *Site) OnInvalidate(fn func()) {
	if fn == nil {
		return
	}
	s.hooksMu.Lock()
	s.hooks = append(s.hooks, fn)
	s.hooksMu.Unlock()
}

// runInvalidateHooks calls each OnInvalidate hook outside the lock.
func (s *Site) runInvalidateHooks() {
	s.hooksMu.Lock()
	hooks := append([]func(){}, s.hooks...)
	s.hooksMu.Unlock()
	for _, fn := range hooks {
		fn()
	}
}
