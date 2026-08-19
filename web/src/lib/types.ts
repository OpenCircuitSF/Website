// TypeScript interfaces mirroring the Go backend's JSON shapes. Field names are
// snake_case to match the API exactly (see internal/handlers/*.go). These are the
// shared contract the views build on.

/** GET /api/me — the current user profile used to gate the admin view. */
export interface User {
  id: number;
  email: string;
  is_admin: boolean;
}

/**
 * A registered passkey (GET /account/credentials item). Matches
 * internal/handlers/credentials.go `credentialView`.
 */
export interface Credential {
  id: number;
  device_name: string;
  aaguid: string;
  device_hint: string;
  sign_count: number;
  created_at: string;
  last_used_at: string | null;
}

/**
 * One audit-log row (GET /admin/audit item). Matches
 * internal/handlers/audit.go `auditRecordView`.
 */
export interface AuditEntry {
  id: number;
  actor_id: number | null;
  user_id: number | null;
  action: string;
  target_type: string | null;
  target_id: number | null;
  metadata: unknown;
  ip_address: string | null;
  created_at: string;
}

/** An admin-visible user account (GET /admin/users item). */
export interface AdminUser {
  id: number;
  email: string;
  is_admin: boolean;
  active: boolean;
  created_at: string;
  last_login_at?: string;
}

/** A runtime server setting (GET /admin/settings item). */
export interface Setting {
  key: string;
  value: string;
  updated_at?: string;
}
