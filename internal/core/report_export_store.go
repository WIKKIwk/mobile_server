package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type ReportExportStore struct {
	path   string
	mu     sync.Mutex
	loaded bool
	cache  map[string]ReportExportRecord
}

func NewReportExportStore(path string) *ReportExportStore {
	return &ReportExportStore{path: path}
}

func (s *ReportExportStore) Get(reportID string) (ReportExportRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	all, err := s.loadAllLocked()
	if err != nil {
		return ReportExportRecord{}, err
	}
	return all[reportID], nil
}

func (s *ReportExportStore) Put(record ReportExportRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	all, err := s.loadAllLocked()
	if err != nil {
		return err
	}
	all[record.ReportID] = record
	return s.writeAllLocked(all)
}

func (s *ReportExportStore) loadAllLocked() (map[string]ReportExportRecord, error) {
	if s.loaded {
		if s.cache == nil {
			s.cache = map[string]ReportExportRecord{}
		}
		return s.cache, nil
	}
	all, err := s.readAllLocked()
	if err != nil {
		return nil, err
	}
	s.cache = all
	s.loaded = true
	return s.cache, nil
}

func (s *ReportExportStore) readAllLocked() (map[string]ReportExportRecord, error) {
	if _, err := os.Stat(s.path); err != nil {
		if os.IsNotExist(err) {
			return map[string]ReportExportRecord{}, nil
		}
		return nil, err
	}

	raw, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return map[string]ReportExportRecord{}, nil
	}

	var data map[string]ReportExportRecord
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	if data == nil {
		data = map[string]ReportExportRecord{}
	}
	return data, nil
}

func (s *ReportExportStore) writeAllLocked(data map[string]ReportExportRecord) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.path), "report-exports-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return err
	}
	s.cache = cloneReportExportMap(data)
	s.loaded = true
	return nil
}

func cloneReportExportMap(input map[string]ReportExportRecord) map[string]ReportExportRecord {
	cloned := make(map[string]ReportExportRecord, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}
