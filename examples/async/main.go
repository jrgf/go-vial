package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/async"
)

type generateReportRequest struct {
	Name       string `json:"name"`
	DurationMS int    `json:"duration_ms"`
}

type generatedReport struct {
	DownloadURL string `json:"download_url"`
}

func main() {
	address := os.Getenv("ADDR")
	if address == "" {
		address = ":8080"
	}
	if err := newApp().Run(context.Background(), address); err != nil {
		slog.Error("application stopped with an error", "error", err)
		os.Exit(1)
	}
}

func newApp() *vial.App {
	executor := async.NewMemoryExecutor(
		async.WithWorkers(4),
		async.WithQueueSize(32),
		async.WithTaskTimeout(15*time.Second),
	)
	executor.Handle("reports.generate", generateReport)

	app := vial.New(vial.WithDisallowUnknownJSONFields(true))
	app.Async(executor)
	app.Post("/reports", submitReport)
	app.Get("/operations/{id}", vial.OperationStatusHandler(executor, authorizeOperation))
	app.Delete("/operations/{id}", vial.OperationCancelHandler(executor, authorizeOperation))
	app.Get("/metrics/async", vial.AsyncMetricsHandler(executor))
	app.Health("/health")
	app.Readiness("/ready", executor.Ready)
	return app
}

func submitReport(contextValue *vial.Context) error {
	userID, err := requestUserID(contextValue)
	if err != nil {
		return err
	}
	if _, err := vial.ParsePrefer(contextValue.Header("Prefer")); err != nil {
		return err
	}

	var request generateReportRequest
	if err := contextValue.BindJSON(&request); err != nil {
		return err
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" || len(request.Name) > 64 {
		return vial.NewHTTPError(http.StatusBadRequest, "invalid_report_name", "name must contain 1 to 64 characters")
	}
	if request.DurationMS == 0 {
		request.DurationMS = 1500
	}
	if request.DurationMS < 100 || request.DurationMS > 10_000 {
		return vial.NewHTTPError(http.StatusBadRequest, "invalid_duration", "duration_ms must be between 100 and 10000")
	}

	operation, err := contextValue.Async().Submit(contextValue.Request().Context(), vial.SubmitRequest{
		Name:             "reports.generate",
		Payload:          request,
		IdempotencyKey:   contextValue.Header("Idempotency-Key"),
		IdempotencyScope: userID,
		Metadata:         map[string]string{"user_id": userID},
	})
	if err != nil {
		return err
	}
	operation, completed, err := contextValue.Await(operation, 3*time.Second)
	if err != nil {
		return err
	}
	if !completed {
		return contextValue.Accepted(operation)
	}
	if operation.Status == vial.OperationSucceeded {
		return contextValue.JSON(http.StatusCreated, operation.Result)
	}
	return vial.NewHTTPError(http.StatusInternalServerError, "report_generation_failed", "The report could not be generated")
}

func generateReport(contextValue context.Context, job async.Job) (any, error) {
	var request generateReportRequest
	if err := job.Decode(&request); err != nil {
		return nil, err
	}
	ticker := time.NewTicker(time.Duration(request.DurationMS) * time.Millisecond / 10)
	defer ticker.Stop()
	for progress := 10; progress <= 100; progress += 10 {
		select {
		case <-contextValue.Done():
			return nil, contextValue.Err()
		case <-ticker.C:
			if err := job.Progress(contextValue, progress); err != nil {
				return nil, err
			}
		}
	}
	return generatedReport{DownloadURL: "/downloads/" + url.PathEscape(request.Name) + ".csv"}, nil
}

func authorizeOperation(contextValue *vial.Context, operation *vial.Operation) error {
	userID, err := requestUserID(contextValue)
	if err != nil {
		return err
	}
	if operation.Metadata["user_id"] != userID {
		return vial.ErrOperationNotFound
	}
	return nil
}

func requestUserID(contextValue *vial.Context) (string, error) {
	userID := strings.TrimSpace(contextValue.Header("X-User-ID"))
	if userID == "" || len(userID) > 128 {
		return "", vial.NewHTTPError(http.StatusUnauthorized, "authentication_required", "X-User-ID is required")
	}
	return userID, nil
}
