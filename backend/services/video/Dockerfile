FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY backend/services/video ./backend/services/video
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/video ./backend/services/video
FROM alpine:3.22
RUN addgroup -S testagram && adduser -S -G testagram -u 65532 testagram
COPY --from=build /out/video /usr/local/bin/testagram-video
USER 65532:65532
EXPOSE 8790
ENTRYPOINT ["/usr/local/bin/testagram-video"]
