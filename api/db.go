package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"os"
	"sort"
	"strconv"
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
		log.Printf("lock %d not released: %v (it will drop at disconnect)", key, err)
	}
}

// SessionSecret: the cookie signing key. Never derived from a human
// password — a captured cookie would allow breaking it offline. Either
// provided by the environment, or drawn at random once and kept, so
// sessions survive restarts.
func SessionSecret(ctx context.Context, pool *pgxpool.Pool) ([]byte, error) {
	provided, err := UsableSecret(os.Getenv("PARAPHE_SECRET_KEY"), "PARAPHE_SECRET_KEY")
	if err != nil {
		return nil, err
	}
	if provided != "" {
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
	raw := make([]byte, 32)
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

	log.Print("import: waiting for the lock…")
	// outside the transaction: otherwise the snapshot is frozen at lock
	// acquisition, hence BEFORE the wait, and the next instance works on a
	// stale view
	if err := takeLock(ctx, c, ImportLock); err != nil {
		return err
	}
	defer releaseLock(ctx, c, ImportLock)
	log.Print("import: lock acquired")

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
			log.Printf("schema: column %s added", c)
		}
	}
	return bootstrapOrg, nil
}

func importList(ctx context.Context, tx pgx.Tx, path string) error {
	rows, err := ReadCSV(path)
	if err != nil {
		return err
	}
	// safety net: `WHERE NOT (insee_code = ANY('{}'))` is TRUE for every
	// row. An empty or truncated CSV (image built without `task build`)
	// would therefore empty the database — without an error, with a
	// reassuring message.
	if len(rows) < 1000 {
		return fmt.Errorf("import refused: %d row(s) in %s, while ~34,800 are "+
			"expected. Truncated or missing file — run `task build`",
			len(rows), path)
	}

	// A single instance imports: the next ones see the list is already
	// there and release the lock at once. Without this, ten replicas would
	// re-import one after another and the last would exceed the timeout.
	//
	// The skip is decided on the CONTENT, never on the row count. Counting
	// looks equivalent and is not: the routine change is a corrected email,
	// a revised score, or a false positive whose rank drops — all
	// count-preserving. A correction that stops one mayor being thanked for
	// someone else's endorsement leaves 34 826 rows before and after, and a
	// count check would never let it reach a running instance.
	digest, err := fileDigest(path)
	if err != nil {
		return err
	}
	var stored string
	err = tx.QueryRow(ctx,
		"SELECT value FROM settings WHERE key='list_digest'").Scan(&stored)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("reading the list digest: %w", err)
	}
	if stored == digest {
		log.Printf("list unchanged (%d mayors), import skipped", len(rows))
		return nil
	}

	// The score is cast to int by every listing query: one unreadable value
	// takes the whole list down with a 500 — every screen of every
	// volunteer, on one bad row. Refused here, named, at startup.
	for _, r := range rows {
		// The empty string is the one non-integer that actually arrives. One
		// such row in 34 826 answers 500 on "take a batch", for the whole
		// campaign. The casts downstream are tolerant AND this refuses it:
		// an empty score is a crossing that went wrong, not a value.
		// int32, not int: the column is cast to PostgreSQL's `int`, so a
		// value Atoi accepts happily — 3000000000 — still answers "out of
		// range for type integer" and takes the whole list down. The guard
		// has to be at least as strict as the queries it protects.
		// Trimmed the way PostgreSQL trims — that is, not at all beyond
		// ASCII: Go's TrimSpace strips U+00A0, the `int` input does not, so
		// a value the guard accepted still raised at query time.
		v := r["score"]
		if trimmed := strings.Trim(v, " \t\n\v\f\r"); trimmed != v {
			v = trimmed
		}
		// The listing NEGATES the score, so the lowest int32 overflows on
		// the way out: the range the queries survive is one short.
		if n, err := strconv.ParseInt(v, 10, 32); err != nil || n == math.MinInt32 {
			return fmt.Errorf("score %q for INSEE %q is not an integer that "+
				"fits: the list would be unreadable for every volunteer",
				v, r["insee_code"])
		}
	}

	var columns []string
	for _, c := range Cols {
		columns = append(columns, c+" TEXT")
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(
		"CREATE TEMP TABLE import_maires(%s) ON COMMIT DROP",
		strings.Join(columns, ", "))); err != nil {
		return fmt.Errorf("import temp table: %w", err)
	}
	values := make([][]any, 0, len(rows))
	for _, r := range rows {
		row := make([]any, len(Cols))
		for i, c := range Cols {
			row[i] = r[c]
		}
		values = append(values, row)
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"import_maires"}, Cols,
		pgx.CopyFromRows(values)); err != nil {
		return fmt.Errorf("copying the list: %w", err)
	}

	// data columns are refreshed (corrected email, revised score), work
	// columns (volunteer, status, updated_at) are never touched
	var updates []string
	for _, c := range Cols {
		if c != "insee_code" {
			updates = append(updates, fmt.Sprintf("%s=excluded.%s", c, c))
		}
	}
	names := strings.Join(Cols, ",")
	if _, err := tx.Exec(ctx, fmt.Sprintf(
		"INSERT INTO mayors(%s) SELECT %s FROM import_maires "+
			"ON CONFLICT(insee_code) DO UPDATE SET %s",
		names, names, strings.Join(updates, ", "))); err != nil {
		return fmt.Errorf("importing the list: %w", err)
	}

	if err := removeStale(ctx, tx, rows); err != nil {
		return err
	}
	// Recorded LAST, inside the same transaction: a failed import must not
	// leave a digest saying the list is up to date, or the next start would
	// skip the very refresh that just failed.
	if _, err := tx.Exec(ctx,
		"INSERT INTO settings VALUES('list_digest', $1) "+
			"ON CONFLICT(key) DO UPDATE SET value=excluded.value", digest); err != nil {
		return fmt.Errorf("recording the list digest: %w", err)
	}
	return nil
}

// fileDigest: what decides whether the list has changed. The file itself,
// not the rows read from it — the crossing writes it whole, and any
// correction it carries must reach the running instances. `Cols` is part
// of the digest too: it is what the binary READS. A release that starts
// reading a column the file already carried sees the same file, and
// without this the import is skipped and that column stays NULL on every
// row of an updated-in-place instance, silently.
func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // read-only
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	sum.Write([]byte("\x00" + strings.Join(Cols, ",")))
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// removeStale: a target removed from the list (a corrected false positive)
// must disappear — keeping it would have someone write "thank you for your
// endorsement" to a mayor who never signed. Those already worked on are
// kept and flagged, so the team's work is not erased without their
// knowledge.
func removeStale(ctx context.Context, tx pgx.Tx, rows []map[string]string) error {
	alive := make([]string, 0, len(rows))
	for _, r := range rows {
		alive = append(alive, r["insee_code"])
	}
	stale, err := textColumn(ctx, tx,
		"SELECT insee_code FROM mayors WHERE NOT (insee_code = ANY($1))", alive)
	if err != nil {
		return fmt.Errorf("looking up stale targets: %w", err)
	}
	if len(stale) == 0 {
		return nil
	}
	// "already worked on" spans ALL campaigns: the mayors row is shared,
	// and deleting it would strip another campaign of its history. The
	// import transaction runs in the maintenance scope, the only place from
	// which `assignments` is visible across organisations.
	touched, err := textColumn(ctx, tx,
		"SELECT DISTINCT insee_code FROM assignments WHERE insee_code = ANY($1) "+
			"AND (volunteer IS NOT NULL OR status <> 'to_contact')", stale)
	if err != nil {
		return fmt.Errorf("looking up targets already worked on: %w", err)
	}
	workedOn := map[string]bool{}
	for _, i := range touched {
		workedOn[i] = true
	}
	var untouched []string
	for _, i := range stale {
		if !workedOn[i] {
			untouched = append(untouched, i)
		}
	}
	if len(untouched) > 0 {
		if _, err := tx.Exec(ctx,
			"DELETE FROM mayors WHERE insee_code = ANY($1)", untouched); err != nil {
			return fmt.Errorf("deleting stale targets: %w", err)
		}
	}
	if len(touched) > 0 {
		// rank and recent_candidate drive the message template: leaving them
		// would regenerate "you presented X"
		if _, err := tx.Exec(ctx,
			"UPDATE mayors SET priority='RETIRÉ', rank='no_signal', "+
				"rank_label='⚠ retiré : parrainage attribué à tort', "+
				"recent_candidate='', recent_year='', "+
				"endorsement_history='⚠ retiré de la liste : parrainage "+
				"attribué à tort (correction du croisement). Ne pas recontacter "+
				"sur cette base.' WHERE insee_code = ANY($1)", touched); err != nil {
			return fmt.Errorf("flagging removed targets: %w", err)
		}
	}
	log.Printf("list updated: %d stale target(s) deleted, "+
		"%d already worked on flagged", len(untouched), len(touched))
	return nil
}

// bootstrap: without a coordination account, nobody can enter and nobody
// can create one — the app says so instead of opening up. Multi-campaign,
// an instance administrator is also required, the only role allowed to
// approve campaign requests: without one, the public form piles up
// requests nobody can process.
func bootstrap(ctx context.Context, tx pgx.Tx, bootstrapOrg int) error {
	if err := bootstrapAdministration(ctx, tx); err != nil {
		return err
	}
	if bootstrapOrg < 1 {
		// multi-campaign instance without a bootstrap campaign: normal,
		// campaigns are born from approved requests
		return nil
	}

	email := strings.ToLower(strings.TrimSpace(os.Getenv("PARAPHE_ADMIN_EMAIL")))
	password, err := UsableSecret(os.Getenv("PARAPHE_ADMIN_PASSWORD"),
		"PARAPHE_ADMIN_PASSWORD")
	if err != nil {
		return err
	}
	if email != "" && password != "" {
		if err := seedAccount(ctx, tx, bootstrapOrg, email,
			env("PARAPHE_ADMIN_NAME", "Coordination"), password, RoleCoordination); err != nil {
			return err
		}
		log.Printf("coordination account: %s", email)
		return nil
	}
	var one int
	err = tx.QueryRow(ctx,
		"SELECT 1 FROM accounts WHERE org_id=$1 AND role='coordination' AND active",
		bootstrapOrg).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		// Refuse to start rather than serve a campaign nobody can ever
		// enter. `cp .env.exemple .env` leaves PARAPHE_ADMIN_PASSWORD
		// empty, and the application then came up, answered every request,
		// and offered a sign-in form no password opens — with nothing in
		// the logs. DEPLOYMENT.md promises the opposite.
		return fmt.Errorf("no coordination account, and none can be created: "+
			"set PARAPHE_ADMIN_EMAIL and PARAPHE_ADMIN_PASSWORD (currently "+
			"%q and %s). Without them nobody could ever sign in",
			email, describeSecret(os.Getenv("PARAPHE_ADMIN_PASSWORD")))
	}
	if err != nil {
		return fmt.Errorf("looking up a coordination account: %w", err)
	}
	return nil
}

// describeSecret says whether a secret is set WITHOUT printing it.
func describeSecret(v string) string {
	if strings.TrimSpace(v) == "" {
		return "an empty password"
	}
	return "a password that was refused"
}

// bootstrapAdministration creates the instance administrator. It lives in
// the instance scope (organisation 0), which sees NO work row: it moderates
// campaign requests, it does not read volunteers' notes.
func bootstrapAdministration(ctx context.Context, tx pgx.Tx) error {
	email := strings.ToLower(strings.TrimSpace(
		os.Getenv("PARAPHE_INSTANCE_ADMIN_EMAIL")))
	password, err := UsableSecret(os.Getenv("PARAPHE_INSTANCE_ADMIN_PASSWORD"),
		"PARAPHE_INSTANCE_ADMIN_PASSWORD")
	if err != nil {
		return err
	}
	if email == "" || password == "" {
		if BaseDomain() != "" {
			var one int
			err := tx.QueryRow(ctx,
				"SELECT 1 FROM accounts WHERE org_id=$1 AND role=$2 AND active",
				OrgInstance, RoleAdministration).Scan(&one)
			if errors.Is(err, pgx.ErrNoRows) {
				log.Print("NO INSTANCE ADMINISTRATOR: campaign requests will " +
					"pile up with nobody able to approve them. Set " +
					"PARAPHE_INSTANCE_ADMIN_EMAIL and " +
					"PARAPHE_INSTANCE_ADMIN_PASSWORD.")
			} else if err != nil {
				return fmt.Errorf("looking up an instance administrator: %w", err)
			}
		}
		return nil
	}
	if err := seedAccount(ctx, tx, OrgInstance, email,
		env("PARAPHE_INSTANCE_ADMIN_NAME", "Administration"), password,
		RoleAdministration); err != nil {
		return err
	}
	log.Printf("instance administrator: %s", email)
	return nil
}

func seedAccount(ctx context.Context, tx pgx.Tx, org int,
	email, name, password, role string) error {
	hashed, err := HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO accounts(org_id, email, name, password_hash, role, created_at, created_by) "+
			"VALUES($1,$2,$3,$4,$5,$6,'amorçage') "+
			"ON CONFLICT(org_id, email) DO UPDATE SET password_hash=excluded.password_hash, "+
			"role=excluded.role, active=TRUE",
		org, email, name, hashed, role, shortTimestamp()); err != nil {
		return fmt.Errorf("seeding account %s (%s): %w", email, role, err)
	}
	return nil
}

// ReadCSV reads out/04_base_complete.csv (";" separator, UTF-8 with BOM).
func ReadCSV(path string) ([]map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w (the image must be built "+
			"after `task build`)", path, err)
	}
	defer f.Close()

	reader := csv.NewReader(f)
	reader.Comma = ';'
	reader.ReuseRecord = true

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("unreadable header in %s: %w", path, err)
	}
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\uFEFF")
	}
	position := map[string]int{}
	for i, name := range header {
		position[name] = i
	}
	var missing []string
	for _, c := range Cols {
		if _, ok := position[c]; !ok {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("columns missing from %s: %s — the CSV does not "+
			"come from `task build`", path, strings.Join(missing, ", "))
	}

	var rows []map[string]string
	seen := map[string]bool{}
	var duplicates []string
	for n := 2; ; n++ {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, n, err)
		}
		row := make(map[string]string, len(Cols))
		for _, c := range Cols {
			row[c] = record[position[c]]
		}
		insee := row["insee_code"]
		if seen[insee] {
			duplicates = append(duplicates, insee)
		}
		seen[insee] = true
		rows = append(rows, row)
	}
	if len(duplicates) > 0 {
		// a duplicated INSEE would fail the UPSERT with "cannot affect row a
		// second time"; saying it here saves an hour of investigation
		sort.Strings(duplicates)
		if len(duplicates) > 10 {
			duplicates = append(duplicates[:10], "…")
		}
		return nil, fmt.Errorf("%s: duplicated insee_code (%s) — the "+
			"deduplication in outils/build.ts was bypassed", path,
			strings.Join(duplicates, ", "))
	}
	return rows, nil
}

func textColumn(ctx context.Context, q pgx.Tx, sql string, args ...any) ([]string, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func shortTimestamp() string {
	return time.Now().Format("2006-01-02T15:04")
}
