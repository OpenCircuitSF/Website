package api

// Defined reports whether op was built with Define. The public registry
// (internal/publicapi, #0729) refuses an operation that was not, since it
// has no call function, schemas or bound fields.
func (op Operation) Defined() bool { return op.call != nil }
