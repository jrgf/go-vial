// Package async provides Vial's bounded, in-memory asynchronous executor.
//
// MemoryExecutor is not durable: pending, running, completed, and idempotency
// records are lost when the process exits. Use it only when losing work is
// acceptable; use a durable AsyncExecutor for critical production workflows.
package async
