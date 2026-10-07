FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY backend/services/platform ./backend/services/platform
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/platform ./backend/services/platform

FROM alpine:3.22
RUN adduser -D -H -u 10001 app
USER app
COPY --from=build /out/platform /usr/local/bin/platform
EXPOSE 8095
ENTRYPOINT ["/usr/local/bin/platform"]
