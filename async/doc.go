// Package async contains Vial's bounded in-memory executor.
//
// MemoryExecutor loses all operation and idempotency records when the process
// exits. Use a durable AsyncExecutor when that data must survive a restart.
package async
