# E21 工程化常用命令
.PHONY: build run zebra mcp test vet lint eval eval-redteam fmt clean docker-up docker-down

build:
	go build -o bin/zebra-server ./cmd/server
	go build -o bin/zebra ./cmd/zebra
	go build -o bin/zebra-mcp ./cmd/mcp

run: ## 启动企业版服务
	go run ./cmd/server

zebra: ## 本地 CLI 客户端
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

eval-redteam: ## 红队评测：注入/越狱用例安全分门槛（需真实模型）
	ZEBRA_EVAL=1 go test ./test/eval/ -run RedTeam -v

fmt: ## 格式化全部 Go 代码
	gofmt -w cmd internal

clean: ## 清理本地构建产物
	rm -rf bin

docker-up: ## 一键起 ollama+qdrant+zebra
	docker compose up --build

docker-down:
	docker compose down
