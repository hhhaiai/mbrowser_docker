FROM golang:1.23 AS builder

WORKDIR /app

ENV GOPROXY=https://proxy.golang.org,direct
ENV GOSUMDB=sum.golang.org

COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/miui_proxy .

FROM gcr.io/distroless/base-debian12

WORKDIR /app
COPY --from=builder /out/miui_proxy /usr/local/bin/miui_proxy

ENV PORT=8080
ENV DB_PATH=/app/miui.db

EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD ["/usr/local/bin/miui_proxy", "--health-check"] || exit 1
ENTRYPOINT ["/usr/local/bin/miui_proxy"]
