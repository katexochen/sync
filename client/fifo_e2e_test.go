package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/katexochen/sync/api"
	ihttp "github.com/katexochen/sync/internal/http"
	"github.com/stretchr/testify/require"
)

func TestFifoBasics(t *testing.T) {
	ctx := context.Background()
	endpoint := endpoint()
	var uuid, ticket string
	t.Run("new", func(t *testing.T) {
		require := require.New(t)
		out, err := RunFifoNew(ctx, ihttp.NewClient(), &FifoFlags{
			endpoint: endpoint,
			output:   "json",
		})
		require.NoError(err)
		resp, err := decode[api.FifoNewResponse](out)
		require.NoError(err)
		uuid = resp.UUID.String()
	})
	t.Run("ticket", func(t *testing.T) {
		require := require.New(t)
		out, err := RunFifoTicket(ctx, ihttp.NewClient(), &FifoFlags{
			endpoint: endpoint,
			output:   "json",
			uuid:     uuid,
		})
		require.NoError(err)
		resp, err := decode[api.FifoTicketResponse](out)
		require.NoError(err)
		ticket = resp.TicketID.String()
	})
	t.Run("wait", func(t *testing.T) {
		require := require.New(t)
		require.NoError(RunFifoWait(ctx, ihttp.NewClient(), &FifoFlags{
			endpoint: endpoint,
			output:   "json",
			uuid:     uuid,
			ticketID: ticket,
		}))
	})
	t.Run("done", func(t *testing.T) {
		require := require.New(t)
		require.NoError(RunFifoDone(ctx, ihttp.NewClient(), &FifoFlags{
			endpoint: endpoint,
			output:   "json",
			uuid:     uuid,
			ticketID: ticket,
		}))
	})
}

func TestFifoConcurrent100(t *testing.T) {
	require := require.New(t)
	ctx := context.Background()
	endpoint := endpoint()

	var mux sync.Mutex
	assertResourceExclusive := func() {
		if ok := mux.TryLock(); !ok {
			require.Fail("tried to access resource in use")
		}
		defer mux.Unlock()
		time.Sleep(time.Duration(rand.Intn(1000)) * time.Millisecond)
	}

	// Prepare the fifo
	out, err := RunFifoNew(ctx, ihttp.NewClient(), &FifoFlags{
		endpoint: endpoint,
		output:   "json",
	})
	require.NoError(err)
	respNew, err := decode[api.FifoNewResponse](out)
	require.NoError(err)

	runRandomClient := func(wg *sync.WaitGroup) {
		defer wg.Done()

		time.Sleep(time.Duration(rand.Intn(3000)) * time.Millisecond)

		out, err := RunFifoTicket(ctx, ihttp.NewClient(), &FifoFlags{
			endpoint: endpoint,
			output:   "json",
			uuid:     respNew.UUID.String(),
		})
		require.NoError(err)
		respTicket, err := decode[api.FifoTicketResponse](out)
		require.NoError(err)

		require.NoError(RunFifoWait(ctx, ihttp.NewClient(), &FifoFlags{
			endpoint: endpoint,
			output:   "json",
			uuid:     respNew.UUID.String(),
			ticketID: respTicket.TicketID.String(),
		}))

		assertResourceExclusive()

		require.NoError(RunFifoDone(ctx, ihttp.NewClient(), &FifoFlags{
			endpoint: endpoint,
			output:   "json",
			uuid:     respNew.UUID.String(),
			ticketID: respTicket.TicketID.String(),
		}))
	}

	var wg sync.WaitGroup
	n := 100
	wg.Add(n)
	for i := 0; i < n; i++ {
		go runRandomClient(&wg)
	}
	wg.Wait()
}

func TestFifo100Waiting(t *testing.T) {
	require := require.New(t)
	ctx := context.Background()
	endpoint := endpoint()

	// Prepare the fifo.
	out, err := RunFifoNew(ctx, ihttp.NewClient(), &FifoFlags{
		endpoint: endpoint,
		output:   "json",
	})
	require.NoError(err)
	respNew, err := decode[api.FifoNewResponse](out)
	require.NoError(err)
	t.Log("fifo uuid:", respNew.UUID)

	// Get a ticket.
	out, err = RunFifoTicket(ctx, ihttp.NewClient(), &FifoFlags{
		endpoint: endpoint,
		output:   "json",
		uuid:     respNew.UUID.String(),
	})
	require.NoError(err)
	respTicket1, err := decode[api.FifoTicketResponse](out)
	require.NoError(err)
	t.Log("ticket1 uuid:", respTicket1.TicketID)

	// Get a second ticket.
	out, err = RunFifoTicket(ctx, ihttp.NewClient(), &FifoFlags{
		endpoint: endpoint,
		output:   "json",
		uuid:     respNew.UUID.String(),
	})
	require.NoError(err)
	respTicket2, err := decode[api.FifoTicketResponse](out)
	require.NoError(err)
	t.Log("ticket2 uuid:", respTicket2.TicketID)

	// Wait for the first ticket.
	require.NoError(RunFifoWait(ctx, ihttp.NewClient(), &FifoFlags{
		endpoint: endpoint,
		output:   "json",
		uuid:     respNew.UUID.String(),
		ticketID: respTicket1.TicketID.String(),
	}))
	t.Log("ticket1 is ready")

	// Now the resource is blocked.
	// Additional clients can join waiting the ticket.

	runWaitClient := func(wg *sync.WaitGroup, ticketID string) {
		defer wg.Done()
		require.NoError(RunFifoWait(ctx, ihttp.NewClient(), &FifoFlags{
			endpoint: endpoint,
			output:   "json",
			uuid:     respNew.UUID.String(),
			ticketID: ticketID,
		}))
	}

	var wg1, wg2 sync.WaitGroup
	n := 100
	wg1.Add(n)
	for i := 0; i < n; i++ {
		go runWaitClient(&wg1, respTicket1.TicketID.String())
	}
	wg2.Add(n)
	for i := 0; i < n; i++ {
		go runWaitClient(&wg2, respTicket2.TicketID.String())
	}

	// Wait so that goroutines are started and blocking.
	time.Sleep(100 * time.Millisecond)

	// Now we can release the first ticket.
	require.NoError(RunFifoDone(ctx, ihttp.NewClient(), &FifoFlags{
		endpoint: endpoint,
		output:   "json",
		uuid:     respNew.UUID.String(),
		ticketID: respTicket1.TicketID.String(),
	}))
	t.Log("ticket1 is done")

	// All clients wating on the first ticket should be released.
	wg1.Wait()
	t.Log("all clients waiting on ticket1 are released")

	// Now we can release the second ticket.
	require.NoError(RunFifoDone(ctx, ihttp.NewClient(), &FifoFlags{
		endpoint: endpoint,
		output:   "json",
		uuid:     respNew.UUID.String(),
		ticketID: respTicket2.TicketID.String(),
	}))
	t.Log("ticket2 is done")

	// All clients wating on the second ticket should be released.
	wg2.Wait()
	t.Log("all clients waiting on ticket2 are released")
}

func endpoint() string {
	e := os.Getenv("E2E_ENDPOINT")
	if e == "" {
		e = "http://localhost:8080"
	}
	return e
}

func decode[T any](s string) (T, error) {
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return v, fmt.Errorf("unmarshal json: %w", err)
	}
	return v, nil
}
