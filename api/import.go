package main

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// The startup import: out/04_base_complete.csv into `mayors`, refreshed on
// content change, work columns never touched.

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
	// …and a floor relative to what is ALREADY there, because the absolute
	// one is 3 % of a real list. A CSV cut to 5,000 rows cleared it, and
	// removeStale then deleted 29,813 mayors and flagged the cards the team
	// had worked with « parrainage attribué à tort » — a statement about the
	// crossing that nothing had re-run. A truncated file is not a correction,
	// and telling a volunteer their work was wrong is the one mistake with no
	// way back.
	//
	// Read BEFORE the upsert: after it, the new rows are already counted.
	// A shrinking list is legitimate (the RNE loses mayors between
	// elections), a list losing a tenth of itself in one restart is not.
	var current int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM mayors").Scan(&current); err != nil {
		return fmt.Errorf("counting the list in place: %w", err)
	}
	if floor := current - current/10; current > 0 && len(rows) < floor {
		return fmt.Errorf("import refused: %s carries %d row(s) against %d "+
			"already in the database. Losing more than a tenth of the list in "+
			"one import is a truncated file, not a correction — check the "+
			"build, then reimport", path, len(rows), current)
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
		slog.Info("list unchanged, import skipped", "mayors", len(rows))
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
	slog.Info("list updated", "stale_deleted", len(untouched),
		"worked_on_flagged", len(touched))
	return nil
}

// ReadCSV reads out/04_base_complete.csv (";" separator, UTF-8 with BOM).
func ReadCSV(path string) ([]map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w (the image must be built "+
			"after `task build`)", path, err)
	}
	defer f.Close() //nolint:errcheck // read-only

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
		slices.Sort(duplicates)
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
