package neo

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
	_ "modernc.org/sqlite"
)

const (
	defaultSessionID = "default"
	defaultConfigKey = "default"
)

type Store struct {
	db *sql.DB
}

type PersistedConfig struct {
	SystemPrompt      string
	MaxIterations     int
	PlannerMaxSteps   int
	MemoryRecallLimit int
	ToolFlags         map[string]bool
	Mode              string
}

type turnMessageRecord struct {
	Seq       int64
	SessionID string
	TurnID    string
	Role      string
	PartsJSON string
	Status    sql.NullString
	CreatedAt int64
	UpdatedAt int64
}

func NewStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if _, err = db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	s := &Store{db: db}
	if err := s.initSchema(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) initSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS messages (
			seq        INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT    NOT NULL DEFAULT 'default',
			role       TEXT    NOT NULL,
			parts      TEXT    NOT NULL,
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0,
			status     TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session_seq ON messages(session_id, seq)`,
		`CREATE TABLE IF NOT EXISTS turn_messages (
			seq        INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT    NOT NULL,
			turn_id    TEXT    NOT NULL,
			role       TEXT    NOT NULL,
			parts      TEXT    NOT NULL,
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0,
			status     TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_turn_messages_session_seq ON turn_messages(session_id, seq)`,
		`CREATE TABLE IF NOT EXISTS configs (
			config_key          TEXT PRIMARY KEY,
			system_prompt       TEXT    NOT NULL,
			max_iterations      INTEGER NOT NULL,
			planner_max_steps   INTEGER NOT NULL,
			memory_recall_limit INTEGER NOT NULL,
			tool_flags          TEXT    NOT NULL,
			mode                TEXT    NOT NULL,
			created_at          INTEGER NOT NULL DEFAULT 0,
			updated_at          INTEGER NOT NULL DEFAULT 0
		)`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("init schema: %w", err)
		}
	}

	for _, stmt := range []string{
		`ALTER TABLE messages ADD COLUMN session_id TEXT NOT NULL DEFAULT 'default'`,
		`ALTER TABLE messages ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE messages ADD COLUMN created_at INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE messages ADD COLUMN status TEXT`,
	} {
		_, _ = s.db.Exec(stmt)
	}

	return nil
}

func (s *Store) SaveRawHistory(sessionID string, msgs []HistoryMessage, runStatus string) error {
	sessionID = normalizeSessionID(sessionID)

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err = tx.Exec(`DELETE FROM messages WHERE session_id = ?`, sessionID); err != nil {
		return err
	}

	now := time.Now().UnixMilli()
	for i, msg := range msgs {
		partsJSON, err := marshalParts(msg.Parts)
		if err != nil {
			return err
		}

		status := msg.Status
		if status == "" && i == len(msgs)-1 && runStatus != "" {
			status = runStatus
		}

		createdAt := msg.CreatedAt
		if createdAt == 0 {
			createdAt = now
		}

		if _, err = tx.Exec(
			`INSERT INTO messages(session_id, role, parts, created_at, updated_at, status) VALUES(?, ?, ?, ?, ?, ?)`,
			sessionID,
			msg.Role,
			partsJSON,
			createdAt,
			now,
			nullableString(status),
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) LoadHistory(sessionID string) ([]HistoryMessage, error) {
	sessionID = normalizeSessionID(sessionID)

	items, err := s.loadTurnHistory(sessionID)
	if err != nil {
		return nil, err
	}
	if len(items) > 0 {
		return items, nil
	}
	return s.loadRawHistory(sessionID)
}

func (s *Store) LoadLLMMessages(sessionID string) ([]llms.MessageContent, error) {
	msgs, err := s.loadRawHistory(normalizeSessionID(sessionID))
	if err != nil {
		return nil, err
	}

	result := make([]llms.MessageContent, len(msgs))
	for i, msg := range msgs {
		result[i] = historyToLLM(msg)
	}
	return result, nil
}

func (s *Store) LoadConfig() (PersistedConfig, bool, error) {
	row := s.db.QueryRow(
		`SELECT system_prompt, max_iterations, planner_max_steps, memory_recall_limit, tool_flags, mode
		 FROM configs WHERE config_key = ?`,
		defaultConfigKey,
	)

	var cfg PersistedConfig
	var toolFlagsJSON string
	if err := row.Scan(
		&cfg.SystemPrompt,
		&cfg.MaxIterations,
		&cfg.PlannerMaxSteps,
		&cfg.MemoryRecallLimit,
		&toolFlagsJSON,
		&cfg.Mode,
	); err != nil {
		if err == sql.ErrNoRows {
			return PersistedConfig{}, false, nil
		}
		return PersistedConfig{}, false, err
	}

	if err := json.Unmarshal([]byte(toolFlagsJSON), &cfg.ToolFlags); err != nil {
		return PersistedConfig{}, false, err
	}
	if cfg.ToolFlags == nil {
		cfg.ToolFlags = map[string]bool{}
	}
	return cfg, true, nil
}

func (s *Store) SaveConfig(cfg PersistedConfig) error {
	flagsJSON, err := json.Marshal(cfg.ToolFlags)
	if err != nil {
		return err
	}

	now := time.Now().UnixMilli()
	mode := strings.TrimSpace(cfg.Mode)
	if mode == "" {
		mode = "auto"
	}

	_, err = s.db.Exec(
		`INSERT INTO configs(
			config_key, system_prompt, max_iterations, planner_max_steps, memory_recall_limit,
			tool_flags, mode, created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(config_key) DO UPDATE SET
			system_prompt = excluded.system_prompt,
			max_iterations = excluded.max_iterations,
			planner_max_steps = excluded.planner_max_steps,
			memory_recall_limit = excluded.memory_recall_limit,
			tool_flags = excluded.tool_flags,
			mode = excluded.mode,
			updated_at = excluded.updated_at`,
		defaultConfigKey,
		cfg.SystemPrompt,
		cfg.MaxIterations,
		cfg.PlannerMaxSteps,
		cfg.MemoryRecallLimit,
		string(flagsJSON),
		mode,
		now,
		now,
	)
	return err
}

func (s *Store) BeginTurn(sessionID, userInput string) (*TurnWriter, error) {
	sessionID = normalizeSessionID(sessionID)
	if err := s.bootstrapTurnHistoryFromRaw(sessionID); err != nil {
		return nil, err
	}

	turnID := uuid.NewString()
	now := time.Now().UnixMilli()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	userParts := []MessagePart{{Type: "text", Text: userInput}}
	userJSON, err := marshalParts(userParts)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(
		`INSERT INTO turn_messages(session_id, turn_id, role, parts, created_at, updated_at, status)
		 VALUES(?, ?, ?, ?, ?, ?, NULL)`,
		sessionID,
		turnID,
		string(llms.ChatMessageTypeHuman),
		userJSON,
		now,
		now,
	); err != nil {
		return nil, err
	}

	result, err := tx.Exec(
		`INSERT INTO turn_messages(session_id, turn_id, role, parts, created_at, updated_at, status)
		 VALUES(?, ?, ?, ?, ?, ?, ?)`,
		sessionID,
		turnID,
		string(llms.ChatMessageTypeAI),
		"[]",
		now,
		now,
		"running",
	)
	if err != nil {
		return nil, err
	}

	assistantSeq, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &TurnWriter{
		store:        s,
		sessionID:    sessionID,
		turnID:       turnID,
		assistantSeq: assistantSeq,
		status:       "running",
	}, nil
}

func (s *Store) bootstrapTurnHistoryFromRaw(sessionID string) error {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM turn_messages WHERE session_id = ?`, sessionID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	rawHistory, err := s.loadRawHistory(sessionID)
	if err != nil {
		return err
	}
	if len(rawHistory) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UnixMilli()
	for _, msg := range rawHistory {
		partsJSON, err := marshalParts(msg.Parts)
		if err != nil {
			return err
		}
		createdAt := msg.CreatedAt
		if createdAt == 0 {
			createdAt = now
		}
		if _, err = tx.Exec(
			`INSERT INTO turn_messages(session_id, turn_id, role, parts, created_at, updated_at, status)
			 VALUES(?, ?, ?, ?, ?, ?, ?)`,
			sessionID,
			uuid.NewString(),
			msg.Role,
			partsJSON,
			createdAt,
			now,
			nullableString(msg.Status),
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) updateTurnMessage(seq int64, parts []MessagePart, status string) error {
	partsJSON, err := marshalParts(parts)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(
		`UPDATE turn_messages
		 SET parts = ?, status = ?, updated_at = ?
		 WHERE seq = ?`,
		partsJSON,
		nullableString(status),
		time.Now().UnixMilli(),
		seq,
	)
	return err
}

func (s *Store) loadTurnHistory(sessionID string) ([]HistoryMessage, error) {
	rows, err := s.db.Query(
		`SELECT role, parts, created_at, status
		 FROM turn_messages
		 WHERE session_id = ?
		 ORDER BY seq`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]HistoryMessage, 0)
	for rows.Next() {
		msg, err := scanHistoryMessage(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, msg)
	}
	return items, rows.Err()
}

func (s *Store) loadRawHistory(sessionID string) ([]HistoryMessage, error) {
	rows, err := s.db.Query(
		`SELECT role, parts, created_at, status
		 FROM messages
		 WHERE session_id = ?
		 ORDER BY seq`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]HistoryMessage, 0)
	for rows.Next() {
		msg, err := scanHistoryMessage(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, msg)
	}
	return items, rows.Err()
}

func scanHistoryMessage(scanner interface{ Scan(dest ...any) error }) (HistoryMessage, error) {
	var role string
	var partsJSON string
	var createdAt int64
	var status sql.NullString
	if err := scanner.Scan(&role, &partsJSON, &createdAt, &status); err != nil {
		return HistoryMessage{}, err
	}

	var parts []MessagePart
	if err := json.Unmarshal([]byte(partsJSON), &parts); err != nil {
		return HistoryMessage{}, err
	}

	return HistoryMessage{
		Role:      role,
		Parts:     parts,
		CreatedAt: createdAt,
		Status:    status.String,
	}, nil
}

func marshalParts(parts []MessagePart) (string, error) {
	data, err := json.Marshal(parts)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func normalizeSessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return defaultSessionID
	}
	return sessionID
}

func nullableString(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
