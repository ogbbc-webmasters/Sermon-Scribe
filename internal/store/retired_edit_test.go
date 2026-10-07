package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLegacyEditJobsCannotRunOrRetry(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	if err := st.CreateSermon(Sermon{ID: "legacy", OriginalFilename: "source.wav", UploadedAt: now.Format(time.RFC3339Nano), Stage: "edit", Status: "failed"}); err != nil {
		t.Fatal(err)
	}
	// Fixture only: simulate persisted jobs from the retired backend.
	if err := st.EnqueueJob(NewJob{ID: "old-edit", SermonID: "legacy", Type: "apply_edits", Stage: "edit"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimNextJob(context.Background(), []string{"apply_edits"}, now); !errors.Is(err, ErrNoJob) {
		t.Fatalf("claim old edit = %v", err)
	}
	if _, err := st.db.Exec(`UPDATE jobs SET state='failed' WHERE id='old-edit'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.RetryFailedJob("legacy", now); !errors.Is(err, ErrNotRetryable) {
		t.Fatalf("retry old edit = %v", err)
	}
	job, err := st.GetJob("old-edit")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != "failed" || job.Attempts != 0 {
		t.Fatalf("legacy job mutated: %+v", job)
	}
}

func TestRecoveryRetiresEditJobsWithoutBlockingRegeneration(t *testing.T) {
	for _, state := range []string{"queued", "running"} {
		t.Run(state, func(t *testing.T) {
			st := openTestStore(t)
			now := time.Now()
			if err := st.CreateSermon(Sermon{ID: "legacy", OriginalFilename: "source.wav", UploadedAt: now.Format(time.RFC3339Nano), Stage: "edit", Status: "running"}); err != nil {
				t.Fatal(err)
			}
			if err := st.EnqueueJob(NewJob{ID: "old-edit", SermonID: "legacy", Type: "apply_edits", Stage: "edit"}, now); err != nil {
				t.Fatal(err)
			}
			if _, err := st.db.Exec(`UPDATE jobs SET state=?, attempts=2 WHERE id='old-edit'`, state); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err := st.RecoverRunningJobs(); err != nil {
					t.Fatal(err)
				}
			}
			job, err := st.GetJob("old-edit")
			if err != nil || job.State != "failed" || job.Attempts != 2 || job.LastError == nil {
				t.Fatalf("retired job = %+v, %v", job, err)
			}
			if _, err := st.EnqueueProcessingRerun("legacy", "new-transcription", "transcription", now); err != nil {
				t.Fatalf("regeneration blocked by retired job: %v", err)
			}
		})
	}
}
