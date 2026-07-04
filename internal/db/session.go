package db

import (
	"database/sql"
	"time"
)

// StoredSession 是持久化到 sessions 表中的会话数据。
type StoredSession struct {
	UserID      int64
	SessionType string
	Step        int
	DraftJSON   string
	UpdatedAt   time.Time
}

// UpsertSession 插入或更新一条会话记录。
func UpsertSession(database *sql.DB, s *StoredSession) error {
	_, err := database.Exec(
		`INSERT INTO sessions (user_id, session_type, step, draft_json, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(user_id, session_type) DO UPDATE SET
		 step = excluded.step, draft_json = excluded.draft_json, updated_at = excluded.updated_at`,
		s.UserID, s.SessionType, s.Step, s.DraftJSON, time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

// GetSession 读取指定用户和类型的会话，不存在返回 nil。
func GetSession(database *sql.DB, userID int64, sessionType string) (*StoredSession, error) {
	row := database.QueryRow(
		`SELECT user_id, session_type, step, draft_json, updated_at
		 FROM sessions WHERE user_id = ? AND session_type = ?`,
		userID, sessionType,
	)
	var s StoredSession
	var updatedAt string
	err := row.Scan(&s.UserID, &s.SessionType, &s.Step, &s.DraftJSON, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
		s.UpdatedAt = t
	}
	return &s, nil
}

// DeleteSession 删除指定用户和类型的会话。
func DeleteSession(database *sql.DB, userID int64, sessionType string) error {
	_, err := database.Exec(
		`DELETE FROM sessions WHERE user_id = ? AND session_type = ?`,
		userID, sessionType,
	)
	return err
}

// ListAllSessions 返回所有持久化会话，用于进程重启时恢复。
func ListAllSessions(database *sql.DB) ([]*StoredSession, error) {
	rows, err := database.Query(
		`SELECT user_id, session_type, step, draft_json, updated_at
		 FROM sessions ORDER BY user_id, session_type`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*StoredSession
	for rows.Next() {
		var s StoredSession
		var updatedAt string
		if err := rows.Scan(&s.UserID, &s.SessionType, &s.Step, &s.DraftJSON, &updatedAt); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
			s.UpdatedAt = t
		}
		sessions = append(sessions, &s)
	}
	return sessions, rows.Err()
}
