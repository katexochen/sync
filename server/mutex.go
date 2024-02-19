package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	uuid "github.com/google/uuid"
	"github.com/katexochen/sync/internal/memstore"
)

type mutex struct {
	sync.Mutex
	nonce string
}

type mutexManager struct {
	mutexes *memstore.Store[string, *mutex]
	log     *slog.Logger
}

func newMutexManager(log *slog.Logger) *mutexManager {
	return &mutexManager{
		mutexes: memstore.New[string, *mutex](),
		log:     log.WithGroup("mutexManager"),
	}
}

func (s *mutexManager) registerHandlers(mux *http.ServeMux, prefix string) {
	mux.HandleFunc(prefix+"/new", s.new)
	mux.HandleFunc(prefix+"/{uuid}/lock", s.lock)
	mux.HandleFunc(prefix+"/{uuid}/unlock/{nonce}", s.unlock)
	mux.HandleFunc(prefix+"/{uuid}/delete", s.delete)
}

func (s *mutexManager) new(w http.ResponseWriter, r *http.Request) {
	uuid := uuid.New().String()
	s.log.Info("new called", "uuid", uuid)
	s.mutexes.Put(uuid, &mutex{})
	writeJSON(w, newMutexResponse{UUID: uuid})
}

func (s *mutexManager) lock(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	s.log.Info("lock called", "uuid", uuid)

	m, ok := s.mutexes.Get(uuid)
	if !ok {
		slog.Warn("lock: not found", "uuid", uuid)
		http.Error(w, "mutex not found", http.StatusNotFound)
		return
	}

	nonce := newNonce()
	m.Lock()
	m.nonce = nonce
	s.log.Info("locked", "uuid", uuid, "nonce", nonce)
	writeJSON(w, lockMutexResponse{Nonce: nonce})
}

func (s *mutexManager) unlock(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	nonce := r.PathValue("nonce")
	s.log.Info("unlock called", "uuid", uuid, "nonce", nonce)

	m, ok := s.mutexes.Get(uuid)
	if !ok {
		s.log.Warn("unlock: not found", "uuid", uuid)
		http.Error(w, "mutex not found", http.StatusNotFound)
		return
	}

	if m.nonce == "" {
		s.log.Warn("unlock: mutex is not locked", "uuid", uuid)
		http.Error(w, "mutex not locked", http.StatusConflict)
		return
	} else if m.nonce != nonce {
		s.log.Warn("unlock: nonce mismatch", "want", m.nonce, "got", nonce)
		http.Error(w, "invalid nonce", http.StatusForbidden)
		return
	}

	m.nonce = ""
	m.Unlock()
	s.log.Info("unlocked", "uuid", uuid)
}

func (s *mutexManager) delete(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	s.mutexes.Delete(uuid)
	s.log.Info("deleted", "uuid", uuid)
}

type (
	newMutexResponse struct {
		UUID string `json:"uuid"`
	}
	lockMutexResponse struct {
		Nonce string `json:"nonce"`
	}
)

func writeJSON(w http.ResponseWriter, resp any) {
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func newNonce() string {
	return uuid.New().String()
}
