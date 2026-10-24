package main

// Read-only diagnostic helper: prints recent sessions and audit events so an
// operator can see why a terminal did not open. Run with:
//   go run ./tools/dbinspect -db data/qiyun.db

import (
	"database/sql"
	"flag"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	path := flag.String("db", "data/qiyun.db", "sqlite database path")
	limit := flag.Int("limit", 20, "how many rows to print")
	flag.Parse()
	// A pure read-only connection cannot read a WAL database while the service
	// holds it, so open normally and only run SELECTs.
	db, err := sql.Open("sqlite", *path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer db.Close()
	fmt.Println("== assets ==")
	rows, err := db.Query(`SELECT a.asset_code,a.name,a.address,a.ssh_port,COALESCE(c.username,''),COALESCE(c.auth_type,''),a.host_key,a.deleted_at FROM assets a LEFT JOIN credentials c ON c.id=a.credential_id ORDER BY a.created_at DESC LIMIT ?`, *limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for rows.Next() {
		var code, name, addr, user, auth, hostKey string
		var port int
		var deleted sql.NullString
		if err := rows.Scan(&code, &name, &addr, &port, &user, &auth, &hostKey, &deleted); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		state := "active"
		if deleted.Valid {
			state = "deleted@" + deleted.String
		}
		keyState := "pending"
		if hostKey != "" {
			keyState = "verified"
		}
		fmt.Printf("  %-16s %-20s %s:%d user=%s auth=%s hostkey=%s %s\n", code, name, addr, port, user, auth, keyState, state)
	}
	rows.Close()

	fmt.Println("== terminal_sessions ==")
	sessions, err := db.Query(`SELECT id,asset_id,status,bytes_in,bytes_out,started_at,COALESCE(ended_at,''),close_reason FROM terminal_sessions ORDER BY started_at DESC LIMIT ?`, *limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for sessions.Next() {
		var id, asset, status, started, ended, reason string
		var in, out int64
		if err := sessions.Scan(&id, &asset, &status, &in, &out, &started, &ended, &reason); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("  %s %s status=%s in=%d out=%d started=%s ended=%s reason=%s\n", id[:8], asset[:8], status, in, out, started, ended, reason)
	}
	sessions.Close()

	fmt.Println("== audit_events (recent) ==")
	audits, err := db.Query(`SELECT occurred_at,action,object_id,result,metadata_json FROM audit_events ORDER BY occurred_at DESC LIMIT ?`, *limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for audits.Next() {
		var at, action, object, result, meta string
		if err := audits.Scan(&at, &action, &object, &result, &meta); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if len(meta) > 200 {
			meta = meta[:200] + "…"
		}
		fmt.Printf("  %s %-24s %s %s %s\n", at, action, object, result, meta)
	}
	audits.Close()
}
