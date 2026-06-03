package store

import (
	"context"
	"time"
)

const (
	AvatarSubjectChat   = "chat"
	AvatarSubjectSender = "sender"

	AvatarStatusAvailable    = ""
	AvatarStatusNotSet       = "not_set"
	AvatarStatusUnauthorized = "unauthorized"
	AvatarStatusUnresolved   = "unresolved"
	AvatarStatusTransient    = "transient_error"
)

type AvatarSubject struct {
	Kind string
	ID   string
}

type Avatar struct {
	SubjectKind string
	SubjectID   string
	FetchJID    string
	PictureID   string
	LocalPath   string
	Status      string
	CheckedAt   int64
	UpdatedAt   int64
	NextCheckAt int64
	RetryCount  int32
	LastError   string
	Fetching    bool
}

func (db *DB) EnsureAvatar(ctx context.Context, subject AvatarSubject, fetchJID string) (Avatar, error) {
	if subject.Kind == "" || subject.ID == "" {
		return Avatar{}, nil
	}
	_, err := db.conn.ExecContext(ctx, `
		INSERT INTO avatars (subject_kind, subject_id, fetch_jid)
		VALUES (?, ?, ?)
		ON CONFLICT(subject_kind, subject_id) DO UPDATE SET
			fetch_jid = CASE WHEN excluded.fetch_jid != '' THEN excluded.fetch_jid ELSE avatars.fetch_jid END,
			status = CASE WHEN excluded.fetch_jid != '' AND avatars.status = 'unresolved' THEN '' ELSE avatars.status END,
			next_check_at = CASE WHEN excluded.fetch_jid != '' AND avatars.status = 'unresolved' THEN 0 ELSE avatars.next_check_at END
	`, subject.Kind, subject.ID, fetchJID)
	if err != nil {
		return Avatar{}, err
	}
	return db.GetAvatar(ctx, subject)
}

func (db *DB) GetAvatar(ctx context.Context, subject AvatarSubject) (Avatar, error) {
	var avatar Avatar
	err := db.conn.QueryRowContext(ctx, `
		SELECT subject_kind, subject_id, fetch_jid, picture_id, local_path, status, checked_at, updated_at, next_check_at, retry_count, last_error
		FROM avatars
		WHERE subject_kind = ? AND subject_id = ?
	`, subject.Kind, subject.ID).Scan(
		&avatar.SubjectKind,
		&avatar.SubjectID,
		&avatar.FetchJID,
		&avatar.PictureID,
		&avatar.LocalPath,
		&avatar.Status,
		&avatar.CheckedAt,
		&avatar.UpdatedAt,
		&avatar.NextCheckAt,
		&avatar.RetryCount,
		&avatar.LastError,
	)
	return avatar, err
}

func (db *DB) ListAvatars(ctx context.Context, subjects []AvatarSubject) ([]Avatar, error) {
	avatars := make([]Avatar, 0, len(subjects))
	for _, subject := range subjects {
		avatar, err := db.GetAvatar(ctx, subject)
		if err != nil {
			return nil, err
		}
		avatars = append(avatars, avatar)
	}
	return avatars, nil
}

// ListChatAvatarSubjects returns chat avatar subjects ordered the same way as
// ListChats (pinned first, then most recent), limited to the given count. It is
// used to bound the post-history-sync profile-picture prefetch to the chats that
// are actually visible on the chat list; the rest load lazily on demand.
func (db *DB) ListChatAvatarSubjects(ctx context.Context, limit int) ([]AvatarSubject, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.conn.QueryContext(ctx, `
		SELECT id FROM chats
		WHERE id != ''
		ORDER BY CASE WHEN is_pinned != 0 THEN 0 ELSE 1 END, pinned_order DESC, last_message_time DESC, id ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	subjects := make([]AvatarSubject, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		subjects = append(subjects, AvatarSubject{Kind: AvatarSubjectChat, ID: id})
	}
	return subjects, rows.Err()
}

func (db *DB) UpdateAvatarAvailable(ctx context.Context, subject AvatarSubject, fetchJID, picID, localPath string, nextCheck time.Time) (Avatar, error) {
	now := time.Now().Unix()
	_, err := db.conn.ExecContext(ctx, `
		UPDATE avatars
		SET fetch_jid = ?, picture_id = ?, local_path = ?, status = '', checked_at = ?, updated_at = ?, next_check_at = ?, retry_count = 0, last_error = ''
		WHERE subject_kind = ? AND subject_id = ?
	`, fetchJID, picID, localPath, now, now, nextCheck.Unix(), subject.Kind, subject.ID)
	if err != nil {
		return Avatar{}, err
	}
	return db.GetAvatar(ctx, subject)
}

func (db *DB) MarkAvatarUnchanged(ctx context.Context, subject AvatarSubject, nextCheck time.Time) (Avatar, error) {
	now := time.Now().Unix()
	_, err := db.conn.ExecContext(ctx, `
		UPDATE avatars
		SET checked_at = ?, next_check_at = ?, retry_count = 0, last_error = ''
		WHERE subject_kind = ? AND subject_id = ?
	`, now, nextCheck.Unix(), subject.Kind, subject.ID)
	if err != nil {
		return Avatar{}, err
	}
	return db.GetAvatar(ctx, subject)
}

func (db *DB) UpdateAvatarStatus(ctx context.Context, subject AvatarSubject, status, lastError string, nextCheck time.Time) (Avatar, error) {
	now := time.Now().Unix()
	_, err := db.conn.ExecContext(ctx, `
		UPDATE avatars
		SET picture_id = '', local_path = '', status = ?, checked_at = ?, updated_at = ?, next_check_at = ?, retry_count = 0, last_error = ?
		WHERE subject_kind = ? AND subject_id = ?
	`, status, now, now, nextCheck.Unix(), lastError, subject.Kind, subject.ID)
	if err != nil {
		return Avatar{}, err
	}
	return db.GetAvatar(ctx, subject)
}

func (db *DB) UpdateAvatarTransientError(ctx context.Context, subject AvatarSubject, lastError string, nextCheck time.Time) (Avatar, error) {
	now := time.Now().Unix()
	_, err := db.conn.ExecContext(ctx, `
		UPDATE avatars
		SET status = ?, checked_at = ?, next_check_at = ?, retry_count = retry_count + 1, last_error = ?
		WHERE subject_kind = ? AND subject_id = ?
	`, AvatarStatusTransient, now, nextCheck.Unix(), lastError, subject.Kind, subject.ID)
	if err != nil {
		return Avatar{}, err
	}
	return db.GetAvatar(ctx, subject)
}

func (db *DB) ClearAvatar(ctx context.Context, subject AvatarSubject, status string, nextCheck time.Time) (Avatar, error) {
	now := time.Now().Unix()
	_, err := db.conn.ExecContext(ctx, `
		UPDATE avatars
		SET picture_id = '', local_path = '', status = ?, checked_at = ?, updated_at = ?, next_check_at = ?, retry_count = 0, last_error = ''
		WHERE subject_kind = ? AND subject_id = ?
	`, status, now, now, nextCheck.Unix(), subject.Kind, subject.ID)
	if err != nil {
		return Avatar{}, err
	}
	return db.GetAvatar(ctx, subject)
}
