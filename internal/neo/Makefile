DATA_DIR  ?= neo_data
ADDR      ?= :9090
DEV_PORT  ?= 3000

.PHONY: dev build run

dev: build
	cd frontend && DEV_PORT=$(DEV_PORT) NEO_BACKEND=http://127.0.0.1$(ADDR) bun run dev &
	go run ../../cmd/neo --addr $(ADDR) --data $(DATA_DIR)

build:
	cd frontend && bun run build

run: build
	go run ../../cmd/neo --addr $(ADDR) --data $(DATA_DIR)
