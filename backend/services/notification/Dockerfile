FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY backend/services/notification ./backend/services/notification
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /notification ./backend/services/notification
FROM alpine:3.22
COPY --from=build /notification /notification
USER 65532:65532
ENTRYPOINT ["/notification"]
