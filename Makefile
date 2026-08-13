# E21 工程化常用命令
.PHONY: build run zebra test vet lint eval docker-up

build:
	go build -o bin/zebra-server ./cmd/server
	go build -o bin/zebra ./cmd/zebra

run: ## 启动企业版服务
	go run ./cmd/server

zebra: ## 单机 CLI 学习入口
	go run ./cmd/zebra

mcp: ## 独立 MCP 服务器 (HTTP)
	go run ./cmd/mcp -http :9000

test: ## 单元测试
	go test ./...

vet:
	go vet ./...

lint:
	test -x $(shell which golangci-lint) && golangci-lint run || go vet ./...

eval: ## LLM 黄金评测（需真实模型，如本地 Ollama）
	ZEBRA_EVAL=1 go test ./test/eval/ -v

docker-up: ## 一键起 ollama+qdrant+zebra
	docker compose up --build

docker-down:
	docker compose down
