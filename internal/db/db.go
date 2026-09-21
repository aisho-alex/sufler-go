package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at REAL NOT NULL,
    ended_at REAL,
    note TEXT
);
CREATE TABLE IF NOT EXISTS segments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER NOT NULL REFERENCES sessions(id),
    ts_start REAL NOT NULL,
    ts_end REAL NOT NULL,
    source TEXT NOT NULL,
    text TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_segments_session ON segments(session_id, ts_start);
CREATE TABLE IF NOT EXISTS hints (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER NOT NULL REFERENCES sessions(id),
    ts REAL NOT NULL,
    hint TEXT,
    importance TEXT,
    model TEXT
);
CREATE TABLE IF NOT EXISTS notes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER NOT NULL REFERENCES sessions(id),
    ts REAL NOT NULL,
    text TEXT NOT NULL,
    reason TEXT,
    source TEXT NOT NULL DEFAULT 'llm'
);
CREATE TABLE IF NOT EXISTS qa (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER NOT NULL REFERENCES sessions(id),
    ts REAL NOT NULL,
    question TEXT NOT NULL,
    answer TEXT
);
`

type Segment struct {
	TsStart float64
	TsEnd   float64
	Source  string
	Text    string
}

type Note struct {
	Ts     float64
	Text   string
	Reason string
}

type DB struct {
	mu   sync.Mutex
	conn *sql.DB
}

func Open(path string) (*DB, error) {
	if d := filepath.Dir(path); d != "" && d != "." {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	if _, err := conn.Exec("PRAGMA journal_mode=WAL"); err != nil {
		conn.Close()
		return nil, err
	}
	if _, err := conn.Exec("PRAGMA busy_timeout=5000"); err != nil {
		conn.Close()
		return nil, err
	}
	if _, err := conn.Exec(schema); err != nil {
		conn.Close()
		return nil, err
	}
	d := &DB{conn: conn}
	if err := d.migrate(); err != nil {
		conn.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) migrate() error {
	rows, err := d.conn.Query("PRAGMA table_info(notes)")
	if err != nil {
		return err
	}
	cols := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		cols[name] = true
	}
	rows.Close()
	if !cols["source"] {
		_, err := d.conn.Exec(
			"ALTER TABLE notes ADD COLUMN source TEXT NOT NULL DEFAULT 'llm'")
		return err
	}
	return nil
}

func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func toNull(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (d *DB) exec(query string, args ...any) (sql.Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.conn.Exec(query, args...)
}

func (d *DB) StartSession(note string) (int64, error) {
	res, err := d.exec(
		"INSERT INTO sessions (started_at, note) VALUES (?, ?)",
		now(), toNull(note))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) EndSession(sessionID int64) error {
	_, err := d.exec(
		"UPDATE sessions SET ended_at = ? WHERE id = ?", now(), sessionID)
	return err
}

func (d *DB) SaveSegment(sessionID int64, tsStart, tsEnd float64,
	source, text string) error {
	_, err := d.exec(
		"INSERT INTO segments (session_id, ts_start, ts_end, source, text)"+
			" VALUES (?, ?, ?, ?, ?)",
		sessionID, tsStart, tsEnd, source, text)
	return err
}

func (d *DB) SaveHint(sessionID int64, hint, importance,
	model string) (int64, error) {
	if hint == "" {
		return 0, nil
	}
	res, err := d.exec(
		"INSERT INTO hints (session_id, ts, hint, importance, model)"+
			" VALUES (?, ?, ?, ?, ?)",
		sessionID, now(), hint, toNull(importance), toNull(model))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) SaveNote(sessionID int64, text, reason,
	source string) (int64, error) {
	if text == "" {
		return 0, nil
	}
	res, err := d.exec(
		"INSERT INTO notes (session_id, ts, text, reason, source)"+
			" VALUES (?, ?, ?, ?, ?)",
		sessionID, now(), text, toNull(reason), source)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) SaveQA(sessionID int64, question, answer string) (int64, error) {
	if question == "" {
		return 0, nil
	}
	res, err := d.exec(
		"INSERT INTO qa (session_id, ts, question, answer) VALUES (?, ?, ?, ?)",
		sessionID, now(), question, toNull(answer))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) SessionSegments(sessionID int64) ([]Segment, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.conn.Query(
		"SELECT ts_start, ts_end, source, text FROM segments"+
			" WHERE session_id = ? ORDER BY ts_start", sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Segment
	for rows.Next() {
		var s Segment
		if err := rows.Scan(&s.TsStart, &s.TsEnd, &s.Source, &s.Text); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) SessionNotes(sessionID int64) ([]Note, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.conn.Query(
		"SELECT ts, text, reason FROM notes WHERE session_id = ? ORDER BY ts",
		sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Note
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.Ts, &n.Text, &n.Reason); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (d *DB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.conn.Close()
}
