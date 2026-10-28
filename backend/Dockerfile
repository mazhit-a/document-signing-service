FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/seed ./cmd/seed \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate \
 && mkdir -p /out/data/files

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /out/seed /out/migrate /app/
COPY --from=build --chown=65532:65532 /out/data /data
ENV STORAGE_DIR=/data/files HTTP_ADDR=:8080
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/app/api"]
