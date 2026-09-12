package phone

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"fadecli/internal/catalog"
)

// Job is written durably BEFORE dialing. A timeout/crash must never turn a
// retry into a second booking. One attempt per destination per NY calendar day.
// Intent receipts are not automatically deleted, even on an HTTP error.
type Job struct {
	ID      string    `json:"id"`
	Created time.Time `json:"created"`
	Request Request   `json:"request"`
	CallID  string    `json:"call_id,omitempty"`
}

var jobID = regexp.MustCompile(`^[a-f0-9]{16}$`)

type Jobs struct{ Dir string }

func (j Jobs) Reserve(r Request, now time.Time) (Job, error) {
	sum := sha256.Sum256([]byte(r.Number + "/" + catalog.InShopTime(now).Format("2006-01-02")))
	job := Job{ID: hex.EncodeToString(sum[:8]), Created: now, Request: r}
	if err := os.MkdirAll(j.Dir, 0700); err != nil {
		return job, err
	}
	b, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return job, err
	}
	f, err := os.OpenFile(filepath.Join(j.Dir, job.ID+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return job, fmt.Errorf("already attempted this number today (request %s); use calls status or check the Vapi dashboard, not another call", job.ID)
	}
	if err != nil {
		return job, err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return job, err
	}
	if closeErr != nil {
		return job, closeErr
	}
	return job, syncDir(j.Dir)
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (j Jobs) Read(id string) (Job, error) {
	var job Job
	if !jobID.MatchString(id) {
		return job, errors.New("use the full 16-character request ID from calls list")
	}
	b, err := os.ReadFile(filepath.Join(j.Dir, id+".json"))
	if err != nil {
		return job, err
	}
	if err = json.Unmarshal(b, &job); err != nil {
		return job, err
	}
	if job.ID != id {
		return job, errors.New("call receipt ID mismatch")
	}
	return job, nil
}

func (j Jobs) Attach(id, callID string) error {
	job, err := j.Read(id)
	if err != nil {
		return err
	}
	if !remoteID.MatchString(callID) {
		return errors.New("invalid remote call ID")
	}
	if job.CallID != "" && job.CallID != callID {
		return errors.New("this request already has a different call ID")
	}
	job.CallID = callID
	b, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(j.Dir, ".call-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), filepath.Join(j.Dir, id+".json")); err != nil {
		return err
	}
	return syncDir(j.Dir)
}

func (j Jobs) List() ([]Job, error) {
	entries, err := os.ReadDir(j.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var jobs []Job
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := e.Name()[:len(e.Name())-5]
		if !jobID.MatchString(id) {
			continue
		}
		job, err := j.Read(id)
		if err != nil {
			return nil, fmt.Errorf("call receipt %s: %w", id, err)
		}
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(a, b int) bool { return jobs[a].Created.After(jobs[b].Created) })
	return jobs, nil
}
