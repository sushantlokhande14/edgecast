FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /edgecast ./cmd/edgecast

FROM alpine:3.20
RUN apk add --no-cache ca-certificates curl
COPY --from=build /edgecast /usr/local/bin/edgecast
COPY scenarios /app/scenarios
ENTRYPOINT ["edgecast"]
