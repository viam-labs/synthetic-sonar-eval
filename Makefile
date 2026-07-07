.PHONY: setup download render markers detect build

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
CONFIDENCE  ?= 0.6
DETECT      ?=

setup:
	@echo "VIAM_AUTH_TOKEN=$$(viam login print-access-token)" > .env

download:
	@test -n "$(SEQUENCE_ID)" || (echo "error: SEQUENCE_ID is required  →  make download SEQUENCE_ID=<id>" && exit 1)
	go run ./cmd/download --sequence-id $(SEQUENCE_ID) --output $(OUTPUT)

render:
	go run ./cmd/render --output $(OUTPUT) --fps $(FPS) $(if $(PARAMS),--params $(PARAMS),) $(if $(TABULAR),--tabular $(TABULAR),)

# ML detection is opt-in: pass DETECT=1 to also run the fish detector over the
# fetched images/sonar frames (make markers PART_ID=<id> DETECT=1).
markers:
	@test -n "$(PART_ID)" || (echo "error: PART_ID is required  →  make markers PART_ID=<id>" && exit 1)
	go run ./cmd/markerplayback --part-id $(PART_ID) --output $(OUTPUT) \
		$(if $(ORG_ID),--org-id $(ORG_ID),) $(if $(START),--start $(START),) $(if $(END),--end $(END),) \
		$(if $(DETECT),--detect --model-dir $(MODEL_DIR) --confidence $(CONFIDENCE),)

detect:
	@test -n "$(IMAGE)" || (echo "error: IMAGE is required  →  make detect IMAGE=<path>" && exit 1)
	go run ./cmd/detect --model-dir $(MODEL_DIR) --image $(IMAGE) --confidence $(CONFIDENCE)

build:
	go build -o bin/download       ./cmd/download
	go build -o bin/render         ./cmd/render
	go build -o bin/markerplayback ./cmd/markerplayback
	go build -o bin/detect         ./cmd/detect


# make markers PART_ID=f79e293c-612f-496b-b26d-5b8bbaab6524 ORG_ID=4a0a99c7-e680-4cb5-acb1-0bd21449b455 START=2026-07-06T04:00:00Z END=2026-07-07T04:00:00Z (checkmate)
# make markers PART_ID=ce6d0f26-aeea-48dd-be34-81e4db0f807e ORG_ID=4a0a99c7-e680-4cb5-acb1-0bd21449b455 (synth sim)
# add DETECT=1 to either of the above to also run ML fish detection on the fetched images/sonar frames