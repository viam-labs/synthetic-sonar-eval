.PHONY: setup download render markers build

OUTPUT      ?= output
SEQUENCE_ID ?=
FPS         ?= 3
PARAMS      ?=
TABULAR     ?=
PART_ID     ?=
ORG_ID      ?=
START       ?=
END         ?=

setup:
	@echo "VIAM_AUTH_TOKEN=$$(viam login print-access-token)" > .env

download:
	@test -n "$(SEQUENCE_ID)" || (echo "error: SEQUENCE_ID is required  →  make download SEQUENCE_ID=<id>" && exit 1)
	go run ./cmd/download --sequence-id $(SEQUENCE_ID) --output $(OUTPUT)

render:
	go run ./cmd/render --output $(OUTPUT) --fps $(FPS) $(if $(PARAMS),--params $(PARAMS),) $(if $(TABULAR),--tabular $(TABULAR),)

markers:
	@test -n "$(PART_ID)" || (echo "error: PART_ID is required  →  make markers PART_ID=<id>" && exit 1)
	go run ./cmd/markerplayback --part-id $(PART_ID) --output $(OUTPUT) $(if $(ORG_ID),--org-id $(ORG_ID),) $(if $(START),--start $(START),) $(if $(END),--end $(END),)

build:
	go build -o bin/download       ./cmd/download
	go build -o bin/render         ./cmd/render
	go build -o bin/markerplayback ./cmd/markerplayback
