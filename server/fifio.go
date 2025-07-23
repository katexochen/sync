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
	UUID                 uuidlib.UUID `gorm:"type:uuid;primaryKey"`
	CreatedAt            time.Time
	UpdatedAt            *time.Time
	WaitTimeout          time.Duration
	AcceptTimeout        time.Duration
	DoneTimeout          time.Duration
	UnusedDestroyTimeout time.Duration
}

type ticket struct {
	UUID       uuidlib.UUID `gorm:"type:uuid;primaryKey"`
	CreatedAt  time.Time
	NotifiedAt *time.Time
	AcceptedAt *time.Time
	FifoUUID   uuidlib.UUID `gorm:"type:uuid;not null"`
	Fifo       *fifo        `gorm:"foreignKey:FifoUUID;references:UUID;constraint:OnDelete:CASCADE"`
}

func (t *ticket) AfterCreate(tx *gorm.DB) (err error) {
	return nil
}

type fifoManager struct {
	log        *slog.Logger
	db         *gorm.DB
	waiters    map[uuidlib.UUID]chan struct{}
	waitersMux sync.RWMutex
}

func (m *fifoManager) updateTicketQueue(fifoUUID uuidlib.UUID) error {
	return m.db.Transaction(func(tx *gorm.DB) error {
		tickets := make([]ticket, 0, 2)
		if err := m.db.Order("created_at ASC").
			Where(&ticket{FifoUUID: fifoUUID}, "FifoUUID", "DoneAt").
			Limit(2).
			Preload("Fifo").
			Find(&tickets).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("no active ticket found for fifo %s", fifoUUID.String())
		} else if err != nil {
			m.log.Error("db query failed", "err", err)
		}
		if len(tickets) == 0 {
			return nil
		}
		if tickets[0].NotifiedAt != nil && time.Now().After(tickets[0].NotifiedAt.Add(tickets[0].Fifo.AcceptTimeout)) {
			m.removeWaiter(tickets[0].UUID)
			if err := m.db.Delete(&tickets[0]).Error; err != nil {
				m.log.Error("db delete failed", "err", err)
				return fmt.Errorf("db delete failed: %w", err)
			}
			tickets = tickets[1:]
		}
		if tickets[0].NotifiedAt == nil {
			tickets[0].NotifiedAt = toPtr(time.Now())
			if err := m.db.Save(&tickets[0]).Error; err != nil {
				m.log.Error("db save failed", "err", err)
				return fmt.Errorf("db save failed: %w", err)
			}
		}
		if waitC, ok := m.getWaiter(tickets[0].UUID); ok {
			close(waitC)
			m.removeWaiter(tickets[0].UUID)
		}
		return nil
	})
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
						// delete(m.waiters, t.UUID)
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
		log:     log,
		db:      db,
		waiters: make(map[uuidlib.UUID]chan struct{}),
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

	fifo := &fifo{
		UUID:                 uuid,
		WaitTimeout:          time.Minute,
		DoneTimeout:          10 * time.Minute,
		AcceptTimeout:        1 * time.Minute,
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

	tick := &ticket{
		UUID:     uuidlib.New(),
		FifoUUID: fifoUUID,
	}

	if err := s.db.Create(tick).Error; err != nil {
		log.Error("db create failed", "err", err)
		http.Error(w, "db create failed", http.StatusInternalServerError)
		return
	}
	if err := s.updateTicketQueue(fifoUUID); err != nil {
		log.Error("get active ticket failed", "err", err)
		http.Error(w, "get active ticket failed", http.StatusInternalServerError)
		return
	}

	log.Info("ticket created", "ticket", tick.UUID.String())
	encode(w, 200, api.FifoTicketResponse{TicketID: tick.UUID})
}

func (s *fifoManager) wait(w http.ResponseWriter, r *http.Request) {
	fifoUUIDStr := r.PathValue("uuid") // TODO: we don't actually need the fifo UUID here
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

	// If the ticket is already notified, we don't need to wait
	if tick.NotifiedAt != nil {
		log.Info("ticket already notified")
		return
	}

	waitC := s.getOrCreateWaiter(tick.UUID)

	if err := s.updateTicketQueue(tick.FifoUUID); err != nil {
		log.Error("get active ticket failed", "err", err)
		http.Error(w, "get active ticket failed", http.StatusInternalServerError)
		return
	}

	<-waitC

	now := time.Now()
	tick.AcceptedAt = &now
	rowsAffected, err := gorm.G[ticket](s.db).
		Where("accepted_at = ?", nil).
		Select("AcceptedAt").
		Updates(r.Context(), *tick)
	if err != nil {
		log.Error("updating accepted_at failed", "err", err)
		http.Error(w, "updating accepted_at failed", http.StatusInternalServerError)
		return
	}
	if rowsAffected == 0 {
		log.Info("ticket was already accepted")
	} else {
		log.Info("ticket accepted")
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

	tick := &ticket{UUID: tickUUID}
	if err := s.db.Preload("Fifo").First(tick).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		log.Warn("ticket not found")
		http.Error(w, "ticket not found", http.StatusNotFound)
		return
	} else if err != nil {
		log.Warn("db query failed", "err", err)
		http.Error(w, "db query failed", http.StatusInternalServerError)
		return
	}

	if err := s.db.Delete(tick).Error; err != nil {
		log.Error("db delete failed", "err", err)
		http.Error(w, "db delete failed", http.StatusInternalServerError)
		return
	}
	log.Info("ticket deleted")
	if err := s.updateTicketQueue(tick.FifoUUID); err != nil {
		log.Error("get active ticket failed", "err", err)
		http.Error(w, "get active ticket failed", http.StatusInternalServerError)
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
