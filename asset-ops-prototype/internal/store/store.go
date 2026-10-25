package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
	"qiyun-asset-ops/internal/secretbox"
	"qiyun-asset-ops/internal/sshauth"
)

type Asset struct {
	ID                 string    `json:"id"`
	AssetCode          string    `json:"asset_code"`
	Name               string    `json:"name"`
	Address            string    `json:"address"`
	SSHPort            int       `json:"ssh_port"`
	RDPPort            int       `json:"rdp_port"`
	OSFamily           string    `json:"os_family"`
	OSName             string    `json:"os_name"`
	Owner              string    `json:"owner"`
	Department         string    `json:"department"`
	Environment        string    `json:"environment"`
	Status             string    `json:"status"`
	CredentialID       string    `json:"credential_id,omitempty"`
	Username           string    `json:"username,omitempty"`
	AuthType           string    `json:"auth_type,omitempty"`
	HostKey            string    `json:"-"`
	HostKeyFingerprint string    `json:"host_key_fingerprint,omitempty"`
	HostKeyStatus      string    `json:"host_key_status"`
	AssetType          string    `json:"asset_type"`
	SerialNumber       string    `json:"serial_number"`
	MACAddress         string    `json:"mac_address"`
	Location           string    `json:"location"`
	PriceCents         int64     `json:"price_cents"`
	PurchasedAt        string    `json:"purchased_at"`
	WarrantyUntil      string    `json:"warranty_until"`
	Supplier           string    `json:"supplier"`
	PurchaseOrder      string    `json:"purchase_order"`
	Version            int64     `json:"version"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type CreateAsset struct {
	AssetCode   string `json:"asset_code"`
	Name        string `json:"name"`
	Address     string `json:"address"`
	SSHPort     int    `json:"ssh_port"`
	RDPPort     int    `json:"rdp_port"`
	OSName      string `json:"os_name"`
	Owner       string `json:"owner"`
	Department  string `json:"department"`
	Environment string `json:"environment"`
	Username    string `json:"username"`
	AuthType    string `json:"auth_type"`
	Secret      string `json:"secret"`
	Passphrase  string `json:"passphrase"`
	// Ledger fields are optional IT-asset attributes. Empty means "not recorded"
	// and the UI shows 待补充 rather than inventing a value.
	AssetType     string `json:"asset_type"`
	SerialNumber  string `json:"serial_number"`
	MACAddress    string `json:"mac_address"`
	Location      string `json:"location"`
	PriceCents    int64  `json:"price_cents"`
	PurchasedAt   string `json:"purchased_at"`
	WarrantyUntil string `json:"warranty_until"`
	Supplier      string `json:"supplier"`
	PurchaseOrder string `json:"purchase_order"`
}

// AssetPatch carries only the fields present in the request, so a PATCH cannot
// silently blank out attributes the caller did not send.
type AssetPatch struct {
	Name          *string `json:"name"`
	Address       *string `json:"address"`
	SSHPort       *int    `json:"ssh_port"`
	RDPPort       *int    `json:"rdp_port"`
	OSName        *string `json:"os_name"`
	Owner         *string `json:"owner"`
	Department    *string `json:"department"`
	Environment   *string `json:"environment"`
	AssetType     *string `json:"asset_type"`
	SerialNumber  *string `json:"serial_number"`
	MACAddress    *string `json:"mac_address"`
	Location      *string `json:"location"`
	PriceCents    *int64  `json:"price_cents"`
	PurchasedAt   *string `json:"purchased_at"`
	WarrantyUntil *string `json:"warranty_until"`
	Supplier      *string `json:"supplier"`
	PurchaseOrder *string `json:"purchase_order"`
}

type Credential struct{ ID, Username, AuthType, Secret, Passphrase string }

type Store struct {
	db  *sql.DB
	box *secretbox.Box
}

func Open(path string, box *secretbox.Box) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, box: box}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// assetsDDL deliberately has no inline UNIQUE on asset_code: deleted assets are
// only soft-deleted, so the uniqueness rule has to apply to active rows only
// (see idx_assets_code_active). With the old table-level UNIQUE, re-registering a
// deleted asset code failed with "资产编码已经存在" even though the list was empty.
const assetsDDL = `CREATE TABLE IF NOT EXISTS assets (
 id TEXT PRIMARY KEY, asset_code TEXT NOT NULL, name TEXT NOT NULL,
 address TEXT NOT NULL, ssh_port INTEGER NOT NULL CHECK(ssh_port BETWEEN 1 AND 65535),
 rdp_port INTEGER NOT NULL DEFAULT 3389 CHECK(rdp_port BETWEEN 1 AND 65535),
 os_family TEXT NOT NULL DEFAULT 'linux', os_name TEXT NOT NULL DEFAULT '', owner TEXT NOT NULL DEFAULT '',
 department TEXT NOT NULL DEFAULT '', environment TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'unknown',
 credential_id TEXT NOT NULL REFERENCES credentials(id), host_key TEXT NOT NULL DEFAULT '',
 host_key_fingerprint TEXT NOT NULL DEFAULT '', version INTEGER NOT NULL DEFAULT 1,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL, deleted_at TEXT,
 asset_type TEXT NOT NULL DEFAULT 'linux_host', serial_number TEXT NOT NULL DEFAULT '',
 mac_address TEXT NOT NULL DEFAULT '', location TEXT NOT NULL DEFAULT '', price_cents INTEGER NOT NULL DEFAULT 0,
 purchased_at TEXT NOT NULL DEFAULT '', warranty_until TEXT NOT NULL DEFAULT '',
 supplier TEXT NOT NULL DEFAULT '', purchase_order TEXT NOT NULL DEFAULT ''
)`

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS credentials (
 id TEXT PRIMARY KEY, username TEXT NOT NULL, auth_type TEXT NOT NULL,
 nonce BLOB NOT NULL, ciphertext BLOB NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);`+assetsDDL+`;
CREATE INDEX IF NOT EXISTS idx_assets_group ON assets(department, environment, status);
CREATE TABLE IF NOT EXISTS terminal_sessions (
 id TEXT PRIMARY KEY, asset_id TEXT NOT NULL, target_address TEXT NOT NULL, host_key_fingerprint TEXT NOT NULL,
 status TEXT NOT NULL, bytes_in INTEGER NOT NULL DEFAULT 0, bytes_out INTEGER NOT NULL DEFAULT 0,
 started_at TEXT NOT NULL, ended_at TEXT, close_reason TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS audit_events (
 id TEXT PRIMARY KEY, occurred_at TEXT NOT NULL, action TEXT NOT NULL, object_type TEXT NOT NULL,
 object_id TEXT NOT NULL, result TEXT NOT NULL, metadata_json TEXT NOT NULL DEFAULT '{}'
);`)
	if err != nil {
		return err
	}
	if err := s.ensureAgentSchema(ctx); err != nil {
		return err
	}
	// Databases created by the first release carry an inline UNIQUE constraint that
	// cannot be dropped in place; rebuild the table once, copying existing rows.
	if err := s.rebuildAssetsIfFullUnique(ctx); err != nil {
		return err
	}
	// Ledger columns were added later; ALTER keeps older databases working.
	ledgerColumns := []struct{ name, ddl string }{
		{"rdp_port", "INTEGER NOT NULL DEFAULT 3389"},
		{"asset_type", "TEXT NOT NULL DEFAULT 'linux_host'"},
		{"serial_number", "TEXT NOT NULL DEFAULT ''"},
		{"mac_address", "TEXT NOT NULL DEFAULT ''"},
		{"location", "TEXT NOT NULL DEFAULT ''"},
		{"price_cents", "INTEGER NOT NULL DEFAULT 0"},
		{"purchased_at", "TEXT NOT NULL DEFAULT ''"},
		{"warranty_until", "TEXT NOT NULL DEFAULT ''"},
		{"supplier", "TEXT NOT NULL DEFAULT ''"},
		{"purchase_order", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, column := range ledgerColumns {
		_, err := s.db.ExecContext(ctx, "ALTER TABLE assets ADD COLUMN "+column.name+" "+column.ddl)
		if err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return err
		}
	}
	// Only active assets must have a unique code, so a deleted code can be reused.
	_, err = s.db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_assets_code_active ON assets(asset_code) WHERE deleted_at IS NULL`)
	if err != nil {
		return err
	}
	return s.migrateAuth(ctx)
}

func (s *Store) rebuildAssetsIfFullUnique(ctx context.Context) error {
	var ddl sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='assets'`).Scan(&ddl)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToUpper(ddl.String), "UNIQUE") {
		return nil
	}
	existing := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(assets)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid, notNull, pk int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		existing[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	all := []string{"id", "asset_code", "name", "address", "ssh_port", "rdp_port", "os_family", "os_name", "owner", "department", "environment", "status", "credential_id", "host_key", "host_key_fingerprint", "version", "created_at", "updated_at", "deleted_at", "asset_type", "serial_number", "mac_address", "location", "price_cents", "purchased_at", "warranty_until", "supplier", "purchase_order"}
	copied := make([]string, 0, len(all))
	for _, column := range all {
		if existing[column] {
			copied = append(copied, column)
		}
	}
	if len(copied) == 0 {
		return errors.New("assets table has no recognisable columns; refusing to rebuild")
	}
	list := strings.Join(copied, ",")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`ALTER TABLE assets RENAME TO assets_legacy`,
		assetsDDL,
		`INSERT INTO assets(` + list + `) SELECT ` + list + ` FROM assets_legacy`,
		`DROP TABLE assets_legacy`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("rebuild assets table: %w", err)
		}
	}
	slog.Info("assets table rebuilt to drop the table-level unique constraint", "columns", list)
	return tx.Commit()
}

// validateCredential checks and normalises the secret before it is encrypted.
// Doing it here means an unusable password or key is rejected at submit time,
// with a reason the operator can act on, instead of after the fingerprint has
// already been approved.
func validateCredential(in *CreateAsset) error {
	in.Username = strings.TrimSpace(in.Username)
	if in.Username == "" {
		return errors.New("请填写 SSH 用户名")
	}
	switch in.AuthType {
	case sshauth.KindPassword:
		if err := sshauth.ValidatePassword(in.Secret); err != nil {
			return err
		}
		in.Secret = strings.TrimSpace(in.Secret)
		in.Passphrase = ""
	case sshauth.KindPrivateKey:
		if _, err := sshauth.ParsePrivateKey(in.Secret, in.Passphrase); err != nil {
			return err
		}
		in.Secret = sshauth.Clean(in.Secret)
		in.Passphrase = strings.TrimSpace(in.Passphrase)
	default:
		return errors.New("auth_type 必须是 password 或 private_key")
	}
	return nil
}

func validateCreate(in *CreateAsset) error {
	in.AssetCode = strings.TrimSpace(in.AssetCode)
	in.Name = strings.TrimSpace(in.Name)
	in.Address = strings.TrimSpace(in.Address)
	in.Username = strings.TrimSpace(in.Username)
	in.Department = strings.TrimSpace(in.Department)
	in.Environment = strings.TrimSpace(in.Environment)
	if in.AssetCode == "" || in.Name == "" || in.Address == "" || in.Username == "" || in.Secret == "" {
		return errors.New("资产编码、名称、地址、SSH 用户名和凭据都是必填项")
	}
	if in.SSHPort == 0 {
		in.SSHPort = 22
	}
	if in.SSHPort < 1 || in.SSHPort > 65535 {
		return errors.New("SSH 端口必须在 1-65535 之间")
	}
	if in.RDPPort == 0 {
		in.RDPPort = 3389
	}
	if in.RDPPort < 1 || in.RDPPort > 65535 {
		return errors.New("RDP 端口必须在 1-65535 之间")
	}
	if IsWindowsOS(in.AssetType, in.OSName) && in.AuthType != sshauth.KindPassword {
		return errors.New("Windows RDP 资产只支持密码凭据；私钥不能用于远程桌面登录")
	}
	if err := validateCredential(in); err != nil {
		return err
	}
	if in.Environment == "" {
		in.Environment = "production"
	}
	return nil
}

// IsWindowsOS reports whether an asset type or OS name describes a Windows
// host. Windows assets are managed with RDP instead of SSH, so they must not be
// queued for SSH host-key scans or metrics collection.
func IsWindowsOS(assetType, osName string) bool {
	return strings.EqualFold(strings.TrimSpace(assetType), "windows_host") ||
		strings.Contains(strings.ToLower(osName), "windows")
}

// NormalizeOS derives the stored os_family/asset_type pair. A host whose OS
// name says Windows is always stored as windows_host, otherwise it would be
// collected over SSH and fail every interval.
func NormalizeOS(assetType, osName string) (family, kind string) {
	if IsWindowsOS(assetType, osName) {
		return "windows", "windows_host"
	}
	return "linux", defaultString(strings.TrimSpace(assetType), "linux_host")
}

// IsWindows reports whether RDP is this asset's management protocol.
func (a Asset) IsWindows() bool {
	return a.OSFamily == "windows" || IsWindowsOS(a.AssetType, a.OSName)
}

func (s *Store) CreateAsset(ctx context.Context, in CreateAsset) (Asset, error) {
	if err := validateCreate(&in); err != nil {
		return Asset{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Asset{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	assetID, credID := uuid.NewString(), uuid.NewString()
	payload, _ := json.Marshal(map[string]string{"secret": in.Secret, "passphrase": in.Passphrase})
	aad := "credential:v1:" + credID + ":" + in.AuthType + ":" + in.Username
	nonce, ciphertext, err := s.box.Encrypt(payload, aad)
	if err != nil {
		return Asset{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO credentials(id,username,auth_type,nonce,ciphertext,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, credID, in.Username, in.AuthType, nonce, ciphertext, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		return Asset{}, err
	}
	osFamily, assetType := NormalizeOS(in.AssetType, in.OSName)
	if _, err = tx.ExecContext(ctx, `INSERT INTO assets(id,asset_code,name,address,ssh_port,rdp_port,os_family,os_name,owner,department,environment,status,credential_id,created_at,updated_at,asset_type,serial_number,mac_address,location,price_cents,purchased_at,warranty_until,supplier,purchase_order) VALUES(?,?,?,?,?,?,?,?,?,?,?,'unknown',?,?,?,?,?,?,?,?,?,?,?,?)`, assetID, in.AssetCode, in.Name, in.Address, in.SSHPort, in.RDPPort, osFamily, in.OSName, in.Owner, in.Department, in.Environment, credID, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), assetType, in.SerialNumber, in.MACAddress, in.Location, in.PriceCents, in.PurchasedAt, in.WarrantyUntil, in.Supplier, in.PurchaseOrder); err != nil {
		return Asset{}, err
	}
	if err = insertAudit(ctx, tx, "asset.create", "asset", assetID, "success", map[string]any{"asset_code": in.AssetCode, "address": in.Address, "port": in.SSHPort}); err != nil {
		return Asset{}, err
	}
	if err = tx.Commit(); err != nil {
		return Asset{}, err
	}
	return s.GetAsset(ctx, assetID)
}

func scanAsset(rows interface{ Scan(...any) error }) (Asset, error) {
	var a Asset
	var created, updated string
	err := rows.Scan(&a.ID, &a.AssetCode, &a.Name, &a.Address, &a.SSHPort, &a.RDPPort, &a.OSFamily, &a.OSName, &a.Owner, &a.Department, &a.Environment, &a.Status, &a.CredentialID, &a.Username, &a.AuthType, &a.HostKey, &a.HostKeyFingerprint, &a.Version, &created, &updated, &a.AssetType, &a.SerialNumber, &a.MACAddress, &a.Location, &a.PriceCents, &a.PurchasedAt, &a.WarrantyUntil, &a.Supplier, &a.PurchaseOrder)
	if err != nil {
		return Asset{}, err
	}
	a.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	a.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	if a.HostKey == "" {
		a.HostKeyStatus = "pending"
	} else {
		a.HostKeyStatus = "verified"
	}
	return a, nil
}

const assetSelect = `SELECT a.id,a.asset_code,a.name,a.address,a.ssh_port,a.rdp_port,a.os_family,a.os_name,a.owner,a.department,a.environment,a.status,a.credential_id,c.username,c.auth_type,a.host_key,a.host_key_fingerprint,a.version,a.created_at,a.updated_at,a.asset_type,a.serial_number,a.mac_address,a.location,a.price_cents,a.purchased_at,a.warranty_until,a.supplier,a.purchase_order FROM assets a JOIN credentials c ON c.id=a.credential_id WHERE a.deleted_at IS NULL`

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (s *Store) ListAssets(ctx context.Context, query string) ([]Asset, error) {
	return s.SearchAssets(ctx, AssetFilter{Query: query})
}

// AssetFilter narrows the list endpoint; empty fields are ignored.
type AssetFilter struct {
	Query       string
	Department  string
	Environment string
	HostKey     string
	Status      string
}

func (s *Store) SearchAssets(ctx context.Context, filter AssetFilter) ([]Asset, error) {
	args := []any{}
	sqlText := assetSelect
	if q := strings.TrimSpace(filter.Query); q != "" {
		sqlText += ` AND (a.name LIKE ? OR a.asset_code LIKE ? OR a.address LIKE ? OR a.owner LIKE ? OR a.department LIKE ? OR a.environment LIKE ? OR a.serial_number LIKE ?)`
		match := "%" + q + "%"
		args = append(args, match, match, match, match, match, match, match)
	}
	if v := strings.TrimSpace(filter.Department); v != "" {
		sqlText += ` AND a.department = ?`
		args = append(args, v)
	}
	if v := strings.TrimSpace(filter.Environment); v != "" {
		sqlText += ` AND a.environment = ?`
		args = append(args, v)
	}
	if v := strings.TrimSpace(filter.Status); v != "" {
		sqlText += ` AND a.status = ?`
		args = append(args, v)
	}
	switch strings.TrimSpace(filter.HostKey) {
	case "pending":
		sqlText += ` AND a.host_key = ''`
	case "verified":
		sqlText += ` AND a.host_key <> ''`
	}
	sqlText += ` ORDER BY a.department,a.environment,a.name`
	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Asset{}
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetAsset(ctx context.Context, id string) (Asset, error) {
	return scanAsset(s.db.QueryRowContext(ctx, assetSelect+` AND a.id=?`, id))
}

func (s *Store) ApproveHostKey(ctx context.Context, id, key, fingerprint string) error {
	if key == "" || fingerprint == "" {
		return errors.New("host key and fingerprint are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE assets SET host_key=?,host_key_fingerprint=?,version=version+1,updated_at=? WHERE id=? AND deleted_at IS NULL`, key, fingerprint, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return sql.ErrNoRows
	}
	if err = insertAudit(ctx, tx, "asset.host_key.approve", "asset", id, "success", map[string]any{"fingerprint": fingerprint}); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateAsset applies a partial update. Changing the address or SSH port
// invalidates the approved host key, because the fingerprint now belongs to a
// different endpoint; the operator must scan and approve again before connecting.
func (s *Store) UpdateAsset(ctx context.Context, id string, patch AssetPatch) (Asset, error) {
	current, err := s.GetAsset(ctx, id)
	if err != nil {
		return Asset{}, err
	}
	changed := map[string]any{}
	assignments := []string{}
	args := []any{}
	set := func(column string, value any, label string) {
		assignments = append(assignments, column+"=?")
		args = append(args, value)
		changed[label] = value
	}
	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name == "" {
			return Asset{}, errors.New("资产名称不能为空")
		}
		set("name", name, "name")
	}
	resetHostKey := false
	if patch.Address != nil {
		address := strings.TrimSpace(*patch.Address)
		if address == "" {
			return Asset{}, errors.New("地址不能为空")
		}
		if address != current.Address {
			resetHostKey = true
		}
		set("address", address, "address")
	}
	if patch.SSHPort != nil {
		port := *patch.SSHPort
		if port < 1 || port > 65535 {
			return Asset{}, errors.New("SSH 端口必须在 1-65535 之间")
		}
		if port != current.SSHPort {
			resetHostKey = true
		}
		set("ssh_port", port, "ssh_port")
	}
	if patch.RDPPort != nil {
		port := *patch.RDPPort
		if port < 1 || port > 65535 {
			return Asset{}, errors.New("RDP 端口必须在 1-65535 之间")
		}
		set("rdp_port", port, "rdp_port")
	}
	if patch.OSName != nil {
		set("os_name", strings.TrimSpace(*patch.OSName), "os_name")
	}
	if patch.Owner != nil {
		set("owner", strings.TrimSpace(*patch.Owner), "owner")
	}
	if patch.Department != nil {
		set("department", strings.TrimSpace(*patch.Department), "department")
	}
	if patch.Environment != nil {
		set("environment", strings.TrimSpace(*patch.Environment), "environment")
	}
	if patch.AssetType != nil {
		kind := defaultString(strings.TrimSpace(*patch.AssetType), "linux_host")
		family, normalized := NormalizeOS(kind, current.OSName)
		set("asset_type", normalized, "asset_type")
		if family != current.OSFamily {
			set("os_family", family, "os_family")
		}
	}
	if patch.SerialNumber != nil {
		set("serial_number", strings.TrimSpace(*patch.SerialNumber), "serial_number")
	}
	if patch.MACAddress != nil {
		set("mac_address", strings.TrimSpace(*patch.MACAddress), "mac_address")
	}
	if patch.Location != nil {
		set("location", strings.TrimSpace(*patch.Location), "location")
	}
	if patch.PriceCents != nil {
		if *patch.PriceCents < 0 {
			return Asset{}, errors.New("价格不能为负数")
		}
		set("price_cents", *patch.PriceCents, "price_cents")
	}
	if patch.PurchasedAt != nil {
		set("purchased_at", strings.TrimSpace(*patch.PurchasedAt), "purchased_at")
	}
	if patch.WarrantyUntil != nil {
		set("warranty_until", strings.TrimSpace(*patch.WarrantyUntil), "warranty_until")
	}
	if patch.Supplier != nil {
		set("supplier", strings.TrimSpace(*patch.Supplier), "supplier")
	}
	if patch.PurchaseOrder != nil {
		set("purchase_order", strings.TrimSpace(*patch.PurchaseOrder), "purchase_order")
	}
	if len(assignments) == 0 {
		return Asset{}, errors.New("没有需要更新的字段")
	}
	if resetHostKey {
		assignments = append(assignments, "host_key=''", "host_key_fingerprint=''")
		changed["host_key_status"] = "pending"
	}
	assignments = append(assignments, "version=version+1", "updated_at=?")
	args = append(args, time.Now().UTC().Format(time.RFC3339Nano), id)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Asset{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE assets SET `+strings.Join(assignments, ",")+` WHERE id=? AND deleted_at IS NULL`, args...)
	if err != nil {
		return Asset{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return Asset{}, sql.ErrNoRows
	}
	if err = insertAudit(ctx, tx, "asset.update", "asset", id, "success", changed); err != nil {
		return Asset{}, err
	}
	if err = tx.Commit(); err != nil {
		return Asset{}, err
	}
	return s.GetAsset(ctx, id)
}

func (s *Store) DeleteAsset(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var cred string
	if err = tx.QueryRowContext(ctx, `SELECT credential_id FROM assets WHERE id=? AND deleted_at IS NULL`, id).Scan(&cred); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE assets SET deleted_at=?,version=version+1 WHERE id=?`, now, id); err != nil {
		return err
	}
	if err = insertAudit(ctx, tx, "asset.delete", "asset", id, "success", map[string]any{"credential_id": cred}); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateCredential replaces the secret for an existing asset without touching the
// approved host key, so a wrong password or key can be corrected in place instead
// of deleting the asset and re-verifying its fingerprint.
func (s *Store) UpdateCredential(ctx context.Context, assetID string, in CreateAsset) (Asset, error) {
	existing, err := s.GetAsset(ctx, assetID)
	if err != nil {
		return Asset{}, err
	}
	if in.Username == "" {
		in.Username = existing.Username
	}
	if in.AuthType == "" {
		in.AuthType = existing.AuthType
	}
	if err := validateCredential(&in); err != nil {
		return Asset{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Asset{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	payload, _ := json.Marshal(map[string]string{"secret": in.Secret, "passphrase": in.Passphrase})
	aad := "credential:v1:" + existing.CredentialID + ":" + in.AuthType + ":" + in.Username
	nonce, ciphertext, err := s.box.Encrypt(payload, aad)
	if err != nil {
		return Asset{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE credentials SET username=?,auth_type=?,nonce=?,ciphertext=?,updated_at=? WHERE id=?`, in.Username, in.AuthType, nonce, ciphertext, now, existing.CredentialID); err != nil {
		return Asset{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE assets SET version=version+1,updated_at=? WHERE id=? AND deleted_at IS NULL`, now, assetID); err != nil {
		return Asset{}, err
	}
	if err = insertAudit(ctx, tx, "credential.update", "asset", assetID, "success", map[string]any{"auth_type": in.AuthType, "username": in.Username}); err != nil {
		return Asset{}, err
	}
	if err = tx.Commit(); err != nil {
		return Asset{}, err
	}
	return s.GetAsset(ctx, assetID)
}

func (s *Store) CredentialForAsset(ctx context.Context, id string) (Asset, Credential, error) {
	a, err := s.GetAsset(ctx, id)
	if err != nil {
		return Asset{}, Credential{}, err
	}
	var nonce, ciphertext []byte
	var c Credential
	c.ID = a.CredentialID
	c.Username = a.Username
	c.AuthType = a.AuthType
	if err = s.db.QueryRowContext(ctx, `SELECT nonce,ciphertext FROM credentials WHERE id=?`, c.ID).Scan(&nonce, &ciphertext); err != nil {
		return Asset{}, Credential{}, err
	}
	aad := "credential:v1:" + c.ID + ":" + c.AuthType + ":" + c.Username
	plain, err := s.box.Decrypt(nonce, ciphertext, aad)
	if err != nil {
		return Asset{}, Credential{}, err
	}
	var payload map[string]string
	if err = json.Unmarshal(plain, &payload); err != nil {
		return Asset{}, Credential{}, errors.New("invalid credential payload")
	}
	c.Secret = payload["secret"]
	c.Passphrase = payload["passphrase"]
	return a, c, nil
}

func (s *Store) StartSession(ctx context.Context, id, assetID, target, fingerprint string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `INSERT INTO terminal_sessions(id,asset_id,target_address,host_key_fingerprint,status,started_at) VALUES(?,?,?,?,?,?)`, id, assetID, target, fingerprint, "connecting", now)
	return err
}
func (s *Store) UpdateSession(ctx context.Context, id, status, reason string, in, out int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE terminal_sessions SET status=?,close_reason=?,bytes_in=?,bytes_out=?,ended_at=CASE WHEN ? IN ('closed','failed') THEN ? ELSE ended_at END WHERE id=?`, status, reason, in, out, status, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

func insertAudit(ctx context.Context, tx *sql.Tx, action, objectType, objectID, result string, metadata any) error {
	raw, _ := json.Marshal(metadata)
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_events(id,occurred_at,action,object_type,object_id,result,metadata_json,actor_user_id) VALUES(?,?,?,?,?,?,?,?)`,
		uuid.NewString(), time.Now().UTC().Format(time.RFC3339Nano), action, objectType, objectID, result, string(raw), ActorFrom(ctx))
	return err
}
func (s *Store) Audit(ctx context.Context, action, objectType, objectID, result string, metadata any) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	if insertAudit(ctx, tx, action, objectType, objectID, result, metadata) == nil {
		_ = tx.Commit()
	}
}

func IsUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}
func IsNotFound(err error) bool {
	return err != nil && (errors.Is(err, sql.ErrNoRows) || strings.Contains(strings.ToLower(err.Error()), "no rows"))
}
func WrapNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("not found: %w", err)
	}
	return err
}
