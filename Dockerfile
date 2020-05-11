FROM golang:1.14.2-buster

RUN pwd
COPY main.go go.sum /go/
COPY rebalancer /go/rebalancer/
RUN pwd
RUN find .
RUN go get .
RUN go build .

