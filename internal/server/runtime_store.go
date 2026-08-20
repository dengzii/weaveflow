package server

import (
	"os"
	"path/filepath"

	filestore "github.com/dengzii/weaveflow/internal/runtimestore/file"
	sqlitestore "github.com/dengzii/weaveflow/internal/runtimestore/sqlite"
	"github.com/dengzii/weaveflow/runtime"
)

func openCachedRuntimeStore(baseDir, backend string) (defaultRuntimeStore, error) {
	switch backend {
	case RuntimeStoreSQLite:
		return sqlitestore.Open(filepath.Join(baseDir, "runtime.db"))
	default:
		return filestore.Open(baseDir)
	}
}

func openCachedRuntimeReader(baseDir string) (runtime.ExecutionReader, error) {
	sqlitePath := filepath.Join(baseDir, "runtime.db")
	if _, err := os.Stat(sqlitePath); err == nil {
		reader, err := sqlitestore.OpenReader(sqlitePath)
		if err != nil {
			return nil, err
		}
		return reader.ExecutionReader(), nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(baseDir, "execution")); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	reader, err := filestore.OpenReader(baseDir)
	if err != nil {
		return nil, err
	}
	return reader.ExecutionReader(), nil
}
