package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrNoGrant means the user has no explicit grant for the asset. Administrators
// and roles with asset.read.all bypass grants; everyone else is denied.
var ErrNoGrant = errors.New("asset grant not found")

// ErrSessionInvalid covers expired, revoked and disabled-user sessions. The
// caller should treat it as 401, not as an internal error.
var ErrSessionInvalid = errors.New("auth session invalid")

// ErrTicketInvalid covers missing, expired, already redeemed and unknown RDP
// tickets. Callers should return 401/410 without revealing which case occurred.
var ErrTicketInvalid = errors.New("rdp ticket invalid")

type User struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name"`
	Role        string     `json:"role"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

type AuthSession struct {
	ID          string
	UserID      string
	Username    string
	DisplayName string
	Role        string
	Status      string
	CSRF        string
	ExpiresAt   time.Time
}

type AssetGrant struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	AssetID     string `json:"asset_id"`
	CanEdit     bool   `json:"can_edit"`
	CanConnect  bool   `json:"can_connect"`
}

type AssetGrantInput struct {
	UserID     string `json:"user_id"`
	CanEdit    bool   `json:"can_edit"`
	CanConnect bool   `json:"can_connect"`
}

type RDPTicket struct {
	UserID    string
	AssetID   string
	Options   string
	ExpiresAt time.Time
}

type AuditEvent struct {
	ID          string          `json:"id"`
	OccurredAt  time.Time       `json:"occurred_at"`
	ActorUserID string          `json:"actor_user_id,omitempty"`
	Action      string          `json:"action"`
	ObjectType  string          `json:"object_type"`
	ObjectID    string          `json:"object_id"`
	Result      string          `json:"result"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
}

const authSchema = `
CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  username TEXT NOT NULL COLLATE NOCASE UNIQUE,
  display_name TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL DEFAULT 'viewer',
  status TEXT NOT NULL DEFAULT 'active',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  last_login_at TEXT
);
CREATE TABLE IF NOT EXISTS auth_sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  csrf_token TEXT NOT NULL,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  ip TEXT NOT NULL DEFAULT '',
  user_agent TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_auth_sessions_user ON auth_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_auth_sessions_expiry ON auth_sessions(expires_at);
CREATE TABLE IF NOT EXISTS asset_grants (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  asset_id TEXT NOT NULL,
  can_edit INTEGER NOT NULL DEFAULT 0,
  can_connect INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY(user_id, asset_id)
);
CREATE INDEX IF NOT EXISTS idx_asset_grants_asset ON asset_grants(asset_id);
CREATE TABLE IF NOT EXISTS rdp_tickets (
  ticket_hash TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  asset_id TEXT NOT NULL,
  options_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  used_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_rdp_tickets_expiry ON rdp_tickets(expires_at);
`

func (s *Store) migrateAuth(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, authSchema); err != nil {
		return err
	}
	// Audit rows created before multi-user support have no actor; keep the
	// column default so old databases migrate in place.
	_, err := s.db.ExecContext(ctx, `ALTER TABLE audit_events ADD COLUMN actor_user_id TEXT NOT NULL DEFAULT ''`)
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return err
	}
	return nil
}

func parseStoreTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	return count, err
}

func (s *Store) CreateUser(ctx context.Context, username, displayName, passwordHash, role string) (User, error) {
	username = strings.TrimSpace(username)
	displayName = strings.TrimSpace(displayName)
	if username == "" || passwordHash == "" {
		return User{}, errors.New("username and password are required")
	}
	if displayName == "" {
		displayName = username
	}
	now := time.Now().UTC()
	user := User{ID: uuid.NewString(), Username: username, DisplayName: displayName, Role: role, Status: "active", CreatedAt: now, UpdatedAt: now}
	_, err := s.db.ExecContext(ctx, `INSERT INTO users(id,username,display_name,password_hash,role,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		user.ID, user.Username, user.DisplayName, passwordHash, user.Role, user.Status, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return User{}, err
	}
	return user, nil
}

func scanUser(row *sql.Row) (User, string, error) {
	var user User
	var lastLogin sql.NullString
	var createdAt, updatedAt string
	if err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &user.Role, &user.Status, &createdAt, &updatedAt, &lastLogin); err != nil {
		return User{}, "", err
	}
	user.CreatedAt = parseStoreTime(createdAt)
	user.UpdatedAt = parseStoreTime(updatedAt)
	if lastLogin.Valid && lastLogin.String != "" {
		value := parseStoreTime(lastLogin.String)
		user.LastLoginAt = &value
	}
	return user, "", nil
}

func (s *Store) GetUser(ctx context.Context, id string) (User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,username,display_name,role,status,created_at,updated_at,last_login_at FROM users WHERE id=?`, id)
	user, _, err := scanUser(row)
	return user, err
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (User, string, error) {
	var user User
	var hash string
	var lastLogin sql.NullString
	var createdAt, updatedAt string
	row := s.db.QueryRowContext(ctx, `SELECT id,username,display_name,password_hash,role,status,created_at,updated_at,last_login_at FROM users WHERE username=?`, strings.TrimSpace(username))
	if err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &hash, &user.Role, &user.Status, &createdAt, &updatedAt, &lastLogin); err != nil {
		return User{}, "", err
	}
	user.CreatedAt = parseStoreTime(createdAt)
	user.UpdatedAt = parseStoreTime(updatedAt)
	if lastLogin.Valid && lastLogin.String != "" {
		value := parseStoreTime(lastLogin.String)
		user.LastLoginAt = &value
	}
	return user, hash, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,username,display_name,role,status,created_at,updated_at,last_login_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		var user User
		var lastLogin sql.NullString
		var createdAt, updatedAt string
		if err := rows.Scan(&user.ID, &user.Username, &user.DisplayName, &user.Role, &user.Status, &createdAt, &updatedAt, &lastLogin); err != nil {
			return nil, err
		}
		user.CreatedAt = parseStoreTime(createdAt)
		user.UpdatedAt = parseStoreTime(updatedAt)
		if lastLogin.Valid && lastLogin.String != "" {
			value := parseStoreTime(lastLogin.String)
			user.LastLoginAt = &value
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) UpdateUserRole(ctx context.Context, id, role string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE users SET role=?, updated_at=? WHERE id=?`, role, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (s *Store) UpdateUserDisplayName(ctx context.Context, id, displayName string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE users SET display_name=?, updated_at=? WHERE id=?`, strings.TrimSpace(displayName), time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (s *Store) UpdateUserStatus(ctx context.Context, id, status string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE users SET status=?, updated_at=? WHERE id=?`, status, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	if err := requireAffected(result); err != nil {
		return err
	}
	if status != "active" {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE user_id=?`, id)
	}
	return nil
}

func (s *Store) UpdateUserPassword(ctx context.Context, id, passwordHash string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash=?, updated_at=? WHERE id=?`, passwordHash, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	if err := requireAffected(result); err != nil {
		return err
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE user_id=?`, id)
	return nil
}

func (s *Store) TouchUserLogin(ctx context.Context, id string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE users SET last_login_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), id)
}

func (s *Store) CreateAuthSession(ctx context.Context, userID, tokenHash, csrf string, expires time.Time, ip, userAgent string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO auth_sessions(id,user_id,token_hash,csrf_token,created_at,expires_at,last_seen_at,ip,user_agent) VALUES(?,?,?,?,?,?,?,?,?)`,
		uuid.NewString(), userID, tokenHash, csrf, now.Format(time.RFC3339Nano), expires.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), ip, userAgent)
	return err
}

func (s *Store) AuthSessionByToken(ctx context.Context, tokenHash string) (AuthSession, error) {
	var session AuthSession
	var expiresAt string
	row := s.db.QueryRowContext(ctx, `SELECT s.id,s.user_id,s.csrf_token,s.expires_at,u.username,u.display_name,u.role,u.status
		FROM auth_sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=?`, tokenHash)
	if err := row.Scan(&session.ID, &session.UserID, &session.CSRF, &expiresAt, &session.Username, &session.DisplayName, &session.Role, &session.Status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AuthSession{}, ErrSessionInvalid
		}
		return AuthSession{}, err
	}
	session.ExpiresAt = parseStoreTime(expiresAt)
	if session.Status != "active" || !session.ExpiresAt.After(time.Now().UTC()) {
		return AuthSession{}, ErrSessionInvalid
	}
	return session, nil
}

func (s *Store) TouchAuthSession(ctx context.Context, id string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE auth_sessions SET last_seen_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), id)
}

func (s *Store) DeleteAuthSession(ctx context.Context, tokenHash string) {
	_, _ = s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE token_hash=?`, tokenHash)
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID string) {
	_, _ = s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE user_id=?`, userID)
}

func (s *Store) DeleteExpiredAuthSessions(ctx context.Context) {
	_, _ = s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE expires_at < ?`, time.Now().UTC().Format(time.RFC3339Nano))
}

func (s *Store) AssetGrantFor(ctx context.Context, userID, assetID string) (AssetGrant, error) {
	var grant AssetGrant
	var canEdit, canConnect int
	err := s.db.QueryRowContext(ctx, `SELECT user_id,asset_id,can_edit,can_connect FROM asset_grants WHERE user_id=? AND asset_id=?`, userID, assetID).
		Scan(&grant.UserID, &grant.AssetID, &canEdit, &canConnect)
	if errors.Is(err, sql.ErrNoRows) {
		return AssetGrant{}, ErrNoGrant
	}
	if err != nil {
		return AssetGrant{}, err
	}
	grant.CanEdit = canEdit == 1
	grant.CanConnect = canConnect == 1
	return grant, nil
}

func (s *Store) ListAssetGrantsForUser(ctx context.Context, userID string) ([]AssetGrant, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id,asset_id,can_edit,can_connect FROM asset_grants WHERE user_id=? ORDER BY asset_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grants := []AssetGrant{}
	for rows.Next() {
		var grant AssetGrant
		var canEdit, canConnect int
		if err := rows.Scan(&grant.UserID, &grant.AssetID, &canEdit, &canConnect); err != nil {
			return nil, err
		}
		grant.CanEdit, grant.CanConnect = canEdit == 1, canConnect == 1
		grants = append(grants, grant)
	}
	return grants, rows.Err()
}

func (s *Store) ListAssetGrantsForAsset(ctx context.Context, assetID string) ([]AssetGrant, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT g.user_id,g.asset_id,g.can_edit,g.can_connect,u.username,u.display_name
		FROM asset_grants g JOIN users u ON u.id=g.user_id WHERE g.asset_id=? ORDER BY u.username`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grants := []AssetGrant{}
	for rows.Next() {
		var grant AssetGrant
		var canEdit, canConnect int
		if err := rows.Scan(&grant.UserID, &grant.AssetID, &canEdit, &canConnect, &grant.Username, &grant.DisplayName); err != nil {
			return nil, err
		}
		grant.CanEdit, grant.CanConnect = canEdit == 1, canConnect == 1
		grants = append(grants, grant)
	}
	return grants, rows.Err()
}

func (s *Store) SetAssetGrant(ctx context.Context, userID, assetID string, canEdit, canConnect bool) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO asset_grants(user_id,asset_id,can_edit,can_connect) VALUES(?,?,?,?)
		ON CONFLICT(user_id,asset_id) DO UPDATE SET can_edit=excluded.can_edit,can_connect=excluded.can_connect`,
		userID, assetID, boolInt(canEdit), boolInt(canConnect))
	return err
}

func (s *Store) DeleteAssetGrant(ctx context.Context, userID, assetID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM asset_grants WHERE user_id=? AND asset_id=?`, userID, assetID)
	return err
}

func (s *Store) ReplaceAssetGrants(ctx context.Context, assetID string, grants []AssetGrantInput) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM asset_grants WHERE asset_id=?`, assetID); err != nil {
		return err
	}
	for _, grant := range grants {
		if !grant.CanEdit && !grant.CanConnect {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO asset_grants(user_id,asset_id,can_edit,can_connect) VALUES(?,?,?,?)`,
			grant.UserID, assetID, boolInt(grant.CanEdit), boolInt(grant.CanConnect)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func requireAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("not found: %w", sql.ErrNoRows)
	}
	return nil
}

func (s *Store) CreateRDPTicket(ctx context.Context, ticketHash, userID, assetID, options string, expires time.Time) error {
	now := time.Now().UTC()
	_, _ = s.db.ExecContext(ctx, `DELETE FROM rdp_tickets WHERE expires_at < ?`, now.Format(time.RFC3339Nano))
	_, err := s.db.ExecContext(ctx, `INSERT INTO rdp_tickets(ticket_hash,user_id,asset_id,options_json,created_at,expires_at) VALUES(?,?,?,?,?,?)`,
		ticketHash, userID, assetID, options, now.Format(time.RFC3339Nano), expires.UTC().Format(time.RFC3339Nano))
	return err
}

// ConsumeRDPTicket atomically marks a ticket as used. It deliberately returns a
// generic error for expired, used and unknown tickets so a caller cannot probe
// which tickets once existed.
func (s *Store) ConsumeRDPTicket(ctx context.Context, ticketHash string) (RDPTicket, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RDPTicket{}, err
	}
	defer tx.Rollback()
	var ticket RDPTicket
	var expiresAt string
	err = tx.QueryRowContext(ctx, `SELECT user_id,asset_id,options_json,expires_at FROM rdp_tickets WHERE ticket_hash=? AND used_at IS NULL`, ticketHash).
		Scan(&ticket.UserID, &ticket.AssetID, &ticket.Options, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RDPTicket{}, ErrTicketInvalid
	}
	if err != nil {
		return RDPTicket{}, err
	}
	ticket.ExpiresAt = parseStoreTime(expiresAt)
	if !ticket.ExpiresAt.After(time.Now().UTC()) {
		return RDPTicket{}, ErrTicketInvalid
	}
	result, err := tx.ExecContext(ctx, `UPDATE rdp_tickets SET used_at=? WHERE ticket_hash=? AND used_at IS NULL`, time.Now().UTC().Format(time.RFC3339Nano), ticketHash)
	if err != nil {
		return RDPTicket{}, err
	}
	if err := requireAffected(result); err != nil {
		return RDPTicket{}, ErrTicketInvalid
	}
	if err := tx.Commit(); err != nil {
		return RDPTicket{}, err
	}
	return ticket, nil
}

func (s *Store) ListAuditEvents(ctx context.Context, limit int) ([]AuditEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,occurred_at,actor_user_id,action,object_type,object_id,result,metadata_json
		FROM audit_events ORDER BY occurred_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []AuditEvent{}
	for rows.Next() {
		var event AuditEvent
		var occurredAt, metadata string
		if err := rows.Scan(&event.ID, &occurredAt, &event.ActorUserID, &event.Action, &event.ObjectType, &event.ObjectID, &event.Result, &metadata); err != nil {
			return nil, err
		}
		event.OccurredAt = parseStoreTime(occurredAt)
		if json.Valid([]byte(metadata)) {
			event.Metadata = json.RawMessage(metadata)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

type actorContextKey struct{}

// WithActor records who performed an audited action. Handlers set it in the
// request context after session authentication; internal calls may omit it.
func WithActor(ctx context.Context, userID string) context.Context {
	if userID == "" {
		return ctx
	}
	return context.WithValue(ctx, actorContextKey{}, userID)
}

func ActorFrom(ctx context.Context) string {
	value, _ := ctx.Value(actorContextKey{}).(string)
	return value
}
