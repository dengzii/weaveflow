package neo

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tmc/langchaingo/llms"
	_ "modernc.org/sqlite"
)

// HistoryStore persists conversation messages to a SQLite database in the
// same HistoryMessage format served to the frontend.
type HistoryStore struct {
	db *sql.DB
}

func NewHistoryStore(path string) (*HistoryStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if _, err = db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS messages (
		seq        INTEGER PRIMARY KEY AUTOINCREMENT,
		role       TEXT    NOT NULL,
		parts      TEXT    NOT NULL,
		created_at INTEGER NOT NULL DEFAULT 0,
		status     TEXT
	)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	// Migrate older schemas that lack the new columns.
	for _, col := range []string{
		`ALTER TABLE messages ADD COLUMN created_at INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE messages ADD COLUMN status TEXT`,
	} {
		_, _ = db.Exec(col) // ignore errors — column already exists
	}
	return &HistoryStore{db: db}, nil
}

func (s *HistoryStore) Close() error { return s.db.Close() }

// Save replaces the stored conversation with msgs (in client HistoryMessage format).
// The last message's status is set to runStatus ("completed", "failed", "stopped").
func (s *HistoryStore) Save(msgs []HistoryMessage, runStatus string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM messages`); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	for i, m := range msgs {
		b, _ := json.Marshal(m.Parts)
		status := m.Status
		if status == "" && i == len(msgs)-1 && runStatus != "" {
			status = runStatus
		}
		createdAt := m.CreatedAt
		if createdAt == 0 {
			createdAt = now
		}
		if _, err = tx.Exec(`INSERT INTO messages(role, parts, created_at, status) VALUES(?, ?, ?, ?)`,
			m.Role, string(b), createdAt, nullableString(status)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Load returns the stored conversation as HistoryMessage slices (ready for the frontend).
func (s *HistoryStore) Load() ([]HistoryMessage, error) {
	rows, err := s.db.Query(`SELECT role, parts, created_at, status FROM messages ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []HistoryMessage
	for rows.Next() {
		var role, partsJSON string
		var createdAt int64
		var status sql.NullString
		if err = rows.Scan(&role, &partsJSON, &createdAt, &status); err != nil {
			return nil, err
		}
		var parts []MessagePart
		if err = json.Unmarshal([]byte(partsJSON), &parts); err != nil {
			return nil, err
		}
		result = append(result, HistoryMessage{
			Role:      role,
			Parts:     parts,
			CreatedAt: createdAt,
			Status:    status.String,
		})
	}
	return result, rows.Err()
}

// LoadLLMMessages converts stored history to llms.MessageContent for LLM context.
func (s *HistoryStore) LoadLLMMessages() ([]llms.MessageContent, error) {
	msgs, err := s.Load()
	if err != nil {
		return nil, err
	}
	result := make([]llms.MessageContent, len(msgs))
	for i, m := range msgs {
		result[i] = historyToLLM(m)
	}
	return result, nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
