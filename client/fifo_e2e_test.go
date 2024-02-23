package main

import (
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/katexochen/sync/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFifoBasics(t *testing.T) {
	ctx := context.Background()
	endpoint := endpoint()
	var uuid, ticket string
	t.Run("new", func(t *testing.T) {
		require := require.New(t)
		out, err := RunFifoNew(ctx, newHTTPClient(), &FifoFlags{
			endpoint: endpoint,
			output:   "json",
		})
		require.NoError(err)
		resp := &api.FifoNewResponse{}
		require.NoError(json.Unmarshal([]byte(out), resp))
		uuid = resp.UUID.String()
	})
	t.Run("ticket", func(t *testing.T) {
		require := require.New(t)
		out, err := RunFifoTicket(ctx, newHTTPClient(), &FifoFlags{
			endpoint: endpoint,
			output:   "json",
			uuid:     uuid,
		})
		require.NoError(err)
		resp := &api.FifoTicketResponse{}
		require.NoError(json.Unmarshal([]byte(out), resp))
		ticket = resp.TicketID.String()
	})
	t.Run("wait", func(t *testing.T) {
		require := require.New(t)
		require.NoError(RunFifoWait(ctx, newHTTPClient(), &FifoFlags{
			endpoint: endpoint,
			output:   "json",
			uuid:     uuid,
			ticketID: ticket,
		}))
	})
	t.Run("done", func(t *testing.T) {
		require := require.New(t)
		require.NoError(RunFifoDone(ctx, newHTTPClient(), &FifoFlags{
			endpoint: endpoint,
			output:   "json",
			uuid:     uuid,
			ticketID: ticket,
		}))
	})
}

func TestFifoConcurrent100(t *testing.T) {
	assert := assert.New(t)
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
	out, err := RunFifoNew(ctx, newHTTPClient(), &FifoFlags{
		endpoint: endpoint,
		output:   "json",
	})
	require.NoError(err)
	respNew := &api.FifoNewResponse{}
	require.NoError(json.Unmarshal([]byte(out), respNew))

	runRandomClient := func(wg *sync.WaitGroup) {
		defer wg.Done()

		time.Sleep(time.Duration(rand.Intn(3000)) * time.Millisecond)

		out, err := RunFifoTicket(ctx, newHTTPClient(), &FifoFlags{
			endpoint: endpoint,
			output:   "json",
			uuid:     respNew.UUID.String(),
		})
		assert.NoError(err)
		respTicket := &api.FifoTicketResponse{}
		assert.NoError(json.Unmarshal([]byte(out), respTicket))

		assert.NoError(RunFifoWait(ctx, newHTTPClient(), &FifoFlags{
			endpoint: endpoint,
			output:   "json",
			uuid:     respNew.UUID.String(),
			ticketID: respTicket.TicketID.String(),
		}))

		assertResourceExclusive()

		assert.NoError(RunFifoDone(ctx, newHTTPClient(), &FifoFlags{
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
	out, err := RunFifoNew(ctx, newHTTPClient(), &FifoFlags{
		endpoint: endpoint,
		output:   "json",
	})
	require.NoError(err)
	respNew := &api.FifoNewResponse{}
	require.NoError(json.Unmarshal([]byte(out), respNew))
	t.Log("fifo uuid:", respNew.UUID)

	// Get a ticket.
	out, err = RunFifoTicket(ctx, newHTTPClient(), &FifoFlags{
		endpoint: endpoint,
		output:   "json",
		uuid:     respNew.UUID.String(),
	})
	require.NoError(err)
	respTicket1 := &api.FifoTicketResponse{}
	require.NoError(json.Unmarshal([]byte(out), respTicket1))
	t.Log("ticket1 uuid:", respTicket1.TicketID)

	// Get a second ticket.
	out, err = RunFifoTicket(ctx, newHTTPClient(), &FifoFlags{
		endpoint: endpoint,
		output:   "json",
		uuid:     respNew.UUID.String(),
	})
	require.NoError(err)
	respTicket2 := &api.FifoTicketResponse{}
	require.NoError(json.Unmarshal([]byte(out), respTicket2))
	t.Log("ticket2 uuid:", respTicket2.TicketID)

	// Wait for the first ticket.
	require.NoError(RunFifoWait(ctx, newHTTPClient(), &FifoFlags{
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
		require.NoError(RunFifoWait(ctx, newHTTPClient(), &FifoFlags{
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
	require.NoError(RunFifoDone(ctx, newHTTPClient(), &FifoFlags{
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
	require.NoError(RunFifoDone(ctx, newHTTPClient(), &FifoFlags{
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
