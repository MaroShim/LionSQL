package dbmail

import (
	"time"
)

// Config configures the DatabaseMail external worker.
type Config struct {
	// DBURL specifies the connection string to the LionSQL instance (e.g. "postgres://localhost:5433/master?sslmode=disable").
	DBURL string `json:"db_url"`

	// DriverName specifies the database/sql driver name ("postgres" or "sqlserver"). Default is "postgres".
	DriverName string `json:"driver_name"`

	// SMTPHost is the hostname or IP address of the outbound SMTP server.
	SMTPHost string `json:"smtp_host"`

	// SMTPPort is the port of the outbound SMTP server (e.g. 587 or 25).
	SMTPPort int `json:"smtp_port"`

	// SMTPUser is the username for SMTP authentication.
	SMTPUser string `json:"smtp_user"`

	// SMTPPass is the password for SMTP authentication.
	SMTPPass string `json:"smtp_pass"`

	// FromEmail is the default sender email address when not specified per mail item.
	FromEmail string `json:"from_email"`

	// FromName is the display name of the sender.
	FromName string `json:"from_name"`

	// UseTLS specifies whether to require STARTTLS or TLS encryption.
	UseTLS bool `json:"use_tls"`

	// PollInterval is the sleep duration between queue polling ticks. Default is 1s.
	PollInterval time.Duration `json:"poll_interval"`

	// BatchSize is the maximum number of mail items to fetch per poll. Default is 20.
	BatchSize int `json:"batch_size"`
}

// DefaultConfig returns safe production defaults for the DatabaseMail worker.
func DefaultConfig() Config {
	return Config{
		DriverName:   "postgres",
		SMTPPort:     587,
		UseTLS:       true,
		PollInterval: 1 * time.Second,
		BatchSize:    20,
	}
}
