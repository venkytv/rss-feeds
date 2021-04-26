FROM golang:1.16 AS builder
WORKDIR /go
COPY go.mod go.sum *.go .
ENV GOPATH=
RUN CGO_ENABLED=0 GOOS=linux go build

FROM alpine:latest
RUN apk --no-cache add ca-certificates
COPY --from=builder /go/atlasobscura /atlasobscura
ENTRYPOINT ["/atlasobscura"]
