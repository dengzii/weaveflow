package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/internal/memory"

	"github.com/gin-gonic/gin"
)

type memoryWriteRequest struct {
	Value              any               `json:"value,omitempty"`
	Content            string            `json:"content,omitempty"`
	Text               string            `json:"text,omitempty"`
	ContentMetadata    map[string]string `json:"content_metadata,omitempty"`
	SourceRunID        string            `json:"source_run_id,omitempty"`
	SourceGraphSession string            `json:"source_graph_session_id,omitempty"`
	RetainUntil        *time.Time        `json:"retain_until,omitempty"`
	SourceRetention    bool              `json:"source_retention,omitempty"`
	ExpectedVersion    string            `json:"expected_version,omitempty"`
}

type purgeExpiredMemoryRequest struct {
	Now   time.Time `json:"now,omitempty"`
	Limit int       `json:"limit,omitempty"`
}

func (s *Server) memoryStoreOrError(c *gin.Context) (memory.Store, bool) {
	if s == nil || s.memoryStore == nil {
		writeError(c, statusForError(errMemoryStoreNotConfigured), errMemoryStoreNotConfigured)
		return nil, false
	}
	return s.memoryStore, true
}

func (s *Server) handlePutMemory(c *gin.Context) {
	store, ok := s.memoryStoreOrError(c)
	if !ok {
		return
	}
	namespace, ok := requirePathParam(c, "namespace")
	if !ok {
		return
	}
	key, ok := requirePathParam(c, "key")
	if !ok {
		return
	}
	var payload memoryWriteRequest
	if err := decodeRunRequest(c, &payload); err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	record, err := store.Put(c.Request.Context(), memory.Namespace(namespace), memory.MemoryRecord{
		Key:                key,
		Value:              payload.Value,
		Content:            payload.Content,
		Text:               payload.Text,
		ContentMetadata:    payload.ContentMetadata,
		SourceRunID:        payload.SourceRunID,
		SourceGraphSession: payload.SourceGraphSession,
		RetainUntil:        payload.RetainUntil,
		SourceRetention:    payload.SourceRetention,
	}, strings.TrimSpace(payload.ExpectedVersion))
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, record)
}

func (s *Server) handleGetMemory(c *gin.Context) {
	store, ok := s.memoryStoreOrError(c)
	if !ok {
		return
	}
	namespace, ok := requirePathParam(c, "namespace")
	if !ok {
		return
	}
	key, ok := requirePathParam(c, "key")
	if !ok {
		return
	}
	record, err := store.Get(c.Request.Context(), memory.Namespace(namespace), key)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, record)
}

func (s *Server) handleDeleteMemory(c *gin.Context) {
	store, ok := s.memoryStoreOrError(c)
	if !ok {
		return
	}
	namespace, ok := requirePathParam(c, "namespace")
	if !ok {
		return
	}
	key, ok := requirePathParam(c, "key")
	if !ok {
		return
	}
	expectedVersion, err := optionalStringQuery(c, "expected_version")
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	if expectedVersion == "" {
		writeError(c, statusForRequestError(invalidRequestf("expected_version is required")), invalidRequestf("expected_version is required"))
		return
	}
	if err := store.Delete(c.Request.Context(), memory.Namespace(namespace), key, expectedVersion); err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleListMemory(c *gin.Context) {
	store, ok := s.memoryStoreOrError(c)
	if !ok {
		return
	}
	namespace, ok := requirePathParam(c, "namespace")
	if !ok {
		return
	}
	options, err := memoryListOptions(c)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	page, err := store.List(c.Request.Context(), memory.Namespace(namespace), options)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, page)
}

func (s *Server) handleSearchMemory(c *gin.Context) {
	store, ok := s.memoryStoreOrError(c)
	if !ok {
		return
	}
	namespace, ok := requirePathParam(c, "namespace")
	if !ok {
		return
	}
	text, err := optionalStringQuery(c, "text")
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	limit, cursor, includeExpired, err := memoryPageQuery(c)
	if err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	page, err := store.Search(c.Request.Context(), memory.Namespace(namespace), memory.MemoryQuery{
		Text: text, Limit: limit, Cursor: cursor, IncludeExpired: includeExpired,
	})
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, page)
}

func (s *Server) handlePurgeExpiredMemory(c *gin.Context) {
	store, ok := s.memoryStoreOrError(c)
	if !ok {
		return
	}
	var payload purgeExpiredMemoryRequest
	if err := decodeRunRequest(c, &payload); err != nil {
		writeError(c, statusForRequestError(err), err)
		return
	}
	if payload.Limit < 0 {
		err := invalidRequestf("limit must not be negative")
		writeError(c, statusForRequestError(err), err)
		return
	}
	removed, err := store.PurgeExpired(c.Request.Context(), payload.Now, payload.Limit)
	if err != nil {
		writeError(c, statusForError(err), err)
		return
	}
	writeData(c, http.StatusOK, map[string]int{"removed": removed})
}

func memoryListOptions(c *gin.Context) (memory.MemoryListOptions, error) {
	limit, cursor, includeExpired, err := memoryPageQuery(c)
	if err != nil {
		return memory.MemoryListOptions{}, err
	}
	return memory.MemoryListOptions{Limit: limit, Cursor: cursor, IncludeExpired: includeExpired}, nil
}

func memoryPageQuery(c *gin.Context) (int, string, bool, error) {
	limit, err := positiveIntQuery(c, "limit", 50, 500)
	if err != nil {
		return 0, "", false, err
	}
	cursor, err := optionalStringQuery(c, "cursor")
	if err != nil {
		return 0, "", false, err
	}
	includeExpired := false
	value, err := optionalStringQuery(c, "include_expired")
	if err != nil {
		return 0, "", false, err
	}
	if value != "" {
		includeExpired, err = strconv.ParseBool(value)
		if err != nil {
			return 0, "", false, invalidRequestf("include_expired must be a boolean")
		}
	}
	return limit, cursor, includeExpired, nil
}
