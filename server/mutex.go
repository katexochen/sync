package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"github.com/katexochen/sync/internal/memstore"
)

type mutexManager struct {
	mutexes *memstore.Store[string, *sync.Mutex]
	log     *slog.Logger
}

func newMutexManager(log *slog.Logger) *mutexManager {
	return &mutexManager{
		mutexes: memstore.New[string, *sync.Mutex](),
		log:     log.WithGroup("mutexManager"),
	}
}

func (s *mutexManager) registerHandlers(mux *http.ServeMux, prefix string) {
	mux.HandleFunc(prefix+"/new", s.new)
	mux.HandleFunc(prefix+"/{uuid}/lock", s.lock)
	mux.HandleFunc(prefix+"/{uuid}/unlock", s.unlock)
	mux.HandleFunc(prefix+"/{uuid}/delete", s.delete)
}

func (s *mutexManager) new(w http.ResponseWriter, r *http.Request) {
	uuid := uuid.New().String()
	slog.Info("new called", "uuid", uuid)
	s.mutexes.Put(uuid, &sync.Mutex{})
	resp := newMutexResponse{UUID: uuid}
	writeJSON(w, resp)
}

func (s *mutexManager) lock(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	slog.Info("lock called", "uuid", uuid)
	m, ok := s.mutexes.Get(uuid)
	if !ok {
		slog.Warn("lock: not found", "uuid", uuid)
		http.Error(w, "mutex not found", http.StatusNotFound)
		return
	}
	m.Lock()
	slog.Info("locked", "uuid", uuid)
}

func (s *mutexManager) unlock(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	slog.Info("unlock called", "uuid", uuid)
	m, ok := s.mutexes.Get(uuid)
	if !ok {
		slog.Warn("lock: not found", "uuid", uuid)
		http.Error(w, "mutex not found", http.StatusNotFound)
		return
	}

	m.Unlock()
	slog.Info("unlocked", "uuid", uuid)
}

func (s *mutexManager) delete(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	s.mutexes.Delete(uuid)
	slog.Info("deleted", "uuid", uuid)
}

type (
	newMutexResponse struct {
		UUID string `json:uuid`
	}
)

func writeJSON(w http.ResponseWriter, resp any) {
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
