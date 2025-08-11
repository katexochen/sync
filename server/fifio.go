package main

import (
	"context"
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
	"k8s.io/utils/clock"
)

type fifo struct {
	UUID                 uuidlib.UUID `gorm:"type:uuid;primaryKey"`
	CreatedAt            time.Time
	UpdatedAt            time.Time
	WaitTimeout          time.Duration
	AcceptTimeout        time.Duration
	DoneTimeout          time.Duration
	UnusedDestroyTimeout time.Duration
	AllowOverrides       bool
}

type ticket struct {
	UUID          uuidlib.UUID `gorm:"type:uuid;primaryKey"`
	CreatedAt     time.Time
	NotifiedAt    *time.Time
	AcceptedAt    *time.Time
	WaitTimeout   time.Duration
	AcceptTimeout time.Duration
	DoneTimeout   time.Duration
	FifoUUID      uuidlib.UUID `gorm:"type:uuid;not null"`
	Fifo          *fifo        `gorm:"foreignKey:FifoUUID;references:UUID;constraint:OnDelete:CASCADE"`
}

type fifoManager struct {
	log          *slog.Logger
	db           *gorm.DB
	waiters      map[uuidlib.UUID]chan struct{}
	waitersMux   sync.RWMutex
	clock        clock.WithDelayedExecution
	notifyCh     chan uuidlib.UUID
	notifiers    map[uuidlib.UUID]struct{}
	notifiersMux sync.RWMutex
	pullRate     time.Duration
}

func (m *fifoManager) updateFifo(tx *gorm.DB, fifoUUID uuidlib.UUID) error {
	fifo := &fifo{UUID: fifoUUID}
	if err := tx.First(fifo).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("fifo %s not found", fifoUUID.String())
	} else if err != nil {
		m.log.Error("db query failed", "err", err)
		return fmt.Errorf("db query failed: %w", err)
	}
	// Mark the fifo as updated to prevent it from being deleted
	fifo.UpdatedAt = m.clock.Now()
	if err := tx.Select("UpdatedAt").Updates(&fifo).Error; err != nil {
		m.log.Error("db update failed", "err", err)
		return fmt.Errorf("db update failed: %w", err)
	}
	return nil
}

func (m *fifoManager) checkTimeouts(t ticket) error {
	if t.NotifiedAt != nil && t.AcceptedAt == nil && m.clock.Now().After(t.NotifiedAt.Add(t.AcceptTimeout)) {
		m.log.Warn("ticket was not accepted in time", "ticket", t.UUID.String())
		return fmt.Errorf("ticket %s was not accepted in time", t.UUID.String())
	}
	if t.AcceptedAt != nil && m.clock.Now().After(t.AcceptedAt.Add(t.DoneTimeout)) {
		m.log.Warn("ticket was not marked as done in time", "ticket", t.UUID.String())
		return fmt.Errorf("ticket %s was not marked as done in time", t.UUID.String())
	}
	return nil
}

func (m *fifoManager) updateTicketQueue(fifoUUID uuidlib.UUID) error {
	return m.db.Transaction(func(tx *gorm.DB) error {
		// Update the fifo to mark it as used
		if err := m.updateFifo(tx, fifoUUID); err != nil {
			m.log.Error("updating fifo failed", "fifo", fifoUUID.String(), "err", err)
			return fmt.Errorf("updating fifo failed: %w", err)
		}
		// Get the first two tickets in the queue for the fifo
		tickets := make([]ticket, 0, 2)
		if err := tx.Order("created_at ASC").
			Where(&ticket{FifoUUID: fifoUUID}, "FifoUUID", "DoneAt").
			Limit(2).
			Find(&tickets).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("no active ticket found for fifo %s", fifoUUID.String())
		} else if err != nil {
			m.log.Error("db query failed", "err", err)
		}
		// The ticket queue is empty
		if len(tickets) == 0 {
			return nil
		}
		if err := m.checkTimeouts(tickets[0]); err != nil {
			if err := tx.Delete(&tickets[0]).Error; err != nil {
				m.log.Error("db delete failed", "err", err)
				return fmt.Errorf("db delete failed: %w", err)
			}
			// Ensure late wait calls are notified
			if waitC, ok := m.getWaiter(tickets[0].UUID); ok {
				close(waitC)
				m.removeWaiter(tickets[0].UUID)
			}
			tickets = tickets[1:]
		}
		if len(tickets) == 0 {
			return nil
		}
		// If there is no active ticket, we notify the first one in the queue
		if tickets[0].NotifiedAt == nil {
			tickets[0].NotifiedAt = toPtr(m.clock.Now())
			if err := tx.Select("NotifiedAt").Updates(&tickets[0]).Error; err != nil {
				m.log.Error("db save failed", "err", err)
				return fmt.Errorf("db save failed: %w", err)
			}
		}
		if tickets[0].AcceptedAt == nil {
			m.notifyOnce(tickets[0].UUID, tickets[0].NotifiedAt.Add(tickets[0].AcceptTimeout))
		} else {
			m.notifyOnce(tickets[0].UUID, tickets[0].NotifiedAt.Add(tickets[0].WaitTimeout))
		}
		// In any case, ensure we notify the waiters for the first ticket
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

func (m *fifoManager) notifyOnce(uuid uuidlib.UUID, t time.Time) {
	m.notifiersMux.Lock()
	defer m.notifiersMux.Unlock()
	if _, ok := m.notifiers[uuid]; ok {
		return
	}
	m.notifiers[uuid] = struct{}{}
	m.clock.AfterFunc(-m.clock.Since(t), func() {
		m.log.Debug("ticket timeout reached", "ticket", uuid.String())
		m.notifyCh <- uuid
	})
}

func (m *fifoManager) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			m.log.Info("fifo manager stopped")
			return
		case uuid := <-m.notifyCh:
			m.notifiersMux.Lock()
			delete(m.notifiers, uuid)
			m.notifiersMux.Unlock()
			m.log.Debug("checking ticket timeout", "ticket", uuid.String())
			tick := &ticket{UUID: uuid}
			if err := m.db.First(tick).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			} else if err != nil {
				m.log.Error("db query failed", "err", err)
				continue
			}
			if err := m.checkTimeouts(*tick); err != nil {
				if err := m.updateTicketQueue(tick.FifoUUID); err != nil {
					m.log.Error("updating ticket queue failed", "fifo", tick.FifoUUID.String(), "err", err)
				}
			}
		case <-m.clock.After(m.pullRate):
			m.log.Debug("checking for unused fifos")
			var fifos []fifo
			if err := m.db.Find(&fifos).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			} else if err != nil {
				m.log.Error("db query failed", "err", err)
				continue
			}
			for _, fifo := range fifos {
				if m.clock.Now().After(fifo.UpdatedAt.Add(fifo.UnusedDestroyTimeout)) {
					m.log.Info("deleting unused fifo", "uuid", fifo.UUID.String())
					if err := m.db.Delete(&fifo).Error; err != nil {
						m.log.Error("db delete failed", "err", err)
					}
				}
			}
		}
	}
}

func newFifoManager(db *gorm.DB, clock clock.WithDelayedExecution, log *slog.Logger) *fifoManager {
	db.AutoMigrate(
		&fifo{},
		&ticket{},
	)
	fm := &fifoManager{
		log:       log,
		db:        db,
		waiters:   make(map[uuidlib.UUID]chan struct{}),
		clock:     clock,
		notifyCh:  make(chan uuidlib.UUID, 100),
		notifiers: make(map[uuidlib.UUID]struct{}),
		pullRate:  5 * time.Minute,
	}
	go fm.run(context.Background())
	return fm
}

func (m *fifoManager) registerHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/fifo/new", m.new)
	mux.HandleFunc("/fifo/{uuid}/ticket", m.ticket)
	mux.HandleFunc("/fifo/{uuid}/wait/{ticket}", m.wait)
	mux.HandleFunc("/fifo/{uuid}/done/{ticket}", m.done)
}

func (m *fifoManager) new(w http.ResponseWriter, r *http.Request) {
	uuid := uuidlib.New()
	log := m.log.With("call", "new", "uuid", uuid.String())
	log.Info("called")

	fifo := &fifo{
		UUID:                 uuid,
		WaitTimeout:          6 * time.Hour,
		AcceptTimeout:        1 * time.Minute,
		DoneTimeout:          10 * time.Minute,
		UnusedDestroyTimeout: 30 * 24 * time.Hour,
		AllowOverrides:       false,
	}

	if r.FormValue("wait_timeout") != "" {
		waitTimeout, err := time.ParseDuration(r.FormValue("wait_timeout"))
		if err != nil {
			log.Warn("invalid wait timeout", "err", err)
			http.Error(w, "invalid wait timeout", http.StatusBadRequest)
			return
		}
		fifo.WaitTimeout = waitTimeout
	}
	if r.FormValue("accept_timeout") != "" {
		acceptTimeout, err := time.ParseDuration(r.FormValue("accept_timeout"))
		if err != nil {
			log.Warn("invalid accept timeout", "err", err)
			http.Error(w, "invalid accept timeout", http.StatusBadRequest)
			return
		}
		fifo.AcceptTimeout = acceptTimeout
	}
	if r.FormValue("done_timeout") != "" {
		doneTimeout, err := time.ParseDuration(r.FormValue("done_timeout"))
		if err != nil {
			log.Warn("invalid done timeout", "err", err)
			http.Error(w, "invalid done timeout", http.StatusBadRequest)
			return
		}
		fifo.DoneTimeout = doneTimeout
	}
	if r.FormValue("unused_destroy_timeout") != "" {
		unusedDestroyTimeout, err := time.ParseDuration(r.FormValue("unused_destroy_timeout"))
		if err != nil {
			log.Warn("invalid unused destroy timeout", "err", err)
			http.Error(w, "invalid unused destroy timeout", http.StatusBadRequest)
			return
		}
		fifo.UnusedDestroyTimeout = unusedDestroyTimeout
	}
	if r.FormValue("allow_overrides") == "true" {
		fifo.AllowOverrides = true
	}

	res := m.db.Create(fifo)
	if res.Error != nil {
		log.Error("db create failed", "err", res.Error)
		http.Error(w, "db create failed", http.StatusInternalServerError)
		return
	}

	encode(w, 200, api.FifoNewResponse{UUID: fifo.UUID})
}

func (m *fifoManager) ticket(w http.ResponseWriter, r *http.Request) {
	fifoUUIDStr := r.PathValue("uuid")
	log := m.log.With("call", "ticket", "fifo", fifoUUIDStr)
	log.Info("called")

	fifoUUID, err := uuidlib.Parse(fifoUUIDStr)
	if err != nil {
		log.Warn("invalid uuid", "err", err)
		http.Error(w, "invalid uuid", http.StatusBadRequest)
		return
	}

	fifo := &fifo{UUID: fifoUUID}
	if err := m.db.First(fifo).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		log.Warn("fifo not found")
		http.Error(w, "fifo not found", http.StatusNotFound)
		return
	} else if err != nil {
		log.Warn("db query failed", "err", err)
		http.Error(w, "db query failed", http.StatusInternalServerError)
		return
	}

	tick := &ticket{
		UUID:          uuidlib.New(),
		FifoUUID:      fifoUUID,
		WaitTimeout:   fifo.WaitTimeout,
		AcceptTimeout: fifo.AcceptTimeout,
		DoneTimeout:   fifo.DoneTimeout,
	}

	m.log.Info("fifo overrides", "allow_overrides", fifo.AllowOverrides)
	if fifo.AllowOverrides {
		if r.FormValue("wait_timeout") != "" {
			waitTimeout, err := time.ParseDuration(r.FormValue("wait_timeout"))
			if err != nil {
				log.Warn("invalid wait timeout", "err", err)
				http.Error(w, "invalid wait timeout", http.StatusBadRequest)
				return
			}
			tick.WaitTimeout = waitTimeout
			m.log.Debug("wait timeout set", "wait_timeout", tick.WaitTimeout)
		}
		if r.FormValue("accept_timeout") != "" {
			acceptTimeout, err := time.ParseDuration(r.FormValue("accept_timeout"))
			if err != nil {
				log.Warn("invalid accept timeout", "err", err)
				http.Error(w, "invalid accept timeout", http.StatusBadRequest)
				return
			}
			tick.AcceptTimeout = acceptTimeout
			m.log.Debug("accept timeout set", "accept_timeout", tick.AcceptTimeout)
		}
		if r.FormValue("done_timeout") != "" {
			doneTimeout, err := time.ParseDuration(r.FormValue("done_timeout"))
			if err != nil {
				log.Warn("invalid done timeout", "err", err)
				http.Error(w, "invalid done timeout", http.StatusBadRequest)
				return
			}
			tick.DoneTimeout = doneTimeout
			m.log.Debug("done timeout set", "done_timeout", tick.DoneTimeout)
		}
	}

	if err := m.db.Create(tick).Error; err != nil {
		log.Error("db create failed", "err", err)
		http.Error(w, "db create failed", http.StatusInternalServerError)
		return
	}
	if err := m.updateTicketQueue(fifoUUID); err != nil {
		log.Error("get active ticket failed", "err", err)
		http.Error(w, "get active ticket failed", http.StatusInternalServerError)
		return
	}

	log.Info("ticket created", "ticket", tick.UUID.String())
	encode(w, 200, api.FifoTicketResponse{TicketID: tick.UUID})
}

func (m *fifoManager) wait(w http.ResponseWriter, r *http.Request) {
	fifoUUIDStr := r.PathValue("uuid")
	tickUUIDStr := r.PathValue("ticket")
	log := m.log.With("call", "wait", "fifo", fifoUUIDStr, "ticket", tickUUIDStr)
	log.Info("called")

	tickUUID, err := uuidlib.Parse(tickUUIDStr)
	if err != nil {
		log.Warn("invalid ticket uuid", "err", err)
		http.Error(w, "invalid ticket uuid", http.StatusBadRequest)
		return
	}

	tick := &ticket{UUID: tickUUID}
	if err := m.db.First(tick).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		log.Warn("ticket not found")
		http.Error(w, "ticket not found", http.StatusNotFound)
		return
	} else if err != nil {
		log.Warn("db query failed", "err", err)
		http.Error(w, "db query failed", http.StatusInternalServerError)
		return
	}
	if tick.FifoUUID.String() != fifoUUIDStr {
		log.Warn("ticket does not belong to fifo", "fifo", fifoUUIDStr, "ticket", tick.FifoUUID.String())
		http.Error(w, "ticket does not belong to fifo", http.StatusBadRequest)
		return
	}
	log.Info("found ticket")

	waitC := m.getOrCreateWaiter(tick.UUID)

	if err := m.updateTicketQueue(tick.FifoUUID); err != nil {
		log.Error("updating ticket queue failed", "err", err)
		http.Error(w, "updating ticket queue failed", http.StatusInternalServerError)
		return
	}

	select {
	case <-m.clock.After(tick.WaitTimeout):
		log.Info("wait timeout reached")
		http.Error(w, "wait timeout reached", http.StatusRequestTimeout)
		return
	case <-waitC:
	}

	if err := m.checkTimeouts(*tick); err != nil {
		// Ticket was not accepted in time
		http.Error(w, err.Error(), http.StatusGone)
		return
	}

	now := m.clock.Now()
	tick.AcceptedAt = &now
	tx := m.db.Where("accepted_at IS NULL").Select("AcceptedAt").Updates(tick)
	if tx.Error != nil {
		log.Error("updating accepted_at failed", "err", err)
		http.Error(w, "updating accepted_at failed", http.StatusInternalServerError)
		return
	} else if tx.RowsAffected == 0 {
		log.Info("ticket was already accepted")
	} else {
		log.Info("ticket accepted")
		m.notifiersMux.Lock()
		delete(m.notifiers, tick.UUID)
		m.notifiersMux.Unlock()
	}
}

func (m *fifoManager) done(w http.ResponseWriter, r *http.Request) {
	fifoUUIDStr := r.PathValue("uuid")
	tickUUIDStr := r.PathValue("ticket")
	log := m.log.With("call", "done", "fifo", fifoUUIDStr, "ticket", tickUUIDStr)
	log.Info("called")

	tickUUID, err := uuidlib.Parse(tickUUIDStr)
	if err != nil {
		log.Warn("invalid ticket uuid", "err", err)
		http.Error(w, "invalid ticket uuid", http.StatusBadRequest)
		return
	}

	tick := &ticket{UUID: tickUUID}
	if err := m.db.First(tick).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		log.Warn("ticket not found")
		http.Error(w, "ticket not found", http.StatusNotFound)
		return
	} else if err != nil {
		log.Warn("db query failed", "err", err)
		http.Error(w, "db query failed", http.StatusInternalServerError)
		return
	}
	if tick.FifoUUID.String() != fifoUUIDStr {
		log.Warn("ticket does not belong to fifo", "fifo", fifoUUIDStr, "ticket", tick.FifoUUID.String())
		http.Error(w, "ticket does not belong to fifo", http.StatusBadRequest)
		return
	}

	m.removeWaiter(tick.UUID)
	if err := m.db.Delete(tick).Error; err != nil {
		log.Error("db delete failed", "err", err)
		http.Error(w, "db delete failed", http.StatusInternalServerError)
		return
	}
	log.Info("ticket deleted")
	if err := m.updateTicketQueue(tick.FifoUUID); err != nil {
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
