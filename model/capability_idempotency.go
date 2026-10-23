package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CapabilityIdempotency maps an idempotency key to a capability task.
// The unique index is the concurrency guard: only one request can claim a key.
type CapabilityIdempotency struct {
	ID           int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId       int    `json:"user_id" gorm:"uniqueIndex:idx_capability_idempotency,priority:1"`
	CapabilityId string `json:"capability_id" gorm:"size:64;uniqueIndex:idx_capability_idempotency,priority:2"`
	KeyHash      string `json:"-" gorm:"size:64;uniqueIndex:idx_capability_idempotency,priority:3"`
	TaskId       string `json:"task_id" gorm:"size:64;index"`
	ClaimedAt    int64  `json:"claimed_at" gorm:"column:claimed_at"`
}

func (CapabilityIdempotency) TableName() string {
	return "capability_idempotencies"
}

const capabilityIdempotencyStaleSeconds = 300

func GetCapabilityIdempotency(userId int, capabilityId string, keyHash string) (*CapabilityIdempotency, error) {
	var row CapabilityIdempotency
	err := DB.Where("user_id = ? AND capability_id = ? AND key_hash = ?", userId, capabilityId, keyHash).
		First(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ClaimCapabilityIdempotency returns claimed=true when this caller created the
// claim. If another request already claimed the key, it returns that row.
// A claim with no task id older than five minutes is considered stale and can
// be reclaimed, so a crashed submit does not block the key forever.
func ClaimCapabilityIdempotency(userId int, capabilityId string, keyHash string) (*CapabilityIdempotency, bool, error) {
	for attempt := 0; attempt < 2; attempt++ {
		row := CapabilityIdempotency{
			UserId:       userId,
			CapabilityId: capabilityId,
			KeyHash:      keyHash,
			ClaimedAt:    common.GetTimestamp(),
		}
		result := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if result.Error != nil {
			return nil, false, result.Error
		}
		if result.RowsAffected > 0 {
			return &row, true, nil
		}

		existing, lookupErr := GetCapabilityIdempotency(userId, capabilityId, keyHash)
		if lookupErr == nil {
			if existing.TaskId == "" &&
				common.GetTimestamp()-existing.ClaimedAt > capabilityIdempotencyStaleSeconds {
				// Stale claim: delete and retry the insert once.
				if err := DB.Delete(&CapabilityIdempotency{}, existing.ID).Error; err != nil {
					return nil, false, err
				}
				continue
			}
			return existing, false, nil
		}
		if errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return nil, false, lookupErr
		}
		return nil, false, lookupErr
	}
	return nil, false, errors.New("failed to claim capability idempotency")
}

func SetCapabilityIdempotencyTask(userId int, capabilityId string, keyHash string, taskId string) error {
	return DB.Model(&CapabilityIdempotency{}).
		Where("user_id = ? AND capability_id = ? AND key_hash = ?", userId, capabilityId, keyHash).
		Update("task_id", taskId).Error
}

func DeleteCapabilityIdempotency(userId int, capabilityId string, keyHash string) error {
	return DB.Where("user_id = ? AND capability_id = ? AND key_hash = ?", userId, capabilityId, keyHash).
		Delete(&CapabilityIdempotency{}).Error
}
