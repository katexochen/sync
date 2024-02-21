package api

import uuidlib "github.com/google/uuid"

type (
	FifoNewResponse struct {
		UUID uuidlib.UUID `json:"uuid"`
	}
	FifoTicketResponse struct {
		TicketID uuidlib.UUID `json:"ticket"`
	}
)
