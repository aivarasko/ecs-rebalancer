FROM golang:1.14.2-buster AS builder

WORKDIR /go/src/ecs-rebalancer
COPY . .
RUN make

FROM alpine:latest
WORKDIR /root/
COPY --from=builder /go/src/ecs-rebalancer/ecs-rebalancer .
CMD ["./ecs-rebalancer"]
