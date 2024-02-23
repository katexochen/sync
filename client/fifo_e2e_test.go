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

func endpoint() string {
	e := os.Getenv("E2E_ENDPOINT")
	if e == "" {
		e = "http://localhost:8080"
	}
	return e
}
