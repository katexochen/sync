package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	uuidlib "github.com/google/uuid"
	"github.com/katexochen/sync/api"
	"gorm.io/gorm"
)

type fifo struct {
	UUID                 uuidlib.UUID `gorm:"type:uuid;primary_key;"`
	CreatedAt            time.Time
	UpdatedAt            *time.Time
	WaitTimeout          time.Duration
	DoneTimeout          time.Duration
	UnusedDestroyTimeout time.Duration
}

type ticket struct {
	UUID       uuidlib.UUID `gorm:"type:uuid;primary_key;"`
	CreatedAt  time.Time
	NotifiedAt *time.Time
	DoneAt     *time.Time
	Fifo       *fifo `gorm:"foreignKey:UUID;references:UUID"`
}

type fifoManager struct {
	log        *slog.Logger
	db         *gorm.DB
	waiters    map[uuidlib.UUID]chan struct{}
	waitersMux sync.RWMutex
}

func (m *fifoManager) addWaiter(uuid uuidlib.UUID) chan struct{} {
	m.waitersMux.Lock()
	defer m.waitersMux.Unlock()
	waitC := make(chan struct{})
	m.waiters[uuid] = waitC
	return waitC
}

func (m *fifoManager) removeWaiter(uuid uuidlib.UUID) {
	m.waitersMux.Lock()
	defer m.waitersMux.Unlock()
	delete(m.waiters, uuid)
}

func (m *fifoManager) getWaiter(uuid uuidlib.UUID) (chan struct{}, bool) {
	m.waitersMux.RLock()
	defer m.waitersMux.RUnlock()
	waitC, ok := m.waiters[uuid]
	return waitC, ok
}

func (m *fifoManager) getOrCreateWaiter(uuid uuidlib.UUID) chan struct{} {
	waitC, ok := m.getWaiter(uuid)
	if !ok {
		waitC = m.addWaiter(uuid)
	}
	return waitC
}

func (m *fifoManager) run() {
	go func() {
		for {
			select {
			case <-time.After(time.Second):
			}
			// get all tickets that are neither done nor notified
			var openTickets []ticket
			if err := m.db.Where("done_at IS NULL AND notified_at IS NULL").Find(&openTickets).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			} else if err != nil {
				m.log.Error("db query failed", "err", err)
				continue
			}
			for _, t := range openTickets {
				if time.Now().After(t.CreatedAt.Add(t.Fifo.WaitTimeout)) {
					waitC, ok := m.waiters[t.UUID]
					if ok {
						close(waitC)
						delete(m.waiters, t.UUID)
					}
				}
			}
		}
	}()
}

func newFifoManager(db *gorm.DB, log *slog.Logger) *fifoManager {
	db.AutoMigrate(
		&fifo{},
		&ticket{},
	)
	return &fifoManager{
		log: log,
		db:  db,
	}
}

func (s *fifoManager) registerHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/fifo/new", s.new)
	mux.HandleFunc("/fifo/{uuid}/ticket", s.ticket)
	mux.HandleFunc("/fifo/{uuid}/wait/{ticket}", s.wait)
	mux.HandleFunc("/fifo/{uuid}/done/{ticket}", s.done)
}

func (s *fifoManager) new(w http.ResponseWriter, r *http.Request) {
	uuid := uuidlib.New()
	log := s.log.With("call", "new", "uuid", uuid.String())
	log.Info("called")

	now := time.Now()
	fifo := &fifo{
		UUID:                 uuid,
		CreatedAt:            now,
		UpdatedAt:            &now,
		WaitTimeout:          time.Minute,
		DoneTimeout:          10 * time.Minute,
		UnusedDestroyTimeout: 30 * 24 * time.Hour,
	}
	res := s.db.Create(fifo)
	if res.Error != nil {
		log.Error("db create failed", "err", res.Error)
		http.Error(w, "db create failed", http.StatusInternalServerError)
		return
	}

	encode(w, 200, api.FifoNewResponse{UUID: fifo.UUID})
}

func (s *fifoManager) ticket(w http.ResponseWriter, r *http.Request) {
	fifoUUIDStr := r.PathValue("uuid")
	log := s.log.With("call", "ticket", "fifo", fifoUUIDStr)
	log.Info("called")

	fifoUUID, err := uuidlib.Parse(fifoUUIDStr)
	if err != nil {
		log.Warn("invalid uuid", "err", err)
		http.Error(w, "invalid uuid", http.StatusBadRequest)
		return
	}

	fifo := &fifo{UUID: fifoUUID}
	if err := s.db.First(fifo).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		log.Warn("fifo not found")
		http.Error(w, "fifo not found", http.StatusNotFound)
		return
	} else if err != nil {
		log.Warn("db query failed", "err", err)
		http.Error(w, "db query failed", http.StatusInternalServerError)
		return
	}

	now := time.Now()
	tick := &ticket{
		UUID:      uuidlib.New(),
		CreatedAt: now,
		Fifo:      fifo,
	}

	if err := s.db.Create(tick).Error; err != nil {
		log.Error("db create failed", "err", err)
		http.Error(w, "db create failed", http.StatusInternalServerError)
		return
	}

	fifo.UpdatedAt = &now
	// TODO: check if value was updated after last request, only update if it was not
	if err := s.db.Save(fifo).Error; err != nil {
		log.Warn("db save failed", "err", err)
		http.Error(w, "db save failed", http.StatusInternalServerError)
		return
	}

	log.Info("ticket created", "ticket", tick.UUID.String())
	encode(w, 200, api.FifoTicketResponse{TicketID: tick.UUID})
}

func (s *fifoManager) wait(w http.ResponseWriter, r *http.Request) {
	fifoUUIDStr := r.PathValue("uuid")
	tickUUIDStr := r.PathValue("ticket")
	log := s.log.With("call", "wait", "fifo", fifoUUIDStr, "ticket", tickUUIDStr)
	log.Info("called")

	tickUUID, err := uuidlib.Parse(tickUUIDStr)
	if err != nil {
		log.Warn("invalid ticket uuid", "err", err)
		http.Error(w, "invalid ticket uuid", http.StatusBadRequest)
		return
	}

	tick := &ticket{UUID: tickUUID}
	if err := s.db.First(tick).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		log.Warn("ticket not found")
		http.Error(w, "ticket not found", http.StatusNotFound)
		return
	} else if err != nil {
		log.Warn("db query failed", "err", err)
		http.Error(w, "db query failed", http.StatusInternalServerError)
		return
	}
	log.Info("found ticket")

	if tick.DoneAt != nil {
		log.Info("ticket already done")
		http.Error(w, "ticket already done", http.StatusConflict)
		return
	}

	waitC := s.getOrCreateWaiter(tick.UUID)
	<-waitC

	now := time.Now()
	tick.NotifiedAt = &now
	if err := s.db.Save(tick).Error; err != nil {
		log.Error("db save failed", "err", err)
		http.Error(w, "db save failed", http.StatusInternalServerError)
		return
	}
}

func (s *fifoManager) done(w http.ResponseWriter, r *http.Request) {
	fifoUUIDStr := r.PathValue("uuid")
	tickUUIDStr := r.PathValue("ticket")
	log := s.log.With("call", "done", "fifo", fifoUUIDStr, "ticket", tickUUIDStr)
	log.Info("called")

	tickUUID, err := uuidlib.Parse(tickUUIDStr)
	if err != nil {
		log.Warn("invalid ticket uuid", "err", err)
		http.Error(w, "invalid ticket uuid", http.StatusBadRequest)
		return
	}

	tick := &ticket{
		UUID: tickUUID,
	}
	if err := s.db.First(tick).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		log.Warn("ticket not found")
		http.Error(w, "ticket not found", http.StatusNotFound)
		return
	} else if err != nil {
		log.Warn("db query failed", "err", err)
		http.Error(w, "db query failed", http.StatusInternalServerError)
		return
	}

	now := time.Now()
	tick.DoneAt = &now
	if err := s.db.Save(tick).Error; err != nil {
		log.Error("db save failed", "err", err)
		http.Error(w, "db save failed", http.StatusInternalServerError)
		return
	}
}

func encode[T any](w http.ResponseWriter, status int, v T) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return nil
}

func toPtr[T any](v T) *T {
	return &v
}
