package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (s *Server) recoverGraphCommits(ctx context.Context) error {
	if s == nil || s.triggers == nil {
		return nil
	}
	graphsDir := filepath.Join(s.baseDir, "graphs")
	entries, err := os.ReadDir(graphsDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read graph commit journals: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		journalPath := filepath.Join(graphsDir, entry.Name(), ".commit.json")
		data, err := os.ReadFile(journalPath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read graph commit journal %q: %w", entry.Name(), err)
		}
		var journal graphCommitJournal
		if err := decodeStrictJSON(data, &journal); err != nil {
			return fmt.Errorf("decode graph commit journal %q: %w", entry.Name(), err)
		}
		journal.GraphID = strings.TrimSpace(journal.GraphID)
		journal.GraphSessionID = strings.TrimSpace(journal.GraphSessionID)
		if journal.GraphID == "" || journal.GraphSessionID == "" {
			return fmt.Errorf("graph commit journal %q is incomplete", entry.Name())
		}
		finalDir := s.uploadedGraphBaseDir(journal.GraphID, journal.GraphSessionID)
		_, finalErr := os.Stat(filepath.Join(finalDir, "graph.json"))
		if os.IsNotExist(finalErr) {
			if _, err := s.triggers.ReplaceGraph(ctx, journal.GraphID, journal.PreviousTriggers); err != nil {
				return fmt.Errorf("restore graph %q triggers: %w", journal.GraphID, err)
			}
		} else if finalErr != nil {
			return fmt.Errorf("inspect graph %q published session: %w", journal.GraphID, finalErr)
		} else if err := s.writeGraphCommitReceipt(journal.GraphID, journal.RequestID, journal.Response); err != nil {
			return fmt.Errorf("recover graph %q commit receipt: %w", journal.GraphID, err)
		}
		candidateDir := filepath.Join(filepath.Dir(finalDir), ".candidates", journal.GraphSessionID)
		if err := os.RemoveAll(candidateDir); err != nil {
			return fmt.Errorf("remove graph %q candidate: %w", journal.GraphID, err)
		}
		if err := os.Remove(journalPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove graph %q commit journal: %w", journal.GraphID, err)
		}
	}
	return nil
}
