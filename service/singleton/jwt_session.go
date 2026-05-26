package singleton

import (
	"log"
	"time"

	"github.com/nezhahq/nezha/model"
)

const (
	JWTSessionGCSchedule       = "@every 10m"
	JWTSessionRevokedRetention = 24 * time.Hour
	JWTSessionExpiredGrace     = 1 * time.Hour
)

func StartJWTSessionGC() error {
	if _, err := CronShared.AddFunc(JWTSessionGCSchedule, RunJWTSessionGC); err != nil {
		return err
	}
	RunJWTSessionGC()
	return nil
}

func RunJWTSessionGC() {
	if DB == nil {
		return
	}
	now := time.Now()

	if err := DB.
		Where("expires_at < ?", now.Add(-JWTSessionExpiredGrace)).
		Delete(&model.JWTSession{}).Error; err != nil {
		log.Printf("NEZHA>> JWTSession GC delete expired failed: %v", err)
	}

	if err := DB.
		Where("revoked_at IS NOT NULL AND revoked_at < ?", now.Add(-JWTSessionRevokedRetention)).
		Delete(&model.JWTSession{}).Error; err != nil {
		log.Printf("NEZHA>> JWTSession GC delete revoked failed: %v", err)
	}
}

func RevokeJWTSession(keyID string) error {
	now := time.Now()
	return DB.Model(&model.JWTSession{}).
		Where("key_id = ? AND revoked_at IS NULL", keyID).
		Update("revoked_at", &now).Error
}

func RevokeJWTSessionsByUser(userID uint64) error {
	now := time.Now()
	return DB.Model(&model.JWTSession{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", &now).Error
}
