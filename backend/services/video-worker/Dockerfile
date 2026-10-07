FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY backend/services/video-worker ./backend/services/video-worker
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/video-worker ./backend/services/video-worker
FROM alpine:3.22
RUN apk add --no-cache ffmpeg
RUN addgroup -S testagram && adduser -S -G testagram -u 65532 testagram
COPY --from=build /out/video-worker /usr/local/bin/testagram-video-worker
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/testagram-video-worker"]
