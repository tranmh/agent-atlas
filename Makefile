# agent-atlas — A2A agents on a world map behind agentgateway.
# `make help` lists the targets. Ports and settings come from .env (cp .env.example .env).
-include .env
export

GO_SERVICES := agent orchestrator visualizer atlasgen
COMPOSE     := docker compose

.DEFAULT_GOAL := help

.PHONY: help
help: ## List the targets
	@grep -E '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build all Go binaries into bin/
	@mkdir -p bin
	@for s in $(GO_SERVICES); do [ -d cmd/$$s ] && go build -o bin/$$s ./cmd/$$s || true; done

.PHONY: test
test: ## Go unit tests
	go test ./...

.PHONY: lint
lint: ## gofmt check + go vet + web lint
	@test -z "$$(gofmt -l cmd internal)" || (gofmt -l cmd internal; exit 1)
	go vet ./...
	cd web && npm run lint

.PHONY: web-test
web-test: ## Web lint + Vitest
	cd web && npm run lint && npm test

.PHONY: up
up: ## Build and start the stack, wait until healthy
	@test -f .env || (echo "missing .env: cp .env.example .env" && exit 1)
	$(COMPOSE) up -d --build --wait

.PHONY: down
down: ## Stop the stack
	$(COMPOSE) down

.PHONY: logs
logs: ## Follow logs (S=<service> for one)
	$(COMPOSE) logs -f $(S)

.PHONY: card
card: ## Fetch an agent card through the gateway (ID=<agent id>)
	@test -n "$(ID)" || (echo "usage: make card ID=<agent id>" && exit 1)
	@curl -s localhost:$(GATEWAY_HOST_PORT)/agents/$(ID)/.well-known/agent-card.json | jq .

.PHONY: gen
gen: ## Regenerate gateway config and compose agents from scenarios/*.yaml
	go run ./cmd/atlasgen

.PHONY: gen-check
gen-check: ## Fail if generated files are stale
	go run ./cmd/atlasgen -check

SCENARIO ?= sc
SPEED ?= 1
CHAOS ?= false
BREAKPOINTS ?= false

.PHONY: run
run: ## Start a run: SCENARIO=sc|ex|pm|tr|kh|all SPEED=1 CHAOS=true BREAKPOINTS=true
	@curl -sf -X POST localhost:$(VISUALIZER_HOST_PORT)/api/runs -H 'content-type: application/json' \
		-d '{"scenario":"$(SCENARIO)","speed":$(SPEED),"chaos":$(CHAOS),"breakpoints":$(BREAKPOINTS)}' | jq -c . \
		|| (echo "run failed: is the stack up (make up)?" && exit 1)

.PHONY: gate-release
gate-release: ## Release all breakpoint gates
	@curl -s -X POST localhost:$(VISUALIZER_HOST_PORT)/api/gate/release -d '{"all":true}' | jq -c .

.PHONY: tail-events
tail-events: ## Follow the live event stream as one line per event
	@curl -sN localhost:$(VISUALIZER_HOST_PORT)/api/events/stream | sed -un 's/^data: //p' | \
		jq -r --unbuffered 'select(.kind) | [.id, .scenario, .source, .kind, .from // "", .to // "", .note // ""] | @tsv'

.PHONY: clean-sessions
clean-sessions: ## Delete recorded sessions (data/sessions/*.jsonl)
	rm -f data/sessions/*.jsonl

.PHONY: web-dev
web-dev: ## Run the web app with hot reload on :3001 against the running stack
	cd web && npm run dev -- -p 3001

.PHONY: web-fixtures
web-fixtures: ## Run the web app on :3001 replaying the bundled sample (no backend)
	cd web && NEXT_PUBLIC_USE_FIXTURES=1 npm run dev -- -p 3001

.PHONY: record-sample
record-sample: ## Record web/public/samples/demo.jsonl: all 5 scenarios with chaos (stack must be up)
	@./scripts/record-sample.sh
