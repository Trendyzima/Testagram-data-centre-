FROM golang:1.23-alpine AS build
WORKDIR /src
COPY main.go .
RUN go build -trimpath -ldflags="-s -w" -o /worker main.go
FROM alpine:3.22
COPY --from=build /worker /worker
USER 65532:65532
ENTRYPOINT ["/worker"]
