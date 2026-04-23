ARG BASE_REGISTRY=docker.m.daocloud.io/library
FROM ${BASE_REGISTRY}/golang:1.25-alpine AS builder

ARG GOPROXY=https://goproxy.cn,direct
ARG GOSUMDB=sum.golang.google.cn
ENV GOPROXY=${GOPROXY} \
    GOSUMDB=${GOSUMDB}

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /liteflow-backend ./cmd/server

FROM ${BASE_REGISTRY}/alpine:3.20
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /liteflow-backend .
COPY --from=builder /app/internal/platform/postgres/migrations ./migrations
COPY --from=builder /app/config/agents ./config/agents

EXPOSE 8081
CMD ["./liteflow-backend"]
