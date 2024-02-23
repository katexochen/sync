package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/katexochen/sync/api"
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

func endpoint() string {
	e := os.Getenv("E2E_ENDPOINT")
	if e == "" {
		e = "http://localhost:8080"
	}
	return e
}
