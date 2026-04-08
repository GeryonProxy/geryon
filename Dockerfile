FROM golang:1.23-alpine AS builder
WORKDIR /build
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o geryon ./cmd/geryon

FROM scratch
COPY --from=builder /build/geryon /geryon
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
EXPOSE 5432 3306 1433 8080 9090
ENTRYPOINT ["/geryon"]
