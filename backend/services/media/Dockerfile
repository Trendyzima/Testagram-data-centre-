FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY backend/services/media ./backend/services/media
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/media ./backend/services/media
FROM alpine:3.22
RUN adduser -D -H -u 10001 app
USER app
COPY --from=build /out/media /usr/local/bin/media
EXPOSE 8096
ENTRYPOINT ["/usr/local/bin/media"]
