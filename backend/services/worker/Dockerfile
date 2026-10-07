FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY backend/services/worker ./backend/services/worker
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /worker ./backend/services/worker
FROM alpine:3.22
COPY --from=build /worker /worker
USER 65532:65532
ENTRYPOINT ["/worker"]
