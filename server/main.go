package main

import (
	"log/slog"
	"net/http"
	"os"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	log.Info("started")

	mux := http.NewServeMux()
	mm := newMutexManager(log)
	mm.registerHandlers(mux, "/mutex")

	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}
