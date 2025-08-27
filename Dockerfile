FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o redirector .
 
FROM alpine:3.18
RUN apk add --no-cache ca-certificates
WORKDIR /
COPY --from=builder /app/redirector /redirector
COPY domains.yaml /domains.yaml
EXPOSE 3000
CMD ["/redirector"]
