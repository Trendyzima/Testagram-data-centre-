FROM golang:1.23-alpine AS build
WORKDIR /src
COPY main.go .
RUN go build -trimpath -ldflags="-s -w" -o /notification main.go
FROM alpine:3.22
COPY --from=build /notification /notification
USER 65532:65532
ENTRYPOINT ["/notification"]
