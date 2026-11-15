package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"lionsql/pkg/dbmail"

	_ "github.com/lib/pq"
)

func runDbMailWorker(args []string) error {
	fs := flag.NewFlagSet("dbmail-worker", flag.ContinueOnError)
	dbURL := fs.String("db-url", "postgres://127.0.0.1:5433/master?sslmode=disable", "Database connection URL (PostgreSQL wire format)")
	driver := fs.String("driver", "postgres", "database/sql driver name ('postgres')")
	smtpHost := fs.String("smtp-host", "", "SMTP server hostname")
	smtpPort := fs.Int("smtp-port", 587, "SMTP server port (default 587)")
	smtpUser := fs.String("smtp-user", "", "SMTP username")
	smtpPass := fs.String("smtp-pass", "", "SMTP password")
	fromEmail := fs.String("from-email", "", "Default sender email address")
	fromName := fs.String("from-name", "LionSQL DatabaseMail", "Default sender display name")
	useTLS := fs.Bool("tls", true, "Use TLS / STARTTLS")
	pollMs := fs.Int("poll-interval-ms", 1000, "Queue polling interval in milliseconds")
	batchSize := fs.Int("batch-size", 20, "Batch size per polling tick")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *smtpHost == "" {
		return fmt.Errorf("-smtp-host is required for dbmail-worker")
	}

	db, err := sql.Open(*driver, *dbURL)
	if err != nil {
		return fmt.Errorf("failed to connect to database at %s: %w", *dbURL, err)
	}

	cfg := dbmail.Config{
		DBURL:        *dbURL,
		DriverName:   *driver,
		SMTPHost:     *smtpHost,
		SMTPPort:     *smtpPort,
		SMTPUser:     *smtpUser,
		SMTPPass:     *smtpPass,
		FromEmail:    *fromEmail,
		FromName:     *fromName,
		UseTLS:       *useTLS,
		PollInterval: time.Duration(*pollMs) * time.Millisecond,
		BatchSize:    *batchSize,
	}

	client := dbmail.NewSQLDBClient(db)
	worker := dbmail.NewWorker(cfg, client)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Printf("[DatabaseMail] Worker started. Polling %s at interval %v...", *dbURL, cfg.PollInterval)
	if err := worker.Start(ctx); err != nil {
		_ = client.Close()
		return err
	}

	<-ctx.Done()
	log.Printf("[DatabaseMail] Shutting down worker...")
	worker.Stop()
	log.Printf("[DatabaseMail] Worker stopped cleanly.")
	return nil
}
