package main

import (
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
	log := s.log.WithGroup("new").With("uuid", uuid)
	log.Info("called")
	s.mutexes.Put(uuid, &mutex{})
	encode(w, 200, newMutexResponse{UUID: uuid})
}

func (s *mutexManager) lock(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	log := s.log.WithGroup("lock").With("uuid", uuid)
	log.Info("called")

	m, ok := s.mutexes.Get(uuid)
	if !ok {
		slog.Warn("not found")
		http.Error(w, "mutex not found", http.StatusNotFound)
		return
	}

	nonce := newNonce()
	m.Lock()
	m.nonce = nonce
	log.Info("locked", "nonce", nonce)
	encode(w, 200, lockMutexResponse{Nonce: nonce})
}

func (s *mutexManager) unlock(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	nonce := r.PathValue("nonce")
	log := s.log.WithGroup("unlock").With("uuid", uuid, "nonce", nonce)
	log.Info("called")

	m, ok := s.mutexes.Get(uuid)
	if !ok {
		log.Warn("not found")
		http.Error(w, "mutex not found", http.StatusNotFound)
		return
	}

	if m.nonce == "" {
		log.Warn("mutex is not locked")
		http.Error(w, "mutex not locked", http.StatusConflict)
		return
	} else if m.nonce != nonce {
		log.Warn("nonce mismatch", "wantNonce", m.nonce)
		http.Error(w, "invalid nonce", http.StatusForbidden)
		return
	}

	m.nonce = ""
	m.Unlock()
	log.Info("unlocked")
}

func (s *mutexManager) delete(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	log := s.log.WithGroup("unlock").With("uuid", uuid)
	s.mutexes.Delete(uuid)
	log.Info("deleted")
}

type (
	newMutexResponse struct {
		UUID string `json:"uuid"`
	}
	lockMutexResponse struct {
		Nonce string `json:"nonce"`
	}
)

func newNonce() string {
	return uuid.New().String()
}
