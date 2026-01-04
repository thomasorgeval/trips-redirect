FROM golang:1.25-alpine AS builder
WORKDIR /app
# Install C build tools needed for CGO
RUN apk add --no-cache gcc musl-dev
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Build with CGO enabled
RUN CGO_ENABLED=1 go build -o redirector .

FROM alpine:3.18
RUN apk add --no-cache ca-certificates
WORKDIR /
COPY --from=builder /app/redirector /redirector
COPY domains.yaml /domains.yaml
EXPOSE 3000
CMD ["/redirector"]
