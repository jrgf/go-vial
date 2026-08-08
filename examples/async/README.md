# Async operations example

Run the in-memory example:

```sh
go run ./examples/async
```

Submit a slow report. The response is `202 Accepted`; copy its `id`:

```sh
curl -i -X POST http://localhost:8080/reports \
  -H 'Content-Type: application/json' \
  -H 'X-User-ID: demo-user' \
  -H 'Prefer: respond-async' \
  -H 'Idempotency-Key: sales-report-1' \
  -d '{"name":"sales","duration_ms":5000}'
```

Poll or cancel it using the same authenticated owner:

```sh
curl -i -H 'X-User-ID: demo-user' http://localhost:8080/operations/OPERATION_ID
curl -i -X DELETE -H 'X-User-ID: demo-user' http://localhost:8080/operations/OPERATION_ID
```

Allow a fast operation to complete inline for up to one second:

```sh
curl -i -X POST http://localhost:8080/reports \
  -H 'Content-Type: application/json' \
  -H 'X-User-ID: demo-user' \
  -H 'Prefer: respond-async, wait=1' \
  -d '{"name":"fast","duration_ms":250}'
```

Inspect readiness and OpenMetrics output:

```sh
curl -i http://localhost:8080/ready
curl http://localhost:8080/metrics/async
```

This example uses the bounded in-memory executor. Pending, running, completed,
and idempotency records are lost when the process stops; use a durable executor
for work that must survive restarts.
