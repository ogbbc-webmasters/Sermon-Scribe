package processing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/ogbbc-webmasters/Sermon-Scribe/internal/store"
)

// Record the provider's charge before validating the task output: invalid
// output can still be billed, and a later retry is a separate call.
func doAIRequest(st *store.Store, client *http.Client, req *http.Request, job store.Job, task, model string) (*http.Response, error) {
	resp, requestErr := client.Do(req)
	var envelope struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			Cost *float64 `json:"cost"`
		} `json:"usage"`
	}
	if resp != nil && requestErr == nil {
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
		requestErr = readErr
		// Even an error response can contain usage. Missing/invalid usage
		// stays unknown; it must never silently become a zero-dollar call.
		if json.Unmarshal(body, &envelope) != nil {
			envelope.Usage.Cost = nil
		}
	}
	if envelope.Model != "" {
		model = envelope.Model
	}
	if err := st.RecordAICall(job, task, model, envelope.ID, envelope.Usage.Cost); err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return nil, errors.Join(requestErr, fmt.Errorf("record OpenRouter cost: %w", err))
	}
	return resp, requestErr
}
