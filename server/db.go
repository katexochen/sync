package main

import (
	"fmt"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func newSqliteDB(path string, loglevel gormlogger.LogLevel) (*gorm.DB, error) {
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("opening sqlite database at %s: %w", path, err)
	}
	db.Logger = db.Logger.LogMode(loglevel)

	// Set max open connections to 1, so we don't get "database is locked" errors
	// See https://github.com/go-gorm/gorm/issues/3709#issuecomment-1397782715
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("getting sql.DB from gorm.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)

	// See https://developer.android.com/topic/performance/sqlite-performance-best-practices
	if err := db.Exec("PRAGMA journal_mode = WAL").Error; err != nil {
		return nil, fmt.Errorf("setting journal mode to WAL: %w", err)
	}
	if err := db.Exec("PRAGMA synchronous = NORMAL").Error; err != nil {
		return nil, fmt.Errorf("setting synchronous mode to NORMAL: %w", err)
	}

	// Needed for foreign keys.
	if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}

	return db, nil
}
