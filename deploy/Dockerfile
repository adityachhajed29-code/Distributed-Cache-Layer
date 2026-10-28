FROM golang:1.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /cache . && CGO_ENABLED=0 go build -o /gateway ./cmd/gateway
FROM debian:bookworm-slim
WORKDIR /app
COPY --from=build /cache /gateway /app/
COPY deploy/nodes.json /app/nodes.json
ENTRYPOINT ["/app/cache"]
