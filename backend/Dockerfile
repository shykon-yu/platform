FROM golang:1.24-alpine AS development
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata
COPY go.mod ./
RUN go mod download
COPY . .
EXPOSE 8080
CMD ["go", "run", "./cmd/api"]

FROM development AS build
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /pes8-api ./cmd/api

FROM gcr.io/distroless/static-debian12 AS production
COPY --from=build /pes8-api /pes8-api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/pes8-api"]
