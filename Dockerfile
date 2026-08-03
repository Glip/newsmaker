# Build
FROM golang:1.23-bookworm AS builder
WORKDIR /src
COPY go.mod ./
COPY . .
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/newsmaker ./cmd/newsmaker

# Runtime with ffmpeg + Russian Trusted CA (Минцифры) for MAX platform-api2
FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg \
    && rm -rf /var/lib/apt/lists/*
COPY certs/russian_trusted_root_ca.crt certs/russian_trusted_sub_ca.crt /usr/local/share/ca-certificates/
RUN update-ca-certificates
WORKDIR /app
COPY --from=builder /out/newsmaker /app/newsmaker
COPY web /app/web
ENV DATA_DIR=/data
ENV WEB_DIR=/app/web
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/app/newsmaker"]
