# ---- E21 工程化：多阶段构建（小体积镜像 + 分层缓存）----
# 构建阶段
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /zebra-server ./cmd/server

# 运行阶段（distroless，无 shell，最小攻击面）
FROM gcr.io/distroless/static-debian12
COPY --from=build /zebra-server /zebra-server
EXPOSE 8080
ENTRYPOINT ["/zebra-server"]
