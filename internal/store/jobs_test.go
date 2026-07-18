package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func seedNormalizeJob(t *testing.T, st *Store, sermonID, jobID string, now time.Time) Sermon {
	t.Helper()
	sm := Sermon{
		ID: sermonID, OriginalFilename: "sermon.wav",
		UploadedAt: now.UTC().Format(time.RFC3339Nano),
		Stage:      "upload", Status: "pending",
	}
	if err := st.CreateSermon(sm); err != nil {
		t.Fatal(err)
	}
	if err := st.StartUpload(sermonID); err != nil {
		t.Fatal(err)
	}
	got, err := st.CompleteUpload(sermonID, jobID, now)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestCompleteUploadEnqueuesNormalization(t *testing.T) {
	st := openTestStore(t)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

	sm := seedNormalizeJob(t, st, "sermon-1", "job-1", now)
	if sm.Stage != "normalization" || sm.Status != "pending" || sm.Progress != 0 {
		t.Fatalf("sermon state = %s/%s progress %d", sm.Stage, sm.Status, sm.Progress)
	}
	job, err := st.GetJob("job-1")
	if err != nil {
		t.Fatal(err)
	}
	if job.Type != "normalize" || job.Stage != "normalization" || job.State != "queued" || job.Attempts != 0 {
		t.Fatalf("unexpected job: %+v", job)
	}
	if job.Parameters != `{"gate_adjustment":0,"volume_adjustment":0}` ||
		sm.NormalizationGateAdjustment != 0 || sm.NormalizationVolumeAdjustment != 0 {
		t.Fatalf("normalization defaults = parameters %q, adjustments %d/%d",
			job.Parameters, sm.NormalizationGateAdjustment, sm.NormalizationVolumeAdjustment)
	}
}

func TestEnqueueNormalizationRerun(t *testing.T) {
	st := openTestStore(t)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	seedNormalizeJob(t, st, "sermon-1", "job-1", now)

	job, err := st.ClaimNextJob(context.Background(), []string{"normalize"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CompleteJob(job, nil, now); err != nil {
		t.Fatal(err)
	}

	parameters := `{"gate_adjustment":1,"volume_adjustment":-1}`
	sm, err := st.EnqueueNormalizationRerun("sermon-1", "job-2", parameters, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if sm.Stage != "normalization" || sm.Status != "pending" || sm.Progress != 0 {
		t.Fatalf("rerun sermon = %+v", sm)
	}
	queued, err := st.GetCurrentJob("sermon-1")
	if err != nil {
		t.Fatal(err)
	}
	if queued.ID != "job-2" || queued.Parameters != parameters || queued.State != "queued" {
		t.Fatalf("rerun job = %+v", queued)
	}
	if _, err := st.EnqueueNormalizationRerun("sermon-1", "job-3", parameters, now); !errors.Is(err, ErrNotRerunnable) {
		t.Fatalf("duplicate rerun error = %v, want ErrNotRerunnable", err)
	}

	if err := st.SetNormalizationAdjustments("sermon-1", 1, -1); err != nil {
		t.Fatal(err)
	}
	sm, err = st.GetSermon("sermon-1")
	if err != nil {
		t.Fatal(err)
	}
	if sm.NormalizationGateAdjustment != 1 || sm.NormalizationVolumeAdjustment != -1 {
		t.Fatalf("stored adjustments = %d/%d, want 1/-1",
			sm.NormalizationGateAdjustment, sm.NormalizationVolumeAdjustment)
	}
}

func TestClaimNextJobIsAtomicAndFiltersTypes(t *testing.T) {
	st := openTestStore(t)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	seedNormalizeJob(t, st, "sermon-1", "job-1", now)

	if _, err := st.ClaimNextJob(context.Background(), []string{"transcribe"}, now); !errors.Is(err, ErrNoJob) {
		t.Fatalf("wrong type claim error = %v, want ErrNoJob", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := st.ClaimNextJob(context.Background(), []string{"normalize"}, now)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	claimed, empty := 0, 0
	for err := range results {
		switch {
		case err == nil:
			claimed++
		case errors.Is(err, ErrNoJob):
			empty++
		default:
			t.Fatalf("claim error: %v", err)
		}
	}
	if claimed != 1 || empty != 1 {
		t.Fatalf("claimed=%d empty=%d, want 1/1", claimed, empty)
	}
	job, err := st.GetJob("job-1")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != "running" || job.Attempts != 1 {
		t.Fatalf("claimed job = %+v", job)
	}
	sm, err := st.GetSermon("sermon-1")
	if err != nil {
		t.Fatal(err)
	}
	if sm.Status != "running" {
		t.Fatalf("sermon status = %q, want running", sm.Status)
	}
}

func TestProgressFailureRetryAndCompletionLifecycle(t *testing.T) {
	st := openTestStore(t)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	seedNormalizeJob(t, st, "sermon-1", "job-1", now)

	job, err := st.ClaimNextJob(context.Background(), []string{"normalize"}, now)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := `{"part":1}`
	sm, err := st.UpdateJobProgress(job.ID, 40, &checkpoint, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if sm.Progress != 40 {
		t.Fatalf("progress = %d, want 40", sm.Progress)
	}

	sm, err = st.RequeueJob(job, "temporary", now.Add(time.Minute), now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if sm.Status != "running" {
		t.Fatalf("status during backoff = %q, want running", sm.Status)
	}
	if _, err := st.ClaimNextJob(context.Background(), []string{"normalize"}, now.Add(30*time.Second)); !errors.Is(err, ErrNoJob) {
		t.Fatalf("early claim error = %v, want ErrNoJob", err)
	}

	job, err = st.ClaimNextJob(context.Background(), []string{"normalize"}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if job.Attempts != 2 || job.Checkpoint == nil || *job.Checkpoint != checkpoint {
		t.Fatalf("second claim = %+v", job)
	}
	if _, err := st.RequeueJob(job, "temporary again", now.Add(2*time.Minute), now.Add(61*time.Second)); err != nil {
		t.Fatal(err)
	}
	job, err = st.ClaimNextJob(context.Background(), []string{"normalize"}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if job.Attempts != MaxJobAttempts() {
		t.Fatalf("attempts = %d, want %d", job.Attempts, MaxJobAttempts())
	}
	sm, err = st.FailJob(job, "permanent", now.Add(121*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if sm.Status != "failed" || sm.Error == nil || *sm.Error != "permanent" {
		t.Fatalf("failed sermon = %+v", sm)
	}

	errorsLog, err := st.ListJobErrors(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(errorsLog) != 3 {
		t.Fatalf("error history length = %d, want 3", len(errorsLog))
	}

	job, sm, err = st.RetryFailedJob("sermon-1", now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if job.State != "queued" || job.Attempts != 0 || job.Checkpoint == nil {
		t.Fatalf("retried job = %+v", job)
	}
	if sm.Status != "pending" || sm.Error != nil {
		t.Fatalf("retried sermon = %+v", sm)
	}

	job, err = st.ClaimNextJob(context.Background(), []string{"normalize"}, now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	sm, err = st.CompleteJob(job, nil, now.Add(181*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if sm.Stage != "normalization" || sm.Status != "done" || sm.Progress != 100 || sm.Error != nil {
		t.Fatalf("completed sermon = %+v", sm)
	}
}

func TestOpenRecoversRunningJobs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	seedNormalizeJob(t, st, "sermon-1", "job-1", now)
	if _, err := st.ClaimNextJob(context.Background(), []string{"normalize"}, now); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	job, err := st.GetJob("job-1")
	if err != nil {
		t.Fatal(err)
	}
	sm, err := st.GetSermon("sermon-1")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != "queued" || sm.Status != "pending" {
		t.Fatalf("recovered job/sermon = %s/%s", job.State, sm.Status)
	}
	if job.Attempts != 0 {
		t.Fatalf("recovered attempts = %d, want 0", job.Attempts)
	}
}

func TestDiscardInterruptedUploads(t *testing.T) {
	st := openTestStore(t)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	for _, item := range []struct {
		id     string
		status string
	}{
		{id: "pending-upload", status: "pending"},
		{id: "running-upload", status: "running"},
	} {
		if err := st.CreateSermon(Sermon{
			ID: item.id, OriginalFilename: item.id + ".wav",
			UploadedAt: now.Format(time.RFC3339Nano),
			Stage:      "upload", Status: item.status,
		}); err != nil {
			t.Fatal(err)
		}
	}
	seedNormalizeJob(t, st, "complete-upload", "job-1", now)

	ids, err := st.DiscardInterruptedUploads()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("discarded ids = %v, want two interrupted uploads", ids)
	}
	for _, id := range []string{"pending-upload", "running-upload"} {
		if _, err := st.GetSermon(id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetSermon(%q) error = %v, want ErrNotFound", id, err)
		}
	}
	if _, err := st.GetSermon("complete-upload"); err != nil {
		t.Fatalf("completed upload was discarded: %v", err)
	}
}

func TestMigrationQueuesCompletedUploadsFromBeforePipeline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL); INSERT INTO schema_version VALUES (2);`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"001_create_sermons.sql", "002_processing_jobs.sql"} {
		migration, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	if _, err := db.Exec(
		`INSERT INTO sermons (id, original_filename, uploaded_at, stage, status)
		 VALUES (?, ?, ?, 'upload', 'done')`,
		"legacy-sermon", "legacy.wav", now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sm, err := st.GetSermon("legacy-sermon")
	if err != nil {
		t.Fatal(err)
	}
	if sm.Stage != "normalization" || sm.Status != "pending" {
		t.Fatalf("legacy sermon state = %s/%s", sm.Stage, sm.Status)
	}
	job, err := st.GetCurrentJob(sm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Type != "normalize" || job.State != "queued" {
		t.Fatalf("legacy normalize job = %+v", job)
	}
}
