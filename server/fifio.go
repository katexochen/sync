package main

import (
	"container/list"
	"log/slog"
	"net/http"
	"time"

	uuidlib "github.com/google/uuid"
	"github.com/katexochen/sync/api"
	"github.com/katexochen/sync/internal/memstore"
)

type ticket struct {
	api.FifoTicketResponse
	waitC chan struct{}
	doneC chan struct{}
}

func newTicket() *ticket {
	return &ticket{
		FifoTicketResponse: api.FifoTicketResponse{TicketID: uuidlib.New()},
		waitC:              make(chan struct{}),
		doneC:              make(chan struct{}),
	}
}

type fifo struct {
	uuid         uuidlib.UUID
	waitTimeout  time.Duration
	doneTimeout  time.Duration
	ticketLookup *memstore.Store[string, *ticket]
	ticketQueue  chan *ticket
	log          *slog.Logger
}

func newFifo(log *slog.Logger) *fifo {
	uuid := uuidlib.New()
	return &fifo{
		uuid:         uuid,
		waitTimeout:  time.Minute,
		doneTimeout:  10 * time.Minute,
		ticketLookup: memstore.New[string, *ticket](),
		ticketQueue:  make(chan *ticket, 100),
		log:          log.WithGroup("fifo").With("uuid", uuid.String()),
	}
}

func (f *fifo) start() {
	go func() {
		f.log.Info("started")
		for {
			f.log.Info("waiting for ticket")
			t := <-f.ticketQueue
			f.log.Info("got ticket", "ticket", t.TicketID)
			select {
			case <-time.After(f.waitTimeout):
				f.log.Warn("timeout waiting for ticket owner", "ticket", t.TicketID)
				continue
			case t.waitC <- struct{}{}:
				f.log.Info("ticket owner notified", "ticket", t.TicketID)
			}
			select {
			case <-time.After(f.doneTimeout):
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
	writeJSON(w, api.FifoNewResponse{UUID: fifo.uuid})
}

func (s *fifoManager) ticket(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	log := s.log.With("call", "ticket", "uuid", uuid)
	log.Info("called")

	fifo, ok := s.fifos.Get(uuid)
	if !ok {
		slog.Warn("not found")
		http.Error(w, "fifo not found", http.StatusNotFound)
		return
	}

	tick := newTicket()
	fifo.ticketLookup.Put(tick.TicketID.String(), tick)
	fifo.ticketQueue <- tick

	writeJSON(w, tick)
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
	}

	tick.doneC <- struct{}{}
	log.Info("ticket done")
}

func searchTicket(tickets *list.List, tickID string) (*ticket, bool) {
	_, t, ok := searchTicketElement(tickets, tickID)
	return t, ok
}

func searchElement(l *list.List, id string) (*list.Element, bool) {
	e, _, ok := searchTicketElement(l, id)
	return e, ok
}

func searchTicketElement(l *list.List, id string) (*list.Element, *ticket, bool) {
	for e := l.Front(); e != nil; e = e.Next() {
		t := e.Value.(*ticket)
		if t.TicketID.String() == id {
			return e, t, true
		}
	}
	return nil, nil, false
}
