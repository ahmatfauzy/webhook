FROM golang:1.27-alpine AS builder
WORKDIR /app
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o webhooker ./cmd/webhooker

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/webhooker .
COPY --from=builder /app/migrations ./migrations
EXPOSE 8080
ENTRYPOINT ["./webhooker"]
