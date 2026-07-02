package db

import "database/sql"

// MailState 记录某账号某文件夹的 IMAP UID 游标。
type MailState struct {
	AccountID   int64
	Folder      string
	UIDValidity uint32
	LastUID     uint32
}

// GetMailState 读取指定账号+文件夹的游标，不存在时返回零值状态（不报错）。
func GetMailState(db *sql.DB, accountID int64, folder string) (*MailState, error) {
	row := db.QueryRow(
		`SELECT account_id, folder, uid_validity, last_uid FROM mail_state WHERE account_id = ? AND folder = ?`,
		accountID, folder,
	)
	var s MailState
	err := row.Scan(&s.AccountID, &s.Folder, &s.UIDValidity, &s.LastUID)
	if err == sql.ErrNoRows {
		return &MailState{AccountID: accountID, Folder: folder}, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// SaveMailState 插入或更新指定账号+文件夹的游标。
func SaveMailState(db *sql.DB, s *MailState) error {
	_, err := db.Exec(
		`INSERT INTO mail_state (account_id, folder, uid_validity, last_uid) VALUES (?, ?, ?, ?)
		 ON CONFLICT(account_id, folder) DO UPDATE SET uid_validity = excluded.uid_validity, last_uid = excluded.last_uid`,
		s.AccountID, s.Folder, s.UIDValidity, s.LastUID,
	)
	return err
}
