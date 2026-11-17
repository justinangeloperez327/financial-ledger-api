# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/financial-ledger-api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/financial-ledger-api /financial-ledger-api

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/financial-ledger-api"]
