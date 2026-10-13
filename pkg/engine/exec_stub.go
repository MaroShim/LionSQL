package engine

import (
	"context"
	"fmt"
)

// ExecuteSQL parses and runs one or more SQL statements on the default session.
func (c *Coordinator) ExecuteSQL(query string) (*QueryResult, error) {
	return c.ExecuteSessionSQL("default", query)
}

// ExecuteSessionSQL runs one or more SQL statements within an existing or new session.
func (c *Coordinator) ExecuteSessionSQL(sessionID string, query string) (*QueryResult, error) {
	return c.ExecuteSessionSQLContext(context.Background(), sessionID, query)
}

// ExecuteSessionSQLContext runs SQL statements within a specific client session and cancellation context.
func (c *Coordinator) ExecuteSessionSQLContext(ctx context.Context, sessionID string, query string) (*QueryResult, error) {
	// Acquire worker slot from connection pool to prevent thread thrashing
	if c.pool != nil {
		if err := c.pool.AcquireWorker(ctx); err != nil {
			return nil, err
		}
		defer c.pool.ReleaseWorker()
	}

	cs, err := c.GetOrCreateSession(sessionID)
	if err != nil {
		return nil, err
	}

	// Basic execution delegates through coordinator's executor
	_ = cs
	return &QueryResult{
		Success: true,
		Command: "OK",
	}, fmt.Errorf("Coordinator.ExecuteSessionSQLContext: use api.Server or direct executor during transitional migration")
}
