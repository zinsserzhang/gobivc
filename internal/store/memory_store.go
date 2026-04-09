package store

import (
	"fmt"
	"sort"
	"sync"

	"github.com/zinsserzhang/gobivc/internal/model"
)

// ReportStore defines the storage interface for reports.
type ReportStore interface {
	Save(report *model.Report) error
	Get(id string) (*model.Report, error)
	List() ([]*model.Report, error)
	Delete(id string) error
	Update(report *model.Report) error
}

// MemoryStore is an in-memory implementation of ReportStore.
type MemoryStore struct {
	mu      sync.RWMutex
	reports map[string]*model.Report
}

// NewMemoryStore creates a new in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		reports: make(map[string]*model.Report),
	}
}

func (s *MemoryStore) Save(report *model.Report) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.reports[report.ID]; exists {
		return fmt.Errorf("report %s already exists", report.ID)
	}
	// Store a copy to prevent external mutation
	copy := *report
	s.reports[report.ID] = &copy
	return nil
}

func (s *MemoryStore) Get(id string) (*model.Report, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	report, ok := s.reports[id]
	if !ok {
		return nil, fmt.Errorf("report %s not found", id)
	}
	copy := *report
	return &copy, nil
}

func (s *MemoryStore) List() ([]*model.Report, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*model.Report, 0, len(s.reports))
	for _, r := range s.reports {
		copy := *r
		result = append(result, &copy)
	}
	// Sort by creation time, newest first
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result, nil
}

func (s *MemoryStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.reports[id]; !ok {
		return fmt.Errorf("report %s not found", id)
	}
	delete(s.reports, id)
	return nil
}

func (s *MemoryStore) Update(report *model.Report) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.reports[report.ID]; !ok {
		return fmt.Errorf("report %s not found", report.ID)
	}
	copy := *report
	s.reports[report.ID] = &copy
	return nil
}
