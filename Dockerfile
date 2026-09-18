# ---- 工程化：多阶段构建（小体积镜像 + 分层缓存）----
# 构建阶段
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /zebra-server ./cmd/server
# 准备运行时数据目录（distroless 无 shell，无法在运行阶段 mkdir）
RUN mkdir -p /app/workspace

# 运行阶段（distroless，无 shell，最小攻击面）
FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=build /zebra-server /zebra-server
COPY --from=build /app/workspace /app/workspace
# 运行时数据目录：技能 / 提示词 / 知识库(RAG+图谱) / 插件 / 沙箱工作区
COPY skills ./skills
COPY prompts ./prompts
COPY docs ./docs
COPY plugins ./plugins
ENV EXEC_WORKDIR=/app/workspace
EXPOSE 8080
ENTRYPOINT ["/zebra-server"]
