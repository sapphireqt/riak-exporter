FROM golang:1.20-alpine
WORKDIR /opt
COPY . .
RUN go mod download
RUN go build -o /bin/riak-exporter
