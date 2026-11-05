package core

// QueryResp represents the generic query execution result.
type QueryResp struct {
	Success bool             `json:"success"`
	Columns []string         `json:"columns"`
	Rows    []map[string]any `json:"rows"`
	Error   string           `json:"error,omitempty"`
}

// Backend defines the database execution abstraction for sprint tasks.
type Backend interface {
	Name() string
	Init() error
	Exec(sql string) (*QueryResp, error)
	Close() error
}

// ParsedTask represents a task record parsed from TASKS.md.
type ParsedTask struct {
	ID          string
	Domain      string
	Title       string
	Priority    int
	Status      string
	PlanDoc     string
	Metrics     string
	TargetFiles string
	Phases      string
	Description string
	UpdatedAt   string
}
