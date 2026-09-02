# Stage 1: build the backend binary.
#
# The Go version must be at least what backend/go.mod requires. This image and that
# directive drifting apart is what made `docker build` fail outright before.
FROM golang:1.26-alpine AS builder

WORKDIR /app
RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO is not needed: the backend never touches media, so it has no codec dependency.
# That is what keeps this image small and the binary static.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-w -s" -o /app/drs-backend ./cmd/server

# The TURN relay ships in the same image. It is a separate process because it wants the
# host's network directly, but building it here means one image, one Go version and one
# copy of the credential code shared with the API server -- which is the whole point of
# not using an external relay.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-w -s" -o /app/drs-turn ./cmd/turnserver

# Stage 2: runtime.
FROM alpine:3.20

WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 drs

# Migrations are embedded in the binary, so nothing else needs copying.
COPY --from=builder /app/drs-backend /app/drs-backend
COPY --from=builder /app/drs-turn /app/drs-turn

# Unprivileged: the process only needs to open a socket and talk to Postgres.
USER drs

EXPOSE 8080
ENTRYPOINT ["/app/drs-backend"]
