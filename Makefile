.PHONY: setup download render markers detect-single detect-dir build

OUTPUT      ?= output
SEQUENCE_ID ?=
FPS         ?= 3
PARAMS      ?=
TABULAR     ?=
PART_ID     ?=
ORG_ID      ?=
START       ?=
END         ?=
MODEL_DIR   ?= omni-detector-fcos-0_0_4
IMAGE       ?=
DIR         ?=
CONFIDENCE  ?= 0.6
DETECT      ?=

setup:
	@echo "VIAM_AUTH_TOKEN=$$(viam login print-access-token)" > .env

# download supports two mutually exclusive modes: a whole recorded sequence
# (SEQUENCE_ID) or a time range (START+END, needs ORG_ID). Both require
# PART_ID, since data lands under output/<part-id>/<hash-of-params>/ and is
# skipped (with a message) if that hash directory already exists.
download:
	@test -n "$(PART_ID)" || (echo "error: PART_ID is required  →  make download PART_ID=<id> SEQUENCE_ID=<id>" && exit 1)
	@test -n "$(SEQUENCE_ID)$(START)$(END)" || (echo "error: SEQUENCE_ID or START+END is required" && exit 1)
	go run ./cmd/download --part-id $(PART_ID) --output $(OUTPUT) \
		$(if $(SEQUENCE_ID),--sequence-id $(SEQUENCE_ID),) \
		$(if $(START),--start $(START),) $(if $(END),--end $(END),) \
		$(if $(ORG_ID),--org-id $(ORG_ID),)

# point OUTPUT at the specific download to render, e.g.
# make render OUTPUT=output/<part-id>/<hash>
render:
	go run ./cmd/render --output $(OUTPUT) --fps $(FPS) $(if $(PARAMS),--params $(PARAMS),) $(if $(TABULAR),--tabular $(TABULAR),)

# ML detection is opt-in: pass DETECT=1 to also run the fish detector over the
# fetched images/sonar frames (make markers PART_ID=<id> DETECT=1).
markers:
	@test -n "$(PART_ID)" || (echo "error: PART_ID is required  →  make markers PART_ID=<id>" && exit 1)
	go run ./cmd/markerplayback --part-id $(PART_ID) --output $(OUTPUT) \
		$(if $(ORG_ID),--org-id $(ORG_ID),) $(if $(START),--start $(START),) $(if $(END),--end $(END),) \
		$(if $(DETECT),--detect --model-dir $(MODEL_DIR) --confidence $(CONFIDENCE),)

detect-single:
	@test -n "$(IMAGE)" || (echo "error: IMAGE is required  →  make detect-single IMAGE=<path>" && exit 1)
	go run ./cmd/detect --model-dir $(MODEL_DIR) --image $(IMAGE) --confidence $(CONFIDENCE)

detect-dir:
	@test -n "$(DIR)" || (echo "error: DIR is required  →  make detect-dir DIR=<path>" && exit 1)
	go run ./cmd/detect --model-dir $(MODEL_DIR) --image $(DIR) --confidence $(CONFIDENCE)

build:
	go build -o bin/download       ./cmd/download
	go build -o bin/render         ./cmd/render
	go build -o bin/markerplayback ./cmd/markerplayback
	go build -o bin/detect         ./cmd/detect


# make markers PART_ID=f79e293c-612f-496b-b26d-5b8bbaab6524 ORG_ID=4a0a99c7-e680-4cb5-acb1-0bd21449b455 START=2026-07-04T04:00:00Z END=2026-07-06T04:00:00Z (checkmate)
# make markers PART_ID=ce6d0f26-aeea-48dd-be34-81e4db0f807e ORG_ID=4a0a99c7-e680-4cb5-acb1-0bd21449b455 (synth sim)
# add DETECT=1 to either of the above to also run ML fish detection on the fetched images/sonar frames