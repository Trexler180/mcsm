package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ApprovalPolicy is the resolved answer to three questions about one grant:
// whether approving its requests needs a password step-up, and whether either
// class of action skips human approval entirely.
//
// It is always the product of ResolveApprovalPolicy, never read piecemeal off a
// grant or a settings row, so the precedence rule between the two layers exists
// in exactly one place.
type ApprovalPolicy struct {
	RequirePassword      bool `json:"require_password"`
	AutoApproveLifecycle bool `json:"auto_approve_lifecycle"`
	AutoApproveUpgrades  bool `json:"auto_approve_upgrades"`
}

// SecureApprovalPolicy is what applies when nothing has been configured: the
// behavior this feature shipped with before the policy was configurable. Both
// the zero-rows case and any read error fall back to it, so a database problem
// can never be the reason a step-up was skipped.
func SecureApprovalPolicy() ApprovalPolicy {
	return ApprovalPolicy{RequirePassword: true}
}

// AutoApproves reports whether this policy lets the given action run without a
// human. Upgrades are gated on their own flag: an action that reinstalls the
// runtime and can roll a world back must never ride along on the toggle whose
// stated purpose is "let it restart the server".
func (p ApprovalPolicy) AutoApproves(action string) bool {
	if _, ok := ParseMCPUpgradeAction(action); ok {
		return p.AutoApproveUpgrades
	}
	return p.AutoApproveLifecycle
}

// GetUserApprovalSettings returns a user's account-level defaults. A user with
// no row has never touched the setting, which reads as the secure default
// rather than as an absence to be filled in later.
func (s *Store) GetUserApprovalSettings(ctx context.Context, userID string) (ApprovalPolicy, error) {
	var reqPw, autoLife, autoUp int64
	err := s.db.QueryRowContext(ctx, `
		SELECT require_password, auto_approve_lifecycle, auto_approve_upgrades
		  FROM user_mcp_approval_settings WHERE user_id = ?`, userID).
		Scan(&reqPw, &autoLife, &autoUp)
	if errors.Is(err, sql.ErrNoRows) {
		return SecureApprovalPolicy(), nil
	}
	if err != nil {
		return SecureApprovalPolicy(), err
	}
	return ApprovalPolicy{
		RequirePassword:      reqPw != 0,
		AutoApproveLifecycle: autoLife != 0,
		AutoApproveUpgrades:  autoUp != 0,
	}, nil
}

// SetUserApprovalSettings writes a user's account-level defaults, creating the
// row on first write.
func (s *Store) SetUserApprovalSettings(ctx context.Context, userID string, p ApprovalPolicy) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO user_mcp_approval_settings
			(user_id, require_password, auto_approve_lifecycle, auto_approve_upgrades, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			require_password       = excluded.require_password,
			auto_approve_lifecycle = excluded.auto_approve_lifecycle,
			auto_approve_upgrades  = excluded.auto_approve_upgrades,
			updated_at             = excluded.updated_at`,
		userID, boolToInt(p.RequirePassword), boolToInt(p.AutoApproveLifecycle),
		boolToInt(p.AutoApproveUpgrades), time.Now().UTC())
	return err
}

// GrantPolicyOverrides is the tri-state per-connection layer. A nil field means
// "inherit the account default" and is the state every grant starts in.
type GrantPolicyOverrides struct {
	RequirePassword      *bool `json:"require_password"`
	AutoApproveLifecycle *bool `json:"auto_approve_lifecycle"`
	AutoApproveUpgrades  *bool `json:"auto_approve_upgrades"`
}

// SetGrantPolicyOverrides replaces a grant's overrides. The update is scoped to
// the owner so a grant id alone is never enough to retarget someone else's
// connection.
func (s *Store) SetGrantPolicyOverrides(ctx context.Context, grantID, userID string, o GrantPolicyOverrides) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE mcp_grants
		   SET require_password = ?, auto_approve_lifecycle = ?, auto_approve_upgrades = ?
		 WHERE id = ? AND user_id = ?`,
		boolPtrToInt(o.RequirePassword), boolPtrToInt(o.AutoApproveLifecycle),
		boolPtrToInt(o.AutoApproveUpgrades), grantID, userID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrMCPGrantNotFound
	}
	return nil
}

// ResolveApprovalPolicy answers what actually applies to one grant: the grant's
// own override where it has set one, the owner's account default otherwise.
//
// Both layers are read live on every call rather than cached on the principal,
// matching how authorize() re-reads the grant and the user's RBAC — a policy
// tightened a second ago must take effect on the next call, not the next
// reconnect.
func (s *Store) ResolveApprovalPolicy(ctx context.Context, userID, grantID string) (ApprovalPolicy, error) {
	base, err := s.GetUserApprovalSettings(ctx, userID)
	if err != nil {
		return SecureApprovalPolicy(), err
	}
	if grantID == "" {
		return base, nil
	}
	grant, err := s.getMCPGrant(ctx, grantID)
	if err != nil {
		// A missing grant authorizes nothing anyway; the caller's own checks
		// will reject it. Returning the account default here keeps this
		// function from being the thing that loosens the gate.
		if errors.Is(err, ErrMCPGrantNotFound) {
			return base, nil
		}
		return SecureApprovalPolicy(), err
	}
	return ApplyGrantOverrides(base, grant), nil
}

// ApplyGrantOverrides layers a grant's tri-state overrides onto an account
// default. This is the precedence rule, and it is the only copy of it: callers
// that already hold both rows (the grant list, which would otherwise re-read
// the account row once per grant) use it directly rather than reimplementing
// "non-nil wins".
func ApplyGrantOverrides(base ApprovalPolicy, g *MCPGrant) ApprovalPolicy {
	if g == nil {
		return base
	}
	out := base
	if g.RequirePassword != nil {
		out.RequirePassword = *g.RequirePassword
	}
	if g.AutoApproveLifecycle != nil {
		out.AutoApproveLifecycle = *g.AutoApproveLifecycle
	}
	if g.AutoApproveUpgrades != nil {
		out.AutoApproveUpgrades = *g.AutoApproveUpgrades
	}
	return out
}

// Relaxes reports whether moving from p to next weakens the gate in any
// dimension. Callers use it to demand a step-up for exactly those changes:
// turning protection off is the act worth re-proving your identity for, while
// turning it back on should never be made harder than it needs to be.
func (p ApprovalPolicy) Relaxes(next ApprovalPolicy) bool {
	return (p.RequirePassword && !next.RequirePassword) ||
		(!p.AutoApproveLifecycle && next.AutoApproveLifecycle) ||
		(!p.AutoApproveUpgrades && next.AutoApproveUpgrades)
}

func boolPtrToInt(b *bool) any {
	if b == nil {
		return nil
	}
	return boolToInt(*b)
}

func nullableBool(v *int64) *bool {
	if v == nil {
		return nil
	}
	b := *v != 0
	return &b
}
