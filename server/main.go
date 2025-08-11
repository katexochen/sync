package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"

	gormlogger "gorm.io/gorm/logger"
	"k8s.io/utils/clock"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	log.Info("started")

	path := os.Getenv("FIFO_DB_PATH")
	if path == "" {
		path = "state"
	}
	db, err := newSqliteDB(path, gormlogger.Info)
	if err != nil {
		log.Error("fatal", "err", fmt.Errorf("opening sqlite database: %w", err))
		os.Exit(1)
	}

	mux := http.NewServeMux()
	fm, err := newFifoManager(db, clock.RealClock{}, log)
	if err != nil {
		log.Error("fatal", "err", fmt.Errorf("creating fifo manager: %w", err))
		os.Exit(1)
	}
	fm.registerHandlers(mux)

	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}
