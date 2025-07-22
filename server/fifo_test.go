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
	uuidlib "github.com/google/uuid"
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

	mock.ExpectExec("PRAGMA foreign_keys=ON").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := gormDB.Exec("PRAGMA foreign_keys=ON").Error; err != nil {
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

	req := httptest.NewRequest(http.MethodGet, "/fifos", nil)
	rec := httptest.NewRecorder()

	mgr.new(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(err)

	require.Equal(http.StatusOK, resp.StatusCode)
	require.NoError(json.Unmarshal(body, &api.FifoNewResponse{}))
}

func TestTicket(t *testing.T) {
	require := require.New(t)

	gormDB, mock, err := newMockDB()
	require.NoError(err)

	mgr := newTestFifoManager(t, gormDB, mock)

	fifoUUIDStr := "00000000-0000-0000-0000-000000000000"
	ticketUUIDStr := "11111111-1111-1111-1111-111111111111"

	// Parse fifo UUID from the request
	mock.ExpectQuery("SELECT \\* FROM `fifos` ORDER BY `fifos`.`uuid` LIMIT 1").
		WillReturnRows(sqlmock.NewRows([]string{"uuid"}).AddRow(fifoUUIDStr))

	// Create a new ticket
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `tickets`").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	expectUpdateTicketQueue(mock, fifoUUIDStr, ticketUUIDStr)

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

	fifoUUIDStr := "00000000-0000-0000-0000-000000000000"
	ticketUUIDStr := "11111111-1111-1111-1111-111111111111"
	ticketUUID, err := uuidlib.Parse(ticketUUIDStr)
	require.NoError(err)

	// Parse ticket UUID from the request
	mock.ExpectQuery("SELECT \\* FROM `tickets` WHERE `tickets`.`uuid` = \\?").
		WithArgs(ticketUUIDStr).
		WillReturnRows(sqlmock.NewRows([]string{"uuid", "fifo_uuid"}).
			AddRow(ticketUUIDStr, fifoUUIDStr))

	// Update the ticket queue
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `tickets` WHERE `tickets`.`fifo_uuid` = \\? ORDER BY created_at ASC LIMIT 2").
		WithArgs(fifoUUIDStr).WillReturnRows(sqlmock.NewRows([]string{"uuid", "notified_at"}))
	mock.ExpectCommit()

	// Update the ticket as notified
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `tickets`").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	req := httptest.NewRequest(http.MethodGet, "/fifo/"+fifoUUIDStr+"/wait/"+ticketUUIDStr, nil)
	rec := httptest.NewRecorder()

	mux := http.NewServeMux()
	mgr.registerHandlers(mux)

	wait := make(chan struct{})
	go func() {
		mux.ServeHTTP(rec, req)
		close(wait)
	}()

	require.Eventually(func() bool {
		ch, ok := mgr.waiters[ticketUUID]
		if ok {
			close(ch)
			return true
		}
		return false
	}, 2*time.Second, 100*time.Millisecond)

	require.Eventually(func() bool {
		select {
		case <-wait:
			return true
		default:
			return false
		}
	}, 2*time.Second, 100*time.Millisecond)

	require.NoError(mock.ExpectationsWereMet())

	resp := rec.Result()
	defer resp.Body.Close()

	require.Equal(http.StatusOK, resp.StatusCode)
}

func expectUpdateTicketQueue(mock sqlmock.Sqlmock, fifoUUIDStr, ticketUUIDStr string) {
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `tickets` WHERE `tickets`.`fifo_uuid` = \\? ORDER BY created_at ASC LIMIT 2").
		WithArgs(fifoUUIDStr).
		WillReturnRows(sqlmock.NewRows([]string{"uuid", "notified_at"}).AddRow(ticketUUIDStr, nil))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `tickets`").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectCommit()
}
