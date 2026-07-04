package db

import (
	"database/sql"
	"time"
)

// HasSeenUID 判断某账号的某个 POP3 UIDL 是否已经处理过。
func HasSeenUID(db *sql.DB, accountID int64, uidl string) (bool, error) {
	var count int
	err := db.QueryRow(
		`SELECT COUNT(1) FROM pop3_seen_uids WHERE account_id = ? AND uidl = ?`,
		accountID, uidl,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// MarkSeenUID 记录一个已处理过的 UIDL。重复标记同一个 UIDL 是幂等的。
func MarkSeenUID(db *sql.DB, accountID int64, uidl string) error {
	_, err := db.Exec(
		`INSERT INTO pop3_seen_uids (account_id, uidl, seen_at) VALUES (?, ?, ?)
		 ON CONFLICT(account_id, uidl) DO NOTHING`,
		accountID, uidl, time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

// CountSeenUIDs 统计某账号已处理过的 UIDL 数量，用于展示 POP3 账号的同步进度。
func CountSeenUIDs(db *sql.DB, accountID int64) (int, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(1) FROM pop3_seen_uids WHERE account_id = ?`, accountID).Scan(&count)
	return count, err
}

// PruneSeenUIDs 删除某账号早于 olderThan 的已见 UIDL 记录，避免该表无限增长。
func PruneSeenUIDs(db *sql.DB, accountID int64, olderThan time.Time) error {
	_, err := db.Exec(
		`DELETE FROM pop3_seen_uids WHERE account_id = ? AND seen_at < ?`,
		accountID, olderThan.UTC().Format(time.RFC3339),
	)
	return err
}
