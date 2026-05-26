package model

import "time"

type JWTSession struct {
	KeyID        string     `gorm:"primaryKey;type:char(64)" json:"key_id"`
	UserID       uint64     `gorm:"index:idx_jwt_sessions_user_revoked" json:"user_id"`
	IP           string     `gorm:"type:varchar(64)" json:"ip"`
	UAHash       string     `gorm:"type:char(64)" json:"ua_hash"`
	TokenVersion uint64     `json:"token_version"`
	ExpiresAt    time.Time  `gorm:"index" json:"expires_at"`
	RevokedAt    *time.Time `gorm:"index:idx_jwt_sessions_user_revoked" json:"revoked_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	LastUsedAt   time.Time  `json:"last_used_at"`
}

func (JWTSession) TableName() string {
	return "jwt_sessions"
}
