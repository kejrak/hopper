// Package audit keeps an append-only JSON Lines log of hopper exec runs so
// a human can review what scripts and AI agents ran on which host. Each run
// writes a "start" record before ssh starts — so a run killed midway still
// leaves a trace — and an "end" record with its exit code; a host refused
// before connecting gets a "refused" record.
package audit

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Record events.
const (
	EventStart   = "start"
	EventEnd     = "end"
	EventRefused = "refused"
)

// Run statuses.
const (
	StatusOK         = "ok"
	StatusFailed     = "failed"
	StatusRefused    = "refused"
	StatusUnfinished = "unfinished" // still running, or hopper was killed
)

// Record is one line of the log.
type Record struct {
	Time       time.Time `json:"time"`
	Event      string    `json:"event"`
	ID         string    `json:"id"`
	Host       string    `json:"host"`
	Group      string    `json:"group,omitempty"`
	Command    []string  `json:"command,omitempty"`
	Dir        string    `json:"dir,omitempty"`
	ExitCode   *int      `json:"exit_code,omitempty"`
	DurationMS *int64    `json:"duration_ms,omitempty"`
	Reason     string    `json:"reason,omitempty"`
}

// Run is one exec invocation assembled from its records.
type Run struct {
	ID         string    `json:"id"`
	Time       time.Time `json:"time"`
	Host       string    `json:"host"`
	Group      string    `json:"group"`
	Command    []string  `json:"command"`
	Dir        string    `json:"dir"`
	Status     string    `json:"status"`
	ExitCode   *int      `json:"exit_code"`
	DurationMS *int64    `json:"duration_ms"`
	Reason     string    `json:"reason,omitempty"`
}

// NewID returns a random 16-hex-digit identifier pairing a run's start and
// end records.
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error since Go 1.24
	return hex.EncodeToString(b)
}

// Append writes r as one JSON line, creating the directory (0700) and file
// (0600) as needed. Each record is a single O_APPEND write, so concurrent
// runs never interleave within a line.
func Append(path string, r Record) error {
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Load reads every record in file order. A missing file yields no records
// and no error; blank or malformed lines are skipped.
func Load(path string) ([]Record, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var records []Record
	reader := bufio.NewReader(f)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var r Record
			if json.Unmarshal(line, &r) == nil && r.Event != "" {
				records = append(records, r)
			}
		}
		if errors.Is(readErr, io.EOF) {
			return records, nil
		}
		if readErr != nil {
			return records, readErr
		}
	}
}

// Runs pairs start and end records by ID and returns one Run per start or
// refused record, in log order. A start without an end is unfinished; an
// end without a start is ignored.
func Runs(records []Record) []Run {
	var runs []Run
	index := make(map[string]int)
	for _, r := range records {
		switch r.Event {
		case EventStart:
			index[r.ID] = len(runs)
			runs = append(runs, Run{ID: r.ID, Time: r.Time, Host: r.Host, Group: r.Group,
				Command: r.Command, Dir: r.Dir, Status: StatusUnfinished})
		case EventRefused:
			runs = append(runs, Run{ID: r.ID, Time: r.Time, Host: r.Host, Group: r.Group,
				Command: r.Command, Dir: r.Dir, Status: StatusRefused, Reason: r.Reason})
		case EventEnd:
			i, ok := index[r.ID]
			if !ok {
				continue
			}
			runs[i].ExitCode = r.ExitCode
			runs[i].DurationMS = r.DurationMS
			runs[i].Status = StatusFailed
			if r.ExitCode != nil && *r.ExitCode == 0 {
				runs[i].Status = StatusOK
			}
		}
	}
	return runs
}
