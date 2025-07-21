
## FIFO

### `/fifo/new`

Create a new FIFO queue.

- `?wait_timeout=duration`: Timeout for waiting on a ticket's turn.
  If the ticket isn't processed within this duration, the next ticket is notified.

  Default is `6h`.

- `?accept_timeout=duration`: Timeout for accepting a ticket.
  A ticket is accepted by calling [wait](#fifouuidwaitticket).
  If the ticket isn't accepted within this duration, the next ticket is notified.

  Default is `1m`.

- `?done_timeout=duration`: Timout between ticket acceptance and marking the ticket as done.
  The time a ticket owner has to do the work.
  If the ticket isn't marked as done within this duration, the next ticket is notified.

  Default is `10m`.

- `?unused_destroy_timeout=duration`: Timeout for destroying unused FIFO queues.
  If a FIFO queue is not used within this duration, it will be destroyed.

  Default is `30d`.

- `?allow_overrides=bool`: If set to `true`, the above timeouts can be overridden when creating a ticket.

Returns the following JSON document:

```json
{
  "uuid": "<fifo_uuid>",
}
```

### `/fifo/{uuid}/ticket`

Create a new ticket in the FIFO queue identified by `uuid`.

Accepts `?wait_timeout`, `?accept_timeout` and `?done_timeout`, as documented in [`/fifo/new`](#fifonew) if the fifo queue was created with `?allow_overrides=true`.
Overrides are only valid for a single ticket.

Returns the following JSON document:

```json
{
  "ticket": "<ticket_uuid>",
}
```

### `/fifo/{uuid}/wait/{ticket}`

Wait for ticket `ticket` in the FIFO queue `uuid`.
The call is blocking until the ticket is processed.
Multiple callers can wait for the same ticket.
All will be notified when the ticket is

### `/fifo/{uuid}/done/{ticket}`

Mark the ticket `ticket` as done in the FIFO queue `uuid`.

### Duration parsing

Durations from query parameters are parsed with Go's [`time.ParseDuration`](https://pkg.go.dev/time#ParseDuration), valid examples are `1h45m`, `2h30s`, `10s`, `2d` etc.
A duration of `0` means no timeout.
