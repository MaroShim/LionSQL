package engine

// RegisterGlobalTemp records creatorID as the owner of a global temporary table (##name),
// so it is dropped when the creating session closes.
func (c *Coordinator) RegisterGlobalTemp(tableName, creatorID string) {
	if creatorID == "" {
		creatorID = "default"
	}
	c.globalTempMu.Lock()
	defer c.globalTempMu.Unlock()
	if c.globalTempCreators == nil {
		c.globalTempCreators = make(map[string]string)
	}
	c.globalTempCreators[tableName] = creatorID
}

// UnregisterGlobalTemp removes ownership tracking for a global temporary table.
func (c *Coordinator) UnregisterGlobalTemp(tableName string) {
	c.globalTempMu.Lock()
	defer c.globalTempMu.Unlock()
	delete(c.globalTempCreators, tableName)
}

// SnapshotSessions returns a point-in-time copy of all registered sessions keyed by ID.
func (c *Coordinator) SnapshotSessions() map[string]*ClientSession {
	c.sessMu.RLock()
	defer c.sessMu.RUnlock()
	out := make(map[string]*ClientSession, len(c.sessions))
	for id, cs := range c.sessions {
		out[id] = cs
	}
	return out
}

// SessionCount returns the number of registered client sessions.
func (c *Coordinator) SessionCount() int {
	c.sessMu.RLock()
	defer c.sessMu.RUnlock()
	return len(c.sessions)
}

// FindSessionBySPID returns the session identified by spid, or nil if none exists.
func (c *Coordinator) FindSessionBySPID(spid int) *ClientSession {
	c.sessMu.RLock()
	defer c.sessMu.RUnlock()
	for _, cs := range c.sessions {
		if cs.SPID == spid {
			return cs
		}
	}
	return nil
}
