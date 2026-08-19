package devstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/devstore"
)

func TestNew_SeedsAdmin(t *testing.T) {
	s := devstore.New("admin@test.local")

	if s.AdminEmail() != "admin@test.local" {
		t.Errorf("AdminEmail = %q, want %q", s.AdminEmail(), "admin@test.local")
	}
	if s.AdminID() != 1 {
		t.Errorf("AdminID = %d, want 1", s.AdminID())
	}

	ctx := context.Background()
	users, err := s.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("ListUsers = %d users, want 1", len(users))
	}
	if !users[0].IsAdmin {
		t.Error("seeded user should be admin")
	}
}

func TestPing(t *testing.T) {
	s := devstore.New("")
	if err := s.Ping(context.Background()); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

func TestSettings(t *testing.T) {
	s := devstore.New("")
	ctx := context.Background()

	settings, err := s.ListSettings(ctx)
	if err != nil {
		t.Fatalf("ListSettings: %v", err)
	}
	if len(settings) == 0 {
		t.Fatal("expected at least one setting")
	}

	old, err := s.UpdateSetting(ctx, "registrations_enabled", "false", time.Now())
	if err != nil {
		t.Fatalf("UpdateSetting: %v", err)
	}
	if old != "true" {
		t.Errorf("old value = %q, want %q", old, "true")
	}

	// Unknown key.
	_, err = s.UpdateSetting(ctx, "no_such_key", "val", time.Now())
	if err == nil {
		t.Error("expected ErrSettingNotFound for unknown key")
	}
}

func TestSession(t *testing.T) {
	s := devstore.New("admin@test.local")
	ctx := context.Background()

	tok, exp, err := s.CreateDevSession(1, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("CreateDevSession: %v", err)
	}
	if tok == "" {
		t.Error("expected non-empty session token")
	}

	u, err := s.ResolveSession(ctx, tok, time.Now())
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	if u.ID != 1 {
		t.Errorf("session user ID = %d, want 1", u.ID)
	}
	if !u.IsAdmin {
		t.Error("seeded admin should have is_admin=true")
	}

	// Expired session.
	_, err = s.ResolveSession(ctx, tok, exp.Add(time.Hour))
	if err == nil {
		t.Error("expected error for expired session")
	}

	// Unknown token.
	_, err = s.ResolveSession(ctx, "bogus-token", time.Now())
	if err == nil {
		t.Error("expected error for unknown token")
	}
	if err != auth.ErrSessionInvalid {
		t.Errorf("err = %v, want ErrSessionInvalid", err)
	}
}

func TestDeleteSessionsForUser(t *testing.T) {
	s := devstore.New("admin@test.local")
	ctx := context.Background()

	tok1, _, err := s.CreateDevSession(1, time.Hour)
	if err != nil {
		t.Fatalf("CreateDevSession: %v", err)
	}
	tok2, _, err := s.CreateDevSession(1, time.Hour)
	if err != nil {
		t.Fatalf("CreateDevSession: %v", err)
	}

	n, err := s.DeleteSessionsForUser(ctx, 1)
	if err != nil {
		t.Fatalf("DeleteSessionsForUser: %v", err)
	}
	if n != 2 {
		t.Errorf("DeleteSessionsForUser = %d, want 2", n)
	}

	for _, tok := range []string{tok1, tok2} {
		if _, err := s.ResolveSession(ctx, tok, time.Now()); err == nil {
			t.Errorf("expected session %q to be invalid after DeleteSessionsForUser", tok)
		}
	}
}

func TestDeactivateReactivateUser(t *testing.T) {
	s := devstore.New("admin@test.local")
	ctx := context.Background()

	// The seeded user is an admin; deactivating an admin must fail.
	if _, err := s.DeactivateUser(ctx, 1, nil, audit.Entry{}); err != auth.ErrUserIsAdmin {
		t.Errorf("DeactivateUser(admin) err = %v, want ErrUserIsAdmin", err)
	}

	if _, err := s.DeactivateUser(ctx, 999, nil, audit.Entry{}); err != auth.ErrUserNotFound {
		t.Errorf("DeactivateUser(unknown) err = %v, want ErrUserNotFound", err)
	}
}

func TestListAuditLog_Empty(t *testing.T) {
	s := devstore.New("")
	ctx := context.Background()

	records, total, err := s.ListAuditLog(ctx, nil, 50, 0)
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}
	if total != 0 || len(records) != 0 {
		t.Errorf("ListAuditLog = %d records (total %d), want 0/0 on a fresh store", len(records), total)
	}
}
