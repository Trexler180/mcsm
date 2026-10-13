package store

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
)

// Times used to be stored four ways: CURRENT_TIMESTAMP text, hand-formatted
// UTC strings, unix integers, and Go time.Time values in the driver's default
// format — time.Time.String(), in the host's zone, sometimes with a monotonic
// clock suffix ("2026-01-02 11:00:00.5 -0700 PDT m=+3.1"). Text comparisons
// across those were wrong whenever the host was not on UTC.
//
// The database is now opened with _time_format=datetime and _timezone=UTC, so
// every Go write matches CURRENT_TIMESTAMP: UTC "YYYY-MM-DD HH:MM:SS".
// NormalizeTimes rewrites the rows written before that to the same form.
// Unix-integer columns are left alone; they were always unambiguous.

const canonicalTime = "2006-01-02 15:04:05"

// canonicalGlob matches a value already in canonical form.
const canonicalGlob = "[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9] [0-9][0-9]:[0-9][0-9]:[0-9][0-9]"

var monotonicSuffix = regexp.MustCompile(`\s+m=[+-][0-9.]+$`)

var storedTimeLayouts = []string{
	"2006-01-02 15:04:05.999999999 -0700 MST", // time.Time.String()
	"2006-01-02 15:04:05.999999999 -0700",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02T15:04:05.999999999-07:00",
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04",
	"2006-01-02T15:04",
	"2006-01-02",
}

// parseStoredTime reads any of the formats a time has been stored in. A value
// without a zone was written as UTC.
func parseStoredTime(v string) (time.Time, bool) {
	v = monotonicSuffix.ReplaceAllString(strings.TrimSpace(v), "")
	for _, layout := range storedTimeLayouts {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// NormalizeTimes rewrites every date/time column value that is not already
// canonical UTC text. It is idempotent and cheap once done (each column is a
// single query that finds nothing), so it runs at every startup.
func (s *Store) NormalizeTimes(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name != 'goose_db_version'`)
	if err != nil {
		return 0, err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return 0, err
		}
		tables = append(tables, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	total := 0
	for _, table := range tables {
		cols, err := s.timeColumns(ctx, table)
		if err != nil {
			return total, err
		}
		for _, col := range cols {
			n, err := s.normalizeColumn(ctx, table, col)
			total += n
			if err != nil {
				return total, fmt.Errorf("normalize %s.%s: %w", table, col, err)
			}
		}
	}
	return total, nil
}

// timeColumns lists a table's columns declared as a date or time.
func (s *Store) timeColumns(ctx context.Context, table string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, type FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, err
		}
		t := strings.ToUpper(typ)
		if strings.Contains(t, "DATE") || strings.Contains(t, "TIME") {
			cols = append(cols, name)
		}
	}
	return cols, rows.Err()
}

func quoteIdent(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

func (s *Store) normalizeColumn(ctx context.Context, table, col string) (int, error) {
	t, c := quoteIdent(table), quoteIdent(col)
	rows, err := s.db.QueryContext(ctx,
		`SELECT rowid, `+c+` FROM `+t+` WHERE typeof(`+c+`) = 'text' AND NOT (`+c+` GLOB '`+canonicalGlob+`')`)
	if err != nil {
		return 0, err
	}
	type fix struct {
		rowid int64
		value string
	}
	var fixes []fix
	for rows.Next() {
		var rowid int64
		var raw string
		if err := rows.Scan(&rowid, &raw); err != nil {
			rows.Close()
			return 0, err
		}
		parsed, ok := parseStoredTime(raw)
		if !ok {
			slog.Warn("store: unreadable stored time left as is", "table", table, "column", col, "rowid", rowid)
			continue
		}
		fixes = append(fixes, fix{rowid, parsed.Format(canonicalTime)})
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(fixes) == 0 {
		return 0, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, f := range fixes {
		if _, err := tx.ExecContext(ctx, `UPDATE `+t+` SET `+c+` = ? WHERE rowid = ?`, f.value, f.rowid); err != nil {
			return 0, err
		}
	}
	return len(fixes), tx.Commit()
}
