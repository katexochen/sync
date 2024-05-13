package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/katexochen/sync/api"
	ihttp "github.com/katexochen/sync/internal/http"
)

type Fifo struct {
	endpoint   string
	client     *ihttp.Client
	fifoUUID   string
	ticketUUID string
}

func NewFifo(ctx context.Context, endpoint string) (*Fifo, error) {
	f := &Fifo{
		endpoint: endpoint,
		client:   ihttp.NewClient(),
	}

	url, err := urlJoin(endpoint, "fifo", "new")
	if err != nil {
		return nil, err
	}
	resp := &api.FifoNewResponse{}
	if err := f.client.RequestJSON(ctx, url, http.NoBody, resp); err != nil {
		return nil, err
	}

	f.fifoUUID = resp.UUID.String()
	return f, nil
}

func FifoFromUUID(endpoint, uuid string) *Fifo {
	f := &Fifo{
		endpoint: endpoint,
		client:   ihttp.NewClient(),
		fifoUUID: uuid,
	}
	return f
}

func (f *Fifo) Ticket(ctx context.Context) error {
	url, err := urlJoin(f.endpoint, "fifo", f.fifoUUID, "ticket")
	if err != nil {
		return err
	}
	resp := &api.FifoTicketResponse{}
	if err := f.client.RequestJSON(ctx, url, http.NoBody, resp); err != nil {
		return err
	}
	f.ticketUUID = resp.TicketID.String()
	return nil
}

func (f *Fifo) Wait(ctx context.Context) error {
	url, err := urlJoin(f.endpoint, "fifo", f.fifoUUID, "wait", f.ticketUUID)
	if err != nil {
		return err
	}
	return f.client.Get(ctx, url)
}

func (f *Fifo) TicketAndWait(ctx context.Context) error {
	if err := f.Ticket(ctx); err != nil {
		return err
	}
	return f.Wait(ctx)
}

func (f *Fifo) Done(ctx context.Context) error {
	url, err := urlJoin(f.endpoint, "fifo", f.fifoUUID, "done", f.ticketUUID)
	if err != nil {
		return err
	}
	return f.client.Get(ctx, url)
}

func urlJoin(base string, pathSegments ...string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parsing endpoint: %w", err)
	}
	return u.JoinPath(pathSegments...).String(), nil
}
