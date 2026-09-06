package main

import (
	"context"
	"database/sql"
	"embed"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	vial "github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/sqlkit"
)

//go:embed migrations/*.sql
var migrations embed.FS

type createNoteRequest struct {
	Body string `json:"body"`
}

type note struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	app, err := newApp(databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	if err := app.Run(context.Background(), ":8080"); err != nil {
		log.Fatal(err)
	}
}

func newApp(databaseURL string) (*vial.App, error) {
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(20)
	database.SetMaxIdleConns(5)
	database.SetConnMaxLifetime(30 * time.Minute)

	migrator, err := sqlkit.NewMigrator(database, migrations, "migrations")
	if err != nil {
		_ = database.Close()
		return nil, err
	}

	app := vial.New(vial.WithDisallowUnknownJSONFields(true))
	app.OnStart(database.PingContext, migrator.Migrate)
	app.OnStop(func(context.Context) error { return database.Close() })
	app.Health("/live")
	app.Readiness("/ready", database.PingContext)
	app.Post("/notes", func(contextValue *vial.Context) error {
		var request createNoteRequest
		if err := contextValue.BindJSON(&request); err != nil {
			return err
		}
		request.Body = strings.TrimSpace(request.Body)
		if request.Body == "" {
			return vial.BadRequest("body_required", "body is required")
		}

		created := note{Body: request.Body}
		if err := sqlkit.InTx(contextValue.Request().Context(), database, nil, func(transaction *sql.Tx) error {
			if err := transaction.QueryRowContext(
				contextValue.Request().Context(),
				"INSERT INTO notes (body) VALUES ($1) RETURNING id, created_at",
				request.Body,
			).Scan(&created.ID, &created.CreatedAt); err != nil {
				return err
			}
			_, err := transaction.ExecContext(
				contextValue.Request().Context(),
				"INSERT INTO note_events (note_id, action) VALUES ($1, 'created')",
				created.ID,
			)
			return err
		}); err != nil {
			return err
		}
		return contextValue.JSON(http.StatusCreated, created)
	})
	return app, nil
}
