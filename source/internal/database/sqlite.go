package database

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

//go:embed schema_sqlite.sql
var schemaSQL string

// DB is the player database: one SQLite file, no server process.
type DB struct {
	mu sync.Mutex
	db *sql.DB
}

// Open opens (creating it if needed) the SQLite file at path.
func Open(path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "." {
		if e := os.MkdirAll(dir, 0o755); e != nil {
			return nil, e
		}
	}
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	h, e := sql.Open("sqlite", dsn)
	if e != nil {
		return nil, e
	}
	h.SetMaxOpenConns(1) // one connection: last_insert_rowid() and changes() follow the statement before them
	d := &DB{db: h}
	if e := d.Ping(); e != nil {
		return nil, e
	}
	return d, nil
}

func (d *DB) Close() error { return d.db.Close() }

// esc makes a value safe inside a single-quoted SQLite string literal. SQLite
// has no backslash escapes, so only the quote is doubled; NUL and control
// bytes are dropped.
func esc(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\'':
			b.WriteString("''")
		case r < 0x20:
			// drop NUL/control characters
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// The queries were written for MariaDB. translate rewrites the few
// MariaDB-only constructs they use; text inside quotes is never touched.
var (
	reDateAdd   = regexp.MustCompile(`(?i)DATE_(ADD|SUB)\(\s*NOW\(\)\s*,\s*INTERVAL\s+(\d+)\s+(SECOND|MINUTE|HOUR|DAY)S?\s*\)`)
	reUnixTS    = regexp.MustCompile(`(?i)UNIX_TIMESTAMP\(([^()]+)\)`)
	reNow       = regexp.MustCompile(`(?i)\bNOW\(\)`)
	reLastID    = regexp.MustCompile(`(?i)\bLAST_INSERT_ID\(\)`)
	reRowCount  = regexp.MustCompile(`(?i)\bROW_COUNT\(\)`)
	reInsIgnore = regexp.MustCompile(`(?i)\bINSERT\s+IGNORE\b`)
	reLeast     = regexp.MustCompile(`(?i)\bLEAST\(`)
)

func translateCode(s string) string {
	s = reDateAdd.ReplaceAllStringFunc(s, func(m string) string {
		g := reDateAdd.FindStringSubmatch(m)
		sign := "+"
		if strings.EqualFold(g[1], "SUB") {
			sign = "-"
		}
		return fmt.Sprintf("datetime('now','%s%s %ss')", sign, g[2], strings.ToLower(g[3]))
	})
	s = reUnixTS.ReplaceAllString(s, "CAST(strftime('%s',$1) AS INTEGER)")
	s = reNow.ReplaceAllString(s, "datetime('now')")
	s = reLastID.ReplaceAllString(s, "last_insert_rowid()")
	s = reRowCount.ReplaceAllString(s, "changes()")
	s = reInsIgnore.ReplaceAllString(s, "INSERT OR IGNORE")
	s = reLeast.ReplaceAllString(s, "MIN(")
	return s
}

// splitStatements splits on ';' outside single-quoted strings and applies translateCode to the code parts.
func splitStatements(sqlText string) []string {
	var stmts []string
	var cur, code strings.Builder
	flush := func() {
		cur.WriteString(translateCode(code.String()))
		code.Reset()
	}
	inQuote := false
	for i := 0; i < len(sqlText); i++ {
		c := sqlText[i]
		switch {
		case c == '\'':
			if !inQuote {
				flush()
			}
			inQuote = !inQuote
			cur.WriteByte(c)
		case inQuote:
			cur.WriteByte(c)
		case c == ';':
			flush()
			if s := strings.TrimSpace(cur.String()); s != "" {
				stmts = append(stmts, s)
			}
			cur.Reset()
		default:
			code.WriteByte(c)
		}
	}
	flush()
	if s := strings.TrimSpace(cur.String()); s != "" {
		stmts = append(stmts, s)
	}
	return stmts
}

func isRowQuery(stmt string) bool {
	u := strings.ToUpper(stmt)
	return strings.HasPrefix(u, "SELECT") || strings.HasPrefix(u, "WITH") || strings.HasPrefix(u, "PRAGMA")
}

// query runs one or more ';'-separated statements and returns every row of
// every SELECT as a tab-separated line (NULL as "NULL"), as the mariadb
// client did in batch mode.
func (d *DB) query(sqlText string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, stmt := range splitStatements(sqlText) {
		if !isRowQuery(stmt) {
			if _, e := d.db.Exec(stmt); e != nil {
				return nil, fmt.Errorf("sqlite: %w: %.200s", e, stmt)
			}
			continue
		}
		rows, e := d.db.Query(stmt)
		if e != nil {
			return nil, fmt.Errorf("sqlite: %w: %.200s", e, stmt)
		}
		cols, _ := rows.Columns()
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		for rows.Next() {
			if e := rows.Scan(ptrs...); e != nil {
				rows.Close()
				return nil, e
			}
			f := make([]string, len(cols))
			for i, v := range vals {
				switch x := v.(type) {
				case nil:
					f[i] = "NULL"
				case int64:
					f[i] = strconv.FormatInt(x, 10)
				case float64:
					f[i] = strconv.FormatFloat(x, 'f', -1, 64)
				case []byte:
					f[i] = string(x)
				case string:
					f[i] = x
				default:
					f[i] = fmt.Sprint(x)
				}
			}
			out = append(out, strings.Join(f, "\t"))
		}
		if e := rows.Err(); e != nil {
			rows.Close()
			return nil, e
		}
		rows.Close()
	}
	return out, nil
}

func (d *DB) Ping() error { _, e := d.query("SELECT 1;"); return e }

// Migrate creates the tables that do not exist yet.
func (d *DB) Migrate() error {
	_, e := d.query(schemaSQL)
	return e
}
