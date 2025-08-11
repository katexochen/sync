package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/katexochen/sync/api"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"k8s.io/utils/clock"
	clocktest "k8s.io/utils/clock/testing"
)

const (
	fifoUUIDStr   = "11111111-1111-1111-1111-111111111111"
	ticketUUIDStr = "22222222-2222-2222-2222-222222222222"
	dummyUUIDStr  = "33333333-3333-3333-3333-333333333333"
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

func newTestFifoManager(t *testing.T, db *gorm.DB, mock sqlmock.Sqlmock, c clock.WithDelayedExecution) *fifoManager {
	t.Helper()
	require := require.New(t)

	mock.ExpectExec("CREATE TABLE `fifos`").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE TABLE `tickets`").
		WillReturnResult(sqlmock.NewResult(0, 0))

	mgr, err := newFifoManager(db, c, slog.Default())
	require.NoError(err)
	require.NoError(mock.ExpectationsWereMet())

	return mgr
}

func TestNewFifo(t *testing.T) {
	require := require.New(t)

	gormDB, mock, err := newMockDB()
	require.NoError(err)
	mgr := newTestFifoManager(t, gormDB, mock, clocktest.NewFakeClock(time.Now()))

	waitTimeout := 1 * time.Second
	acceptTimeout := 2 * time.Second
	doneTimeout := 3 * time.Second
	unusedTimeout := 4 * time.Second

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `fifos` \\(`uuid`,`created_at`,`updated_at`,`wait_timeout`,`accept_timeout`,`done_timeout`,`unused_destroy_timeout`,`allow_overrides`\\) VALUES \\(\\?,\\?,\\?,\\?,\\?,\\?,\\?,\\?\\)").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), waitTimeout, acceptTimeout, doneTimeout, unusedTimeout, true).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	reqPath := fmt.Sprintf("/fifo/new?wait_timeout=%s&accept_timeout=%s&done_timeout=%s&unused_destroy_timeout=%s&allow_overrides=true", waitTimeout, acceptTimeout, doneTimeout, unusedTimeout)
	req := httptest.NewRequest(http.MethodGet, reqPath, http.NoBody)
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
	mgr := newTestFifoManager(t, gormDB, mock, clocktest.NewFakeClock(time.Now()))

	waitTimeout := 1 * time.Second
	acceptTimeout := 2 * time.Second
	doneTimeout := 3 * time.Second

	// Parse fifo UUID from the request
	mock.ExpectQuery("SELECT \\* FROM `fifos` WHERE `fifos`.`uuid` = \\? ORDER BY `fifos`.`uuid` LIMIT 1").
		WillReturnRows(sqlmock.NewRows([]string{"uuid", "allow_overrides"}).
			AddRow(fifoUUIDStr, true))

	// Create a new ticket
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `tickets` \\(`uuid`,`created_at`,`notified_at`,`accepted_at`,`wait_timeout`,`accept_timeout`,`done_timeout`,`fifo_uuid`\\) VALUES \\(\\?,\\?,\\?,\\?,\\?,\\?,\\?,\\?\\)").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), nil, nil, waitTimeout, acceptTimeout, doneTimeout, fifoUUIDStr).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	// Update the ticket queue
	mock.ExpectBegin()
	// Update fifo timestamp
	mock.ExpectQuery("SELECT \\* FROM `fifos` WHERE `fifos`.`uuid` = \\? ORDER BY `fifos`.`uuid` LIMIT 1").
		WithArgs(fifoUUIDStr).
		WillReturnRows(sqlmock.NewRows([]string{"uuid"}).AddRow(fifoUUIDStr))
	mock.ExpectExec("UPDATE `fifos` SET `updated_at`=\\? WHERE `uuid` = \\?").
		WithArgs(sqlmock.AnyArg(), fifoUUIDStr).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT \\* FROM `tickets` WHERE `tickets`.`fifo_uuid` = \\? ORDER BY created_at ASC LIMIT 2").
		WithArgs(fifoUUIDStr).
		WillReturnRows(sqlmock.NewRows([]string{"uuid"}).AddRow(ticketUUIDStr))
	mock.ExpectExec("UPDATE `tickets` SET `notified_at`=\\? WHERE `uuid` = \\?").
		WithArgs(sqlmock.AnyArg(), ticketUUIDStr).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	reqPath := fmt.Sprintf("/fifo/%s/ticket?wait_timeout=%s&accept_timeout=%s&done_timeout=%s", fifoUUIDStr, waitTimeout, acceptTimeout, doneTimeout)
	req := httptest.NewRequest(http.MethodGet, reqPath, nil)
	req.SetPathValue("uuid", fifoUUIDStr)
	rec := httptest.NewRecorder()

	mgr.ticket(rec, req)

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

	mgr := newTestFifoManager(t, gormDB, mock, clocktest.NewFakeClock(time.Now()))

	// Parse ticket UUID from the request
	mock.ExpectQuery("SELECT \\* FROM `tickets` WHERE `tickets`.`uuid` = \\? ORDER BY `tickets`.`uuid` LIMIT 1").
		WithArgs(ticketUUIDStr).
		WillReturnRows(sqlmock.NewRows([]string{"uuid", "wait_timeout", "fifo_uuid"}).
			AddRow(ticketUUIDStr, time.Hour, fifoUUIDStr))

	// Update the ticket queue
	mock.ExpectBegin()
	// Update fifo timestamp
	mock.ExpectQuery("SELECT \\* FROM `fifos` WHERE `fifos`.`uuid` = \\? ORDER BY `fifos`.`uuid` LIMIT 1").
		WithArgs(fifoUUIDStr).
		WillReturnRows(sqlmock.NewRows([]string{"uuid"}).AddRow(fifoUUIDStr))
	mock.ExpectExec("UPDATE `fifos` SET `updated_at`=\\? WHERE `uuid` = \\?").
		WithArgs(sqlmock.AnyArg(), fifoUUIDStr).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT \\* FROM `tickets` WHERE `tickets`.`fifo_uuid` = \\? ORDER BY created_at ASC LIMIT 2").
		WithArgs(fifoUUIDStr).
		WillReturnRows(sqlmock.NewRows([]string{"uuid"}).AddRow(ticketUUIDStr))
	mock.ExpectExec("UPDATE `tickets` SET `notified_at`=\\? WHERE `uuid` = \\?").
		WithArgs(sqlmock.AnyArg(), ticketUUIDStr).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	// Mark the ticket as accepted
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `tickets`").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	req := httptest.NewRequest(http.MethodGet, "/fifo/"+fifoUUIDStr+"/wait/"+ticketUUIDStr, nil)
	req.SetPathValue("uuid", fifoUUIDStr)
	req.SetPathValue("ticket", ticketUUIDStr)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		mgr.wait(rec, req)
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

func TestDone(t *testing.T) {
	require := require.New(t)

	gormDB, mock, err := newMockDB()
	require.NoError(err)

	mgr := newTestFifoManager(t, gormDB, mock, clocktest.NewFakeClock(time.Now()))

	ticketUUID, err := uuid.Parse(ticketUUIDStr)
	require.NoError(err)

	// Parse ticket UUID from the request
	mock.ExpectQuery("SELECT \\* FROM `tickets` WHERE `tickets`.`uuid` = \\? ORDER BY `tickets`.`uuid` LIMIT 1").
		WithArgs(ticketUUIDStr).
		WillReturnRows(sqlmock.NewRows([]string{"uuid", "fifo_uuid"}).
			AddRow(ticketUUIDStr, fifoUUIDStr))
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `tickets` WHERE `tickets`.`uuid` = \\?").
		WithArgs(ticketUUIDStr).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	// Update the ticket queue
	mock.ExpectBegin()
	// Update fifo timestamp
	mock.ExpectQuery("SELECT \\* FROM `fifos` WHERE `fifos`.`uuid` = \\? ORDER BY `fifos`.`uuid` LIMIT 1").
		WithArgs(fifoUUIDStr).
		WillReturnRows(sqlmock.NewRows([]string{"uuid"}).AddRow(fifoUUIDStr))
	mock.ExpectExec("UPDATE `fifos` SET `updated_at`=\\? WHERE `uuid` = \\?").
		WithArgs(sqlmock.AnyArg(), fifoUUIDStr).
		WillReturnResult(sqlmock.NewResult(1, 1))
	// Insert valid active ticket, so that we can wait for the second ticket
	mock.ExpectQuery("SELECT \\* FROM `tickets` WHERE `tickets`.`fifo_uuid` = \\? ORDER BY created_at ASC LIMIT 2").
		WithArgs(fifoUUIDStr).
		WillReturnRows(sqlmock.NewRows([]string{}))
	mock.ExpectCommit()

	mgr.waiters[ticketUUID] = make(chan struct{})

	req := httptest.NewRequest(http.MethodGet, "/fifo/"+fifoUUIDStr+"/done/"+ticketUUIDStr, nil)
	req.SetPathValue("uuid", fifoUUIDStr)
	req.SetPathValue("ticket", ticketUUIDStr)
	rec := httptest.NewRecorder()

	mgr.done(rec, req)

	require.NoError(mock.ExpectationsWereMet())

	resp := rec.Result()
	defer resp.Body.Close()

	require.Equal(http.StatusOK, resp.StatusCode)
	require.Empty(mgr.waiters)
}

func TestTimeouts(t *testing.T) {
	t.Run("wait_timeout", func(t *testing.T) {
		require := require.New(t)
		gormDB, mock, err := newMockDB()
		require.NoError(err)
		c := clocktest.NewFakeClock(time.Now())
		mgr := newTestFifoManager(t, gormDB, mock, c)

		waitTimeout := 1 * time.Second

		mock.ExpectQuery("SELECT \\* FROM `tickets` WHERE `tickets`.`uuid` = \\? ORDER BY `tickets`.`uuid` LIMIT 1").
			WithArgs(ticketUUIDStr).
			WillReturnRows(sqlmock.NewRows([]string{"uuid", "wait_timeout", "fifo_uuid"}).
				AddRow(ticketUUIDStr, waitTimeout, fifoUUIDStr))
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT \\* FROM `fifos` WHERE `fifos`.`uuid` = \\? ORDER BY `fifos`.`uuid` LIMIT 1").
			WithArgs(fifoUUIDStr).
			WillReturnRows(sqlmock.NewRows([]string{"uuid"}).AddRow(fifoUUIDStr))
		mock.ExpectExec("UPDATE `fifos` SET `updated_at`=\\? WHERE `uuid` = \\?").
			WithArgs(sqlmock.AnyArg(), fifoUUIDStr).
			WillReturnResult(sqlmock.NewResult(1, 1))
		// Simulate no tickets available, so the wait will timeout
		mock.ExpectQuery("SELECT \\* FROM `tickets` WHERE `tickets`.`fifo_uuid` = \\? ORDER BY created_at ASC LIMIT 2").
			WithArgs(fifoUUIDStr).
			WillReturnRows(sqlmock.NewRows([]string{"uuid"}))
		mock.ExpectCommit()

		req := httptest.NewRequest(http.MethodGet, "/fifo/"+fifoUUIDStr+"/wait/"+ticketUUIDStr, nil)
		req.SetPathValue("uuid", fifoUUIDStr)
		req.SetPathValue("ticket", ticketUUIDStr)
		rec := httptest.NewRecorder()

		done := make(chan struct{})
		go func() {
			defer close(done)
			mgr.wait(rec, req)
		}()

		require.Eventually(func() bool {
			return c.HasWaiters()
		}, 200*time.Millisecond, 40*time.Millisecond, "request should be waiting")

		c.Step(waitTimeout + 100*time.Millisecond)
		select {
		case <-time.After(200 * time.Millisecond):
			t.Fatal("timeout waiting for the request to complete")
		case <-done:
		}

		resp := rec.Result()
		defer resp.Body.Close()

		require.Equal(http.StatusRequestTimeout, resp.StatusCode)
		require.NoError(mock.ExpectationsWereMet())
	})

	t.Run("accept_timeout", func(t *testing.T) {
		require := require.New(t)
		gormDB, mock, err := newMockDB()
		require.NoError(err)
		c := clocktest.NewFakeClock(time.Now())
		mgr := newTestFifoManager(t, gormDB, mock, c)

		acceptTimeout := 1 * time.Second

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT \\* FROM `fifos` WHERE `fifos`.`uuid` = \\? ORDER BY `fifos`.`uuid` LIMIT 1").
			WithArgs(fifoUUIDStr).
			WillReturnRows(sqlmock.NewRows([]string{"uuid"}).AddRow(fifoUUIDStr))
		mock.ExpectExec("UPDATE `fifos` SET `updated_at`=\\? WHERE `uuid` = \\?").
			WithArgs(sqlmock.AnyArg(), fifoUUIDStr).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery("SELECT \\* FROM `tickets` WHERE `tickets`.`fifo_uuid` = \\? ORDER BY created_at ASC LIMIT 2").
			WithArgs(fifoUUIDStr).
			WillReturnRows(sqlmock.NewRows([]string{"uuid", "notified_at", "accept_timeout"}).
				AddRow(ticketUUIDStr, c.Now(), acceptTimeout))
		mock.ExpectExec("DELETE FROM `tickets` WHERE `tickets`.`uuid` = \\?").
			WithArgs(ticketUUIDStr).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		c.Step(acceptTimeout + 100*time.Millisecond)

		fifoUUID, err := uuid.Parse(fifoUUIDStr)
		require.NoError(err)
		require.NoError(mgr.updateTicketQueue(fifoUUID))

		require.NoError(mock.ExpectationsWereMet())
	})

	t.Run("done_timeout", func(t *testing.T) {
		require := require.New(t)
		gormDB, mock, err := newMockDB()
		require.NoError(err)
		c := clocktest.NewFakeClock(time.Now())
		mgr := newTestFifoManager(t, gormDB, mock, c)

		doneTimeout := 1 * time.Second

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT \\* FROM `fifos` WHERE `fifos`.`uuid` = \\? ORDER BY `fifos`.`uuid` LIMIT 1").
			WithArgs(fifoUUIDStr).
			WillReturnRows(sqlmock.NewRows([]string{"uuid"}).AddRow(fifoUUIDStr))
		mock.ExpectExec("UPDATE `fifos` SET `updated_at`=\\? WHERE `uuid` = \\?").
			WithArgs(sqlmock.AnyArg(), fifoUUIDStr).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery("SELECT \\* FROM `tickets` WHERE `tickets`.`fifo_uuid` = \\? ORDER BY created_at ASC LIMIT 2").
			WithArgs(fifoUUIDStr).
			WillReturnRows(sqlmock.NewRows([]string{"uuid", "notified_at", "accepted_at", "done_timeout"}).
				AddRow(ticketUUIDStr, nil, c.Now(), doneTimeout).
				AddRow(dummyUUIDStr, c.Now(), nil, doneTimeout))
		mock.ExpectExec("DELETE FROM `tickets` WHERE `tickets`.`uuid` = \\?").
			WithArgs(ticketUUIDStr).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		c.Step(doneTimeout + 100*time.Millisecond)

		fifoUUID, err := uuid.Parse(fifoUUIDStr)
		require.NoError(err)
		require.NoError(mgr.updateTicketQueue(fifoUUID))

		require.NoError(mock.ExpectationsWereMet())
	})

	t.Run("unused_destroy_timeout", func(t *testing.T) {
		require := require.New(t)
		gormDB, mock, err := newMockDB()
		require.NoError(err)
		c := clocktest.NewFakeClock(time.Now())
		mgr := &fifoManager{
			log:      slog.Default(),
			db:       gormDB,
			waiters:  make(map[uuid.UUID]chan struct{}),
			clock:    c,
			pullRate: 5 * time.Second,
		}

		unusedTimeout := 1 * time.Second

		mock.ExpectQuery("SELECT \\* FROM `fifos`").
			WillReturnRows(sqlmock.NewRows([]string{"uuid", "updated_at", "unused_destroy_timeout"}).
				AddRow(fifoUUIDStr, c.Now(), unusedTimeout))
		mock.ExpectBegin()
		mock.ExpectExec("DELETE FROM `fifos` WHERE `fifos`.`uuid` = \\?").
			WithArgs(fifoUUIDStr).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		ctx, cancel := context.WithCancel(t.Context())
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			mgr.run(ctx)
		}()

		require.Eventually(func() bool {
			return c.HasWaiters()
		}, 200*time.Millisecond, 40*time.Millisecond, "function should be waiting")

		c.Step(mgr.pullRate + 100*time.Millisecond)
		time.Sleep(10 * time.Millisecond)

		cancel()
		wg.Wait()

		require.NoError(mock.ExpectationsWereMet())
	})
}
