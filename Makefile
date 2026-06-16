.PHONY: setup download render build

OUTPUT     ?= output
SEQUENCE_ID ?=
FPS         ?= 3

setup:
	@echo "VIAM_AUTH_TOKEN=$$(viam login print-access-token)" > .env

download:
	@test -n "$(SEQUENCE_ID)" || (echo "error: SEQUENCE_ID is required  →  make download SEQUENCE_ID=<id>" && exit 1)
	go run ./cmd/download --sequence-id $(SEQUENCE_ID) --output $(OUTPUT)

render:
	go run ./cmd/render --output $(OUTPUT) --fps $(FPS)

build:
	go build -o bin/download ./cmd/download
	go build -o bin/render   ./cmd/render
