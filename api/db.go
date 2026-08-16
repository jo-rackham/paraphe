package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Advisory locks: two distinct keys, so an instance that only needs the
// session secret does not wait behind the import of 34,826 rows.
const (
	ImportLock = 8047
	SecretLock = 8048
)

const lockWait = 45 * time.Second

// OpenDatabase builds the pool. The keepalives are explicit: without them,
// a partitioned instance holds an advisory lock until the OS TCP timeout
// (2 h by default) and blocks every other instance from starting.
func OpenDatabase(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("unreadable PARAPHE_DATABASE_URL: %w", err)
	}
	cfg.ConnConfig.DialFunc = (&net.Dialer{
		Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connecting to PostgreSQL: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("PostgreSQL unreachable: %w", err)
	}
	return pool, nil
}

// takeLock takes an advisory lock on the given connection, or fails SAYING
// SO. Unbounded, an instance frozen while holding the lock would send the
// others into CrashLoopBackOff with empty logs.
func takeLock(ctx context.Context, c *pgxpool.Conn, key int) error {
	if _, err := c.Exec(ctx, fmt.Sprintf("SET lock_timeout = '%ds'",
		int(lockWait.Seconds()))); err != nil {
		return fmt.Errorf("setting lock_timeout: %w", err)
	}
	// …and put back, whichever way this ends. SET is a SESSION setting and
	// pgx hands the connection back to the pool as it found it — so without
	// this, every request served by that connection afterwards inherits a
	// forty-five second cap on every lock it waits for, and an operator
	// reading a timeout has nothing to trace it to. The cap is wanted for
	// the one statement below and nothing else.
	defer func() {
		if _, err := c.Exec(ctx, "RESET lock_timeout"); err != nil {
			slog.Warn("lock_timeout not reset: the connection carries it back "+
				"to the pool", "error", err)
		}
	}()
	if _, err := c.Exec(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		var pge *pgconn.PgError
		if errors.As(err, &pge) && pge.Code == "55P03" {
			return fmt.Errorf("lock %d unavailable after %s: another instance "+
				"holds it (import in progress, or frozen instance). "+
				"Check: SELECT * FROM pg_locks WHERE locktype='advisory';",
				key, lockWait)
		}
		return fmt.Errorf("taking lock %d: %w", key, err)
	}
	return nil
}

func releaseLock(ctx context.Context, c *pgxpool.Conn, key int) {
	if _, err := c.Exec(ctx, "SELECT pg_advisory_unlock($1)", key); err != nil {
		slog.Warn("lock not released (it will drop at disconnect)",
			"lock", key, "error", err)
	}
}

// minSecretKeyBytes: the floor a supplied session key must clear. The one
// this file draws is 64 bytes and DEPLOYMENT.md asks operators for the same;
// 32 is where a refusal stops being arguable, and it is what
// `openssl rand -hex 16` — the shortest thing anybody pastes — already
// exceeds.
const minSecretKeyBytes = 32

// SessionSecret: the cookie signing key. Never derived from a human
// password — a captured cookie would allow breaking it offline. Either
// provided by the environment, or drawn at random once and kept, so
// sessions survive restarts.
func SessionSecret(ctx context.Context, pool *pgxpool.Pool) ([]byte, error) {
	provided, err := UsableSecret(Get("secret_key"), "PARAPHE_SECRET_KEY")
	if err != nil {
		return nil, err
	}
	if provided != "" {
		// A LENGTH floor, because the whole post-quantum argument below
		// rests on the key and nothing else checked it. `PARAPHE_SECRET_KEY=x`
		// started cleanly and signed every session with one byte: one
		// captured cookie, an offline search of minutes, and the attacker
		// mints a session for any account — verify() never reads the
		// database. Refusing the five example values was not enough.
		if len(provided) < minSecretKeyBytes {
			return nil, fmt.Errorf("PARAPHE_SECRET_KEY is %d bytes long, and "+
				"signs every session: %d at the very least, 64 random ones "+
				"expected. Generate one: openssl rand -hex 64",
				len(provided), minSecretKeyBytes)
		}
		return []byte(provided), nil
	}

	c, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquiring a connection: %w", err)
	}
	defer c.Release()
	// CREATE TABLE IF NOT EXISTS is NOT atomic in PostgreSQL: three
	// instances starting together on a pristine database kill each other on
	// pg_type_typname_nsp_index.
	if err := takeLock(ctx, c, SecretLock); err != nil {
		return nil, err
	}
	defer releaseLock(ctx, c, SecretLock)

	if _, err := c.Exec(ctx, "CREATE TABLE IF NOT EXISTS settings("+
		"key TEXT PRIMARY KEY, value TEXT)"); err != nil {
		return nil, fmt.Errorf("creating the settings table: %w", err)
	}
	// 64 bytes, not 32: the post-quantum margin of an HMAC is bounded by the
	// KEY, not by the digest. Grover searches a k-bit key in 2^(k/2), so 512
	// bits of key keep ~256 where 256 bits keep ~128. Both are ample; this
	// one costs 32 bytes.
	raw := make([]byte, 64)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("drawing the session secret: %w", err)
	}
	// several instances start at the same time: ON CONFLICT DO NOTHING
	// guarantees they all share the same secret, whatever the order
	if _, err := c.Exec(ctx, "INSERT INTO settings VALUES('secret_session', $1) "+
		"ON CONFLICT (key) DO NOTHING", hex.EncodeToString(raw)); err != nil {
		return nil, fmt.Errorf("writing the session secret: %w", err)
	}
	var value string
	if err := c.QueryRow(ctx,
		"SELECT value FROM settings WHERE key='secret_session'").Scan(&value); err != nil {
		return nil, fmt.Errorf("re-reading the session secret: %w", err)
	}
	return []byte(value), nil
}

// InitDatabase creates the schema, imports the list and bootstraps
// coordination. Idempotent, and protected by an advisory lock: all three
// instances start at the same time, exactly one must import.
//
// The whole transaction runs in the MAINTENANCE scope: the import traverses
// the organisations, which no HTTP request can do.
func InitDatabase(ctx context.Context, pool *pgxpool.Pool, csvPath string,
	cfg *Config, bootstrapSlug string) error {
	c, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquiring a connection: %w", err)
	}
	defer c.Release()

	slog.Info("import: waiting for the lock")
	// outside the transaction: otherwise the snapshot is frozen at lock
	// acquisition, hence BEFORE the wait, and the next instance works on a
	// stale view
	if err := takeLock(ctx, c, ImportLock); err != nil {
		return err
	}
	defer releaseLock(ctx, c, ImportLock)
	slog.Info("import: lock acquired")

	tx, err := c.Begin(ctx)
	if err != nil {
		return fmt.Errorf("opening a transaction: %w", err)
	}
	// rollback first on failure: on an aborted transaction the
	// pg_advisory_unlock would raise and mask the real cause
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	bootstrapOrg, err := schema(ctx, tx, cfg, bootstrapSlug)
	if err != nil {
		return err
	}
	if err := importList(ctx, tx, csvPath); err != nil {
		return err
	}
	if err := bootstrap(ctx, tx, bootstrapOrg); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing the import: %w", err)
	}
	return nil
}

// schema creates the tables and returns the bootstrap organisation's
// identifier (0 when the campaign configuration describes none — pristine
// multi-campaign instance).
func schema(ctx context.Context, tx pgx.Tx, cfg *Config, bootstrapSlug string) (int, error) {
	if err := orgSchema(ctx, tx); err != nil {
		return 0, err
	}
	bootstrapOrg := 0
	if cfg != nil && cfg.Complete {
		id, err := ensureOrg(ctx, tx, bootstrapSlug, cfg)
		if err != nil {
			return 0, err
		}
		bootstrapOrg = id
	}

	var columns []string
	for _, c := range Cols {
		if c != "insee_code" {
			columns = append(columns, c+" TEXT")
		}
	}
	statements := []string{
		// Declared here rather than as a side effect of drawing the session
		// secret: the import reads it to decide whether the list changed,
		// and a table that exists only because another code path ran first
		// is a table that is missing the day that path moves.
		`CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY, value TEXT)`,
		// `mayors` carries ONLY the public list: the work columns live in
		// `assignments`, the sole table owned by a campaign.
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS mayors(
			%s,
			insee_code TEXT PRIMARY KEY)`,
			strings.Join(columns, ", ")),
		`CREATE TABLE IF NOT EXISTS notes(
			id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			org_id INTEGER NOT NULL,
			insee_code TEXT, volunteer TEXT, status TEXT, note TEXT, ts TEXT,
			team_id INTEGER)`,
		// Local teams: a team = a work scope (usually one or more
		// departments) with its lead. The name is only unique WITHIN a
		// campaign: two campaigns each have their own "Nord" team.
		`CREATE TABLE IF NOT EXISTS teams(
			id INTEGER GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			org_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			departments TEXT DEFAULT '',
			created_at TEXT)`,
		// Same remark for the address: one person can volunteer in two
		// campaigns hosted here, with the same address.
		`CREATE TABLE IF NOT EXISTS accounts(
			org_id INTEGER NOT NULL,
			email TEXT NOT NULL,
			name TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'volunteer',
			team_id INTEGER,
			active BOOLEAN NOT NULL DEFAULT TRUE,
			personal_note TEXT DEFAULT '',
			created_at TEXT, created_by TEXT,
			PRIMARY KEY (org_id, email))`,
		// Sign-in links. Only the token's SHA-256 is here: what arrives in an
		// inbox exists nowhere on this side, so a dump of this table opens no
		// account (link.go).
		//
		// expires_at is a real timestamptz and not the TEXT the display
		// columns use: it is compared, not shown, and a comparison on a
		// truncated string is a comparison that is wrong at a minute
		// boundary.
		`CREATE TABLE IF NOT EXISTS login_tokens(
			org_id INTEGER NOT NULL,
			token_hash TEXT NOT NULL,
			email TEXT NOT NULL,
			purpose TEXT NOT NULL,
			expires_at TIMESTAMPTZ NOT NULL,
			created_at TEXT,
			PRIMARY KEY (org_id, token_hash))`,
	}

	for _, s := range statements {
		if _, err := tx.Exec(ctx, s); err != nil {
			return 0, fmt.Errorf("schema: %w", err)
		}
	}

	indexes := []string{
		// the screens almost always filter on these columns
		`CREATE INDEX IF NOT EXISTS mayors_sort ON mayors(rank, score DESC, insee_code)`,
		`CREATE INDEX IF NOT EXISTS notes_insee ON notes(org_id, insee_code)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS teams_org_name ON teams(org_id, name)`,
		// ONE live link per address AND PURPOSE, enforced by PostgreSQL
		// rather than by the DELETE that precedes the INSERT. Under READ
		// COMMITTED that DELETE does not see a neighbour's uncommitted row,
		// so two requests arriving together both inserted: two links in one
		// inbox, the older already dead.
		//
		// The purpose is part of it because the two kinds do not compete: a
		// volunteer who asks for a sign-in link while their INVITATION is
		// still pending was destroying an invitation they had not asked
		// about and would find dead days later.
		`CREATE UNIQUE INDEX IF NOT EXISTS login_tokens_address ON login_tokens(org_id, email, purpose)`,
	}
	for _, s := range indexes {
		if _, err := tx.Exec(ctx, s); err != nil {
			return 0, fmt.Errorf("schema: %w", err)
		}
	}

	// migration: a column added to the CSV (a new signal) must appear
	// without destroying the team's work
	rows, err := tx.Query(ctx, "SELECT column_name FROM information_schema.columns "+
		"WHERE table_name='mayors'")
	if err != nil {
		return 0, fmt.Errorf("reading the schema: %w", err)
	}
	existing := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return 0, fmt.Errorf("reading the schema: %w", err)
		}
		existing[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("reading the schema: %w", err)
	}
	for _, c := range Cols {
		if !existing[c] {
			if _, err := tx.Exec(ctx,
				fmt.Sprintf("ALTER TABLE mayors ADD COLUMN %s TEXT", c)); err != nil {
				return 0, fmt.Errorf("adding column %s: %w", c, err)
			}
			slog.Info("schema: column added", "column", c)
		}
	}
	return bootstrapOrg, nil
}

func shortTimestamp() string {
	return time.Now().Format("2006-01-02T15:04")
}
