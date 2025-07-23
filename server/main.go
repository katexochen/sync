package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"

	gormlogger "gorm.io/gorm/logger"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	log.Info("started")

	db, err := newSqliteDB("state", gormlogger.Info)
	if err != nil {
		log.Error("fatal", "err", fmt.Errorf("opening sqlite database: %w", err))
		os.Exit(1)
	}

	mux := http.NewServeMux()
	fm := newFifoManager(db, log)
	fm.registerHandlers(mux)

	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}
