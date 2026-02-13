FROM golang:1.22-alpine AS builder

RUN apk add --no-cache gcc musl-dev linux-headers git make

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN make build

# --- Runtime ---
FROM alpine:3.19

RUN apk add --no-cache ca-certificates

COPY --from=builder /app/bin/inso-sequencer /usr/local/bin/inso-sequencer

EXPOSE 8545 8546

ENTRYPOINT ["inso-sequencer"]
CMD ["--config", "/etc/inso/config.yaml"]
