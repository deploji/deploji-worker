FROM golang as builder
WORKDIR /go/src/github.com/sotomskir/mastermind-worker
COPY . .
RUN go get -d -v ./...
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /go/bin/mastermind-worker .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /go/bin/mastermind-worker .
COPY .env .
CMD ["./mastermind-worker"]