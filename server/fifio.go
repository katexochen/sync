package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	uuidlib "github.com/google/uuid"
	"github.com/katexochen/sync/api"
	"github.com/katexochen/sync/internal/memstore"
)

type ticket struct {
	api.FifoTicketResponse
	// waitC is closed to notify any waiters that its the ticket's turn.
	waitC chan struct{}
	// waitAckC is closed to notify the fifo that the owner has been notified.
	waitAckC chan struct{}
	// waitAckOnce is used to ensure that waitAckC is closed only once.
	waitAckOnce sync.Once
	// doneC is closed to notify the fifo that the ticket is done.
	doneC chan struct{}
	// doneTimeout is the maximum time to wait for the ticket to be done.
	doneTimeout time.Duration
}

func (t *ticket) waitAck() {
	t.waitAckOnce.Do(func() {
		close(t.waitAckC)
	})
}

func newTicket(doneTimeout time.Duration) *ticket {
	return &ticket{
		FifoTicketResponse: api.FifoTicketResponse{TicketID: uuidlib.New()},
		waitC:              make(chan struct{}),
		waitAckC:           make(chan struct{}),
		doneC:              make(chan struct{}),
		doneTimeout:        doneTimeout,
	}
}

type fifo struct {
	uuid                 uuidlib.UUID
	waitTimeout          time.Duration
	doneTimeout          time.Duration
	unusedDestroyTimeout time.Duration
	ticketLookup         *memstore.Store[string, *ticket]
	ticketQueue          chan *ticket
	log                  *slog.Logger
}

func newFifo(log *slog.Logger) *fifo {
	uuid := uuidlib.New()
	return &fifo{
		uuid:                 uuid,
		waitTimeout:          time.Minute,
		doneTimeout:          10 * time.Minute,
		unusedDestroyTimeout: 30 * 24 * time.Hour,
		ticketLookup:         memstore.New[string, *ticket](),
		ticketQueue:          make(chan *ticket, 300),
		log:                  log.WithGroup("fifo").With("uuid", uuid.String()),
	}
}

func (f *fifo) start() {
	go func() {
		f.log.Info("started")
		for {
			var t *ticket

			f.log.Info("waiting for ticket")
			select {
			case t = <-f.ticketQueue:
				f.log.Info("got ticket", "ticket", t.TicketID)
			case <-time.After(f.unusedDestroyTimeout):
				f.log.Info("unused timeout reached, self destruction")
				// TODO: remove referens in manager
				return
			}

			close(t.waitC) // Boardcast to all waiters.

			// Wait for the acknowledgement from the ticket owner.
			select {
			case <-time.After(f.waitTimeout):
				f.log.Warn("timeout waiting for ticket owner", "ticket", t.TicketID)
				continue
			case <-t.waitAckC:
				f.log.Info("ticket owner notified", "ticket", t.TicketID)
			}

			// Wait for the ticket to be done.
			select {
			case <-time.After(t.doneTimeout):
				f.log.Warn("timeout waiting for ticket completion", "ticket", t.TicketID)
			case <-t.doneC:
				f.log.Info("ticket completed", "ticket", t.TicketID)
			}
			f.ticketLookup.Delete(t.TicketID.String())
		}
	}()
}

type fifoManager struct {
	fifos   *memstore.Store[string, *fifo]
	log     *slog.Logger
	fifoLog *slog.Logger
}

func newFifoManager(log *slog.Logger) *fifoManager {
	return &fifoManager{
		fifos:   memstore.New[string, *fifo](),
		log:     log.WithGroup("fifoManager"),
		fifoLog: log,
	}
}

func (s *fifoManager) registerHandlers(mux *http.ServeMux, prefix string) {
	mux.HandleFunc(prefix+"/new", s.new)
	mux.HandleFunc(prefix+"/{uuid}/ticket", s.ticket)
	mux.HandleFunc(prefix+"/{uuid}/wait/{ticket}", s.wait)
	mux.HandleFunc(prefix+"/{uuid}/done/{ticket}", s.done)
}

func (s *fifoManager) new(w http.ResponseWriter, r *http.Request) {
	fifo := newFifo(s.fifoLog)
	log := s.log.With("call", "new", "uuid", fifo.uuid.String())
	log.Info("called")
	fifo.start()
	s.fifos.Put(fifo.uuid.String(), fifo)
	encode(w, 200, api.FifoNewResponse{UUID: fifo.uuid})
}

func (s *fifoManager) ticket(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	log := s.log.With("call", "ticket", "uuid", uuid)
	log.Info("called")

	fifo, ok := s.fifos.Get(uuid)
	if !ok {
		log.Warn("not found")
		http.Error(w, "fifo not found", http.StatusNotFound)
		return
	}

	doneTimeout := fifo.doneTimeout
	doneTimeoutStr := r.URL.Query().Get("done_timeout")
	if doneTimeoutStr != "" {
		var err error
		doneTimeout, err = time.ParseDuration(doneTimeoutStr)
		if err != nil {
			log.Warn("invalid done_timeout", "err", err)
			http.Error(w, "invalid done_timeout", http.StatusBadRequest)
			return
		}
		log.Info("using custom done_timeout", "done_timeout", doneTimeout)
	}

	tick := newTicket(doneTimeout)
	log.Info("ticket created", "ticket", tick.TicketID)
	fifo.ticketLookup.Put(tick.TicketID.String(), tick)
	fifo.ticketQueue <- tick

	encode(w, 200, tick)
}

func (s *fifoManager) wait(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	tickID := r.PathValue("ticket")
	log := s.log.With("call", "wait", "uuid", uuid, "ticket", tickID)
	log.Info("called")

	fifo, ok := s.fifos.Get(uuid)
	if !ok {
		log.Warn("fifo not found")
		http.Error(w, "fifo not found", http.StatusNotFound)
		return
	}

	tick, ok := fifo.ticketLookup.Get(tickID)
	if !ok {
		log.Warn("ticket not found")
		http.Error(w, "ticket not found", http.StatusNotFound)
		return
	}

	log.Info("found ticket, waiting")
	<-tick.waitC
	tick.waitAck()
	log.Info("my turn")
}

func (s *fifoManager) done(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	tickID := r.PathValue("ticket")
	log := s.log.With("call", "done", "uuid", uuid, "ticket", tickID)
	log.Info("called")

	fifo, ok := s.fifos.Get(uuid)
	if !ok {
		log.Warn("fifo not found")
		http.Error(w, "fifo not found", http.StatusNotFound)
		return
	}

	tick, ok := fifo.ticketLookup.Get(tickID)
	if !ok {
		log.Warn("ticket not found")
		http.Error(w, "ticket not found", http.StatusNotFound)
		return
	}

	tick.doneC <- struct{}{}
	log.Info("ticket done")
}

func encode[T any](w http.ResponseWriter, status int, v T) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return nil
}
