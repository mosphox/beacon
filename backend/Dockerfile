FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/beacon .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates \
 && addgroup -g 1000 appgroup \
 && adduser -D -u 1000 -G appgroup appuser
WORKDIR /app
COPY --from=build /out/beacon /app/beacon
RUN mkdir -p /app/data && chown -R appuser:appgroup /app
USER appuser
EXPOSE 8000
ENTRYPOINT ["/app/beacon"]
