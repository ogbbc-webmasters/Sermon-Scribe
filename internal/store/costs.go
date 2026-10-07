package store

// AICost groups all calls, including retries, by task and model. Missing costs
// are counted separately from explicitly free calls.
type AICost struct {
	Task         string  `json:"task"`
	Model        string  `json:"model"`
	Calls        int     `json:"calls"`
	CostUSD      float64 `json:"cost_usd"`
	UnknownCosts int     `json:"unknown_costs"`
}

func (s *Store) RecordAICall(job Job, task, model, requestID string, cost *float64) error {
	_, err := s.db.Exec(`INSERT INTO ai_calls (sermon_id, job_id, task, model, request_id, cost_usd) VALUES (?, ?, ?, ?, ?, ?)`, job.SermonID, job.ID, task, model, requestID, cost)
	return err
}
