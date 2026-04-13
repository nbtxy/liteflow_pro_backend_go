FROM golang:1.23-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /liteflow-backend ./cmd/server

FROM alpine:3.20
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /liteflow-backend .
COPY --from=builder /app/internal/platform/postgres/migrations ./migrations

EXPOSE 8080
CMD ["./liteflow-backend"]
