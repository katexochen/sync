package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/katexochen/sync/api"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newMockDB() (*gorm.DB, sqlmock.Sqlmock, error) {
	db, mock, err := sqlmock.New()
	if err != nil {
		return nil, nil, err
	}

	mock.ExpectQuery("select sqlite_version\\(\\)").
		WillReturnRows(sqlmock.NewRows([]string{"sqlite_version()"}).AddRow("1.2.3"))
	gormDB, err := gorm.Open(sqlite.New(sqlite.Config{Conn: db}))
	if err != nil {
		return nil, nil, err
	}

	return gormDB, mock, nil
}

func newTestFifoManager(t *testing.T, db *gorm.DB, mock sqlmock.Sqlmock) *fifoManager {
	t.Helper()
	require := require.New(t)

	mock.ExpectExec("CREATE TABLE `fifos`").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE TABLE `tickets`").
		WillReturnResult(sqlmock.NewResult(0, 0))

	mgr := newFifoManager(db, slog.Default())
	require.NoError(mock.ExpectationsWereMet())

	return mgr
}

func TestNewFifo(t *testing.T) {
	require := require.New(t)

	gormDB, mock, err := newMockDB()
	require.NoError(err)

	mgr := newTestFifoManager(t, gormDB, mock)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `fifos`").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	req := httptest.NewRequest(http.MethodGet, "/fifo/new", http.NoBody)
	rec := httptest.NewRecorder()
	mgr.new(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(err)

	require.Equal(http.StatusOK, resp.StatusCode)
	fifoResp := &api.FifoNewResponse{}
	require.NoError(json.Unmarshal(body, fifoResp))
	require.NotEmpty(fifoResp.UUID)
	require.NoError(mock.ExpectationsWereMet())
}

func TestTicket(t *testing.T) {
	require := require.New(t)

	gormDB, mock, err := newMockDB()
	require.NoError(err)

	mgr := newTestFifoManager(t, gormDB, mock)

	fifoUUIDStr := "11111111-1111-1111-1111-111111111111"
	ticketUUIDStr := "22222222-2222-2222-2222-222222222222"

	// Parse fifo UUID from the request
	mock.ExpectQuery("SELECT \\* FROM `fifos` WHERE `fifos`.`uuid` = \\? ORDER BY `fifos`.`uuid` LIMIT 1").
		WillReturnRows(sqlmock.NewRows([]string{"uuid"}).AddRow(fifoUUIDStr))

	// Create a new ticket
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `tickets`").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	// Update the ticket queue
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `tickets` WHERE `tickets`.`fifo_uuid` = \\? ORDER BY created_at ASC LIMIT 2").
		WithArgs(fifoUUIDStr).
		WillReturnRows(sqlmock.NewRows([]string{"uuid", "fifo_uuid"}).AddRow(ticketUUIDStr, fifoUUIDStr))
	// Get fifo details from preload
	mock.ExpectQuery("SELECT \\* FROM `fifos` WHERE `fifos`.`uuid` = \\?").
		WithArgs(fifoUUIDStr).
		WillReturnRows(sqlmock.NewRows([]string{"uuid"}).AddRow(fifoUUIDStr))
	mock.ExpectBegin()
	// Mark the ticket as notified
	mock.ExpectExec("UPDATE `tickets` SET `notified_at`=\\? WHERE `uuid` = \\?").
		WithArgs(sqlmock.AnyArg(), ticketUUIDStr).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectCommit()

	req := httptest.NewRequest(http.MethodGet, "/fifo/"+fifoUUIDStr+"/ticket", nil)
	rec := httptest.NewRecorder()

	mux := http.NewServeMux()
	mgr.registerHandlers(mux)
	mux.ServeHTTP(rec, req)

	require.NoError(mock.ExpectationsWereMet())

	resp := rec.Result()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(err)

	require.Equal(http.StatusOK, resp.StatusCode)
	require.NoError(json.Unmarshal(body, &api.FifoTicketResponse{}))
}

func TestWait(t *testing.T) {
	require := require.New(t)

	gormDB, mock, err := newMockDB()
	require.NoError(err)

	mgr := newTestFifoManager(t, gormDB, mock)

	fifoUUIDStr := "11111111-1111-1111-1111-111111111111"
	ticketUUIDStr := "22222222-2222-2222-2222-222222222222"
	require.NoError(err)

	// Parse ticket UUID from the request
	mock.ExpectQuery("SELECT \\* FROM `tickets` WHERE `tickets`.`uuid` = \\? ORDER BY `tickets`.`uuid` LIMIT 1").
		WithArgs(ticketUUIDStr).
		WillReturnRows(sqlmock.NewRows([]string{"uuid", "fifo_uuid"}).
			AddRow(ticketUUIDStr, fifoUUIDStr))
	// Get fifo details from preload
	mock.ExpectQuery("SELECT \\* FROM `fifos` WHERE `fifos`.`uuid` = \\?").
		WithArgs(fifoUUIDStr).
		WillReturnRows(sqlmock.NewRows([]string{"uuid", "wait_timeout"}).
			AddRow(fifoUUIDStr, 1*time.Hour))

	// Update the ticket queue
	// Insert valid active ticket, so that we can wait for the second ticket
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `tickets` WHERE `tickets`.`fifo_uuid` = \\? ORDER BY created_at ASC LIMIT 2").
		WithArgs(fifoUUIDStr).
		WillReturnRows(sqlmock.NewRows([]string{"uuid", "fifo_uuid"}).
			AddRow(ticketUUIDStr, fifoUUIDStr))
	mock.ExpectQuery("SELECT \\* FROM `fifos` WHERE `fifos`.`uuid` = \\?").
		WithArgs(fifoUUIDStr).
		WillReturnRows(sqlmock.NewRows([]string{"uuid"}).
			AddRow(fifoUUIDStr))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `tickets` SET `notified_at`=\\? WHERE `uuid` = \\?").
		WithArgs(sqlmock.AnyArg(), ticketUUIDStr).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectCommit()

	// Mark the ticket as accepted
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `tickets`").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	req := httptest.NewRequest(http.MethodGet, "/fifo/"+fifoUUIDStr+"/wait/"+ticketUUIDStr, nil)
	rec := httptest.NewRecorder()

	mux := http.NewServeMux()
	mgr.registerHandlers(mux)

	done := make(chan struct{})
	go func() {
		defer close(done)
		mux.ServeHTTP(rec, req)
	}()

	select {
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout waiting for the request to complete")
	case <-done:
	}

	require.NoError(mock.ExpectationsWereMet())

	resp := rec.Result()
	defer resp.Body.Close()

	require.Equal(http.StatusOK, resp.StatusCode)
}
