package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	log.Info("started")

	db, err := gorm.Open(sqlite.Open("state"), &gorm.Config{})
	if err != nil {
		log.Error("fatal", "err", fmt.Errorf("open sqlite: %w", err))
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
