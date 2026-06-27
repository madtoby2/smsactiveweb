FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/sms-platform .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=build /out/sms-platform ./sms-platform
RUN mkdir /app/data && chown -R app:app /app
USER app
EXPOSE 3000
VOLUME ["/app/data"]
ENTRYPOINT ["/app/sms-platform"]
