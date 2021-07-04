FROM golang:1.16-alpine

WORKDIR /app

RUN mkdir -p /data/beatmaps

COPY go.mod .
COPY go.sum .

RUN go mod download

COPY . .

# Build the Go app
RUN go build -o ./out/go-oppai .

EXPOSE 5000

CMD ["./out/go-oppai"]
