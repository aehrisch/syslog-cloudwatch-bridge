FROM golang:1.15-buster as build

WORKDIR /go/src/app
ADD . /go/src/app

RUN go get -d -v ./...

RUN go build -o /go/bin/app

FROM gcr.io/distroless/base-debian10
COPY --from=build /go/bin/app /
EXPOSE 514
EXPOSE 514/udp
CMD ["/app"]
