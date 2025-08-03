package migrations

import (
	"gorm.io/gorm"
)

type User struct {
	ID string `gorm:"primaryKey"`
}

type SyncProfile struct {
	ID string `gorm:"primaryKey"`
}

type UserConfig struct {
	UserID string `gorm:"primaryKey"`
}

type SyncProfileConfig struct {
	ProfileID string `gorm:"primaryKey"`
}

type SyncState struct {
	UserID string `gorm:"primaryKey"`
}

type ProfileSyncState struct {
	ProfileID string `gorm:"primaryKey"`
}

func RenameUserToSyncProfile(db *gorm.DB) error {
	// Rename tables
	err := db.Exec("ALTER TABLE users RENAME TO sync_profiles").Error
	if err != nil {
		return err
	}

	err = db.Exec("ALTER TABLE user_configs RENAME TO sync_profile_configs").Error
	if err != nil {
		return err
	}

	err = db.Exec("ALTER TABLE sync_states RENAME COLUMN user_id TO profile_id").Error
	if err != nil {
		return err
	}

	// Update foreign key constraints
	err = db.Exec("ALTER TABLE sync_profile_configs RENAME COLUMN user_id TO profile_id").Error
	if err != nil {
		return err
	}

	// Recreate foreign keys with new column names
	err = db.Exec(`
		ALTER TABLE sync_profile_configs 
		DROP CONSTRAINT IF EXISTS fk_user_configs_user,
		ADD CONSTRAINT fk_sync_profile_configs_profile 
		FOREIGN KEY (profile_id) REFERENCES sync_profiles(id) ON DELETE CASCADE
	`).Error
	if err != nil {
		return err
	}

	err = db.Exec(`
		ALTER TABLE sync_states 
		DROP CONSTRAINT IF EXISTS fk_sync_states_user,
		ADD CONSTRAINT fk_sync_states_profile 
		FOREIGN KEY (profile_id) REFERENCES sync_profiles(id) ON DELETE CASCADE
	`).Error

	return err
}
