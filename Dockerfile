FROM golang:1.16-alpine AS builder

WORKDIR /app

COPY go.mod .
COPY go.sum .

RUN go mod download

COPY . .

# Build the Go app
RUN go build -o ./out/go-oppai .

FROM alpine:3.9

WORKDIR /app

RUN mkdir -p /data/beatmaps

COPY --from=builder /app/out .

EXPOSE 5000

CMD ["./go-oppai"]
